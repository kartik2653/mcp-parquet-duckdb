package dataset

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

// Convert reads the CSV with DuckDB and writes Parquet without loading rows into Go.
// The output file is published only after the Parquet file validates.
func Convert(ctx context.Context, opt ConvertOptions) (ConvertResult, error) {
	started := time.Now()
	if err := validateOptions(&opt); err != nil {
		return ConvertResult{}, err
	}
	db, err := openConverter(ctx)
	if err != nil {
		return ConvertResult{}, err
	}
	defer db.Close()

	inputRows, err := countCSV(ctx, db, opt)
	if err != nil {
		return ConvertResult{}, err
	}
	tmp := opt.Output + ".partial"
	defer os.Remove(tmp)
	if err := writeParquet(ctx, db, opt, tmp); err != nil {
		return ConvertResult{}, err
	}
	outputRows, cols, err := validateParquet(ctx, db, tmp)
	if err != nil {
		return ConvertResult{}, err
	}
	if inputRows != outputRows {
		return ConvertResult{}, fmt.Errorf("row count mismatch: csv %d parquet %d", inputRows, outputRows)
	}
	info, err := os.Stat(tmp)
	if err != nil {
		return ConvertResult{}, err
	}
	if err := os.Rename(tmp, opt.Output); err != nil {
		return ConvertResult{}, fmt.Errorf("publish parquet: %w", err)
	}
	return ConvertResult{
		InputRows:   inputRows,
		OutputRows:  outputRows,
		Columns:     cols,
		OutputBytes: info.Size(),
		Duration:    time.Since(started),
	}, nil
}

func openConverter(ctx context.Context) (*sql.DB, error) {
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		for _, stmt := range []string{
			"SET memory_limit='512MB'",
			"SET autoinstall_known_extensions=false",
			"SET autoload_known_extensions=false",
			"SET allow_community_extensions=false",
		} {
			if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	db := sql.OpenDB(connector)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func countCSV(ctx context.Context, db *sql.DB, opt ConvertOptions) (int64, error) {
	query, args := csvFrom(opt, "count(*)")
	var n int64
	if err := db.QueryRowContext(ctx, query, args...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count csv: %w", err)
	}
	return n, nil
}

func writeParquet(ctx context.Context, db *sql.DB, opt ConvertOptions, dest string) error {
	selectSQL, args := csvFrom(opt, "*")
	query := fmt.Sprintf("COPY (%s) TO %s (FORMAT parquet)", selectSQL, sqlString(dest))
	if _, err := db.ExecContext(ctx, query, args...); err != nil {
		return fmt.Errorf("write parquet: %w", err)
	}
	return nil
}

func validateParquet(ctx context.Context, db *sql.DB, path string) (int64, []Column, error) {
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM read_parquet("+sqlString(path)+")").Scan(&n); err != nil {
		return 0, nil, fmt.Errorf("parquet is invalid")
	}
	cols, err := describeQuery(ctx, db, "SELECT * FROM read_parquet("+sqlString(path)+")")
	if err != nil {
		return 0, nil, fmt.Errorf("parquet is invalid")
	}
	if len(cols) == 0 {
		return 0, nil, fmt.Errorf("parquet schema is empty")
	}
	for _, col := range cols {
		if !ValidIdent(col.Name) {
			return 0, nil, fmt.Errorf("column %q is not a supported identifier; rename it to letters, digits, and underscores", col.Name)
		}
	}
	return n, cols, nil
}

func csvFrom(opt ConvertOptions, projection string) (string, []any) {
	duckTypes, epochCols, strptimeCols := splitTypes(opt.Types)
	selectList := projection
	if projection == "*" {
		selectList = selectProjection(opt, epochCols, strptimeCols)
	}
	parts := []string{
		"SELECT " + selectList + " FROM read_csv(" + sqlString(opt.Input) + ", header = " + boolSQL(opt.Header) + ", delim = " + sqlString(opt.Delimiter) + ", null_padding = true",
	}
	if len(opt.NullStrings) > 0 {
		literals := make([]string, len(opt.NullStrings))
		for i, s := range opt.NullStrings {
			literals[i] = sqlString(s)
		}
		parts = append(parts, ", nullstr = ["+strings.Join(literals, ", ")+"]")
	}
	if opt.SampleSize != nil {
		parts = append(parts, ", sample_size = "+strconv.Itoa(*opt.SampleSize))
	}
	if len(duckTypes) > 0 {
		pairs := make([]string, 0, len(duckTypes))
		for _, name := range sortedKeys(duckTypes) {
			pairs = append(pairs, sqlString(name)+": "+sqlString(duckTypes[name]))
		}
		parts = append(parts, ", types = {"+strings.Join(pairs, ", ")+"}")
	}
	parts = append(parts, ")")
	return strings.Join(parts, ""), nil
}

func splitTypes(types map[string]string) (map[string]string, []string, map[string]string) {
	duckTypes := map[string]string{}
	var epochCols []string
	strptimeCols := map[string]string{}
	for _, name := range sortedKeys(types) {
		if types[name] == "EPOCH_MS" {
			epochCols = append(epochCols, name)
			continue
		}
		if format, ok := strings.CutPrefix(types[name], "STRPTIME:"); ok {
			strptimeCols[name] = format
			continue
		}
		duckTypes[name] = types[name]
	}
	return duckTypes, epochCols, strptimeCols
}

func selectProjection(opt ConvertOptions, epochCols []string, strptimeCols map[string]string) string {
	renamed := map[string]bool{}
	var excludes []string
	var extras []string
	for _, src := range sortedKeys(opt.Rename) {
		dst := opt.Rename[src]
		renamed[dst] = true
		excludes = append(excludes, looseQuote(src))
		extras = append(extras, convertExpr(looseQuote(src), dst, epochCols, strptimeCols)+" AS "+mustIdent(dst))
	}
	var replaces []string
	for _, name := range epochCols {
		if renamed[name] {
			continue
		}
		quoted := mustIdent(name)
		replaces = append(replaces, "epoch_ms(CAST("+quoted+" AS BIGINT)) AS "+quoted)
	}
	for _, name := range sortedKeys(strptimeCols) {
		if renamed[name] {
			continue
		}
		quoted := mustIdent(name)
		replaces = append(replaces, "strptime(CAST("+quoted+" AS VARCHAR), "+sqlString(strptimeCols[name])+") AS "+quoted)
	}
	list := "*"
	if len(excludes) > 0 {
		list = "* EXCLUDE (" + strings.Join(excludes, ", ") + ")"
	} else if len(replaces) > 0 {
		return "* REPLACE (" + strings.Join(replaces, ", ") + ")"
	}
	if len(replaces) > 0 {
		list += " REPLACE (" + strings.Join(replaces, ", ") + ")"
	}
	if len(extras) > 0 {
		list += ", " + strings.Join(extras, ", ")
	}
	return list
}

func convertExpr(source, dest string, epochCols []string, strptimeCols map[string]string) string {
	for _, name := range epochCols {
		if name == dest {
			return "epoch_ms(CAST(" + source + " AS BIGINT))"
		}
	}
	if format, ok := strptimeCols[dest]; ok {
		return "strptime(CAST(" + source + " AS VARCHAR), " + sqlString(format) + ")"
	}
	return source
}

func looseQuote(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func mustIdent(name string) string {
	quoted, err := QuoteIdent(name)
	if err != nil {
		return looseQuote(name)
	}
	return quoted
}

func describeQuery(ctx context.Context, db *sql.DB, query string) ([]Column, error) {
	rows, err := db.QueryContext(ctx, "DESCRIBE "+query)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var cols []Column
	for rows.Next() {
		values := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		rec := map[string]string{}
		for i, name := range names {
			rec[strings.ToLower(name)] = scalarString(values[i])
		}
		col := Column{
			Name:     rec["column_name"],
			DuckType: rec["column_type"],
			Nullable: strings.EqualFold(rec["null"], "YES"),
		}
		col.Kind = classify(col.DuckType)
		col.ApprovedAggregations = defaultAggregations(col.Kind)
		cols = append(cols, col)
	}
	return cols, rows.Err()
}

// DescribeRelation reads the schema of a validated view or table.
func DescribeRelation(ctx context.Context, db *sql.DB, relation string) ([]Column, error) {
	quoted, err := QuoteIdent(relation)
	if err != nil {
		return nil, err
	}
	return describeQuery(ctx, db, "SELECT * FROM "+quoted)
}

func classify(duckType string) Kind {
	u := strings.ToUpper(strings.TrimSpace(duckType))
	switch {
	case u == "BOOLEAN":
		return KindBoolean
	case u == "DATE":
		return KindDate
	case strings.HasPrefix(u, "TIME ") || u == "TIME":
		return KindTime
	case strings.Contains(u, "TIME ZONE") || strings.HasPrefix(u, "TIMESTAMPTZ"):
		return KindTimestampTZ
	case strings.HasPrefix(u, "TIMESTAMP"):
		return KindTimestamp
	case strings.HasPrefix(u, "DECIMAL"), u == "FLOAT", u == "DOUBLE", u == "REAL", strings.Contains(u, "INT"):
		return KindNumeric
	default:
		return KindCategorical
	}
}

func defaultAggregations(kind Kind) []string {
	if kind == KindNumeric {
		return []string{"count", "count_distinct", "sum", "avg", "min", "max"}
	}
	return []string{"count", "count_distinct", "min", "max"}
}

func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case []byte:
		return string(t)
	default:
		return fmt.Sprint(t)
	}
}

func sqlString(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func boolSQL(v bool) string {
	if v {
		return "true"
	}
	return "false"
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	for i := 1; i < len(keys); i++ {
		for j := i; j > 0 && keys[j] < keys[j-1]; j-- {
			keys[j], keys[j-1] = keys[j-1], keys[j]
		}
	}
	return keys
}
