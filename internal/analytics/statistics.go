package analytics

import (
	"context"
	"fmt"
	"strings"

	"github.com/Intellicars/intellicarai/internal/dataset"
)

// ColumnStat is one column's statistical summary.
type ColumnStat struct {
	Column       string             `json:"column"`
	Count        *int64             `json:"count,omitempty"`
	Missing      *int64             `json:"missing,omitempty"`
	Mean         *float64           `json:"mean,omitempty"`
	StdDev       *float64           `json:"std_dev,omitempty"`
	Min          any                `json:"min,omitempty"`
	Max          any                `json:"max,omitempty"`
	Median       *float64           `json:"median,omitempty"`
	Variance     *float64           `json:"variance,omitempty"`
	Percentiles  map[string]float64 `json:"percentiles,omitempty"`
	Status       string             `json:"status,omitempty"`
	Outliers     *OutlierStat       `json:"outliers,omitempty"`
	Distribution *DistributionStat  `json:"distribution,omitempty"`
	Unit         string             `json:"unit,omitempty"`
}

// OutlierStat is a Tukey fence summary. It does not list outlier rows.
type OutlierStat struct {
	Method       string   `json:"method"`
	LowerFence   *float64 `json:"lower_fence,omitempty"`
	UpperFence   *float64 `json:"upper_fence,omitempty"`
	OutlierCount *int64   `json:"outlier_count,omitempty"`
	SampleSize   int64    `json:"sample_size"`
	Status       string   `json:"status"`
}

// DistributionStat is a bounded histogram.
type DistributionStat struct {
	Method   string    `json:"method"`
	Bins     []HistBin `json:"bins"`
	Status   string    `json:"status"`
	BinCount int       `json:"bin_count"`
}

// HistBin is one histogram bucket.
type HistBin struct {
	Bin   int     `json:"bin"`
	Lower float64 `json:"lower"`
	Upper float64 `json:"upper"`
	Count int64   `json:"count"`
}

// CorrelationStat is one Pearson correlation.
type CorrelationStat struct {
	Left       string         `json:"left"`
	Right      string         `json:"right"`
	Value      *float64       `json:"value"`
	SampleSize int64          `json:"sample_size"`
	Method     string         `json:"method"`
	Status     string         `json:"status"`
	Group      map[string]any `json:"group,omitempty"`
}

// GroupStat is a grouped statistical summary.
type GroupStat struct {
	Group   map[string]any `json:"group"`
	Columns []ColumnStat   `json:"columns"`
}

// StatsResult is the compact statistical response.
type StatsResult struct {
	Dataset      string            `json:"dataset"`
	Columns      []ColumnStat      `json:"columns,omitempty"`
	Groups       []GroupStat       `json:"groups,omitempty"`
	Correlations []CorrelationStat `json:"correlations,omitempty"`
	Truncated    bool              `json:"truncated"`
	Assumptions  []string          `json:"assumptions"`
}

// Statistics executes the requested statistical operations inside DuckDB.
func (e *Engine) Statistics(ctx context.Context, req StatsRequest) (StatsResult, error) {
	cat, err := e.dataset(req.Dataset)
	if err != nil {
		return StatsResult{}, err
	}
	if len(req.Columns) == 0 {
		return StatsResult{}, invalid("at least one column is required")
	}
	if len(req.Statistics) == 0 {
		return StatsResult{}, invalid("at least one statistic is required")
	}
	for _, stat := range req.Statistics {
		if !statAllowed(stat) {
			return StatsResult{}, invalid("unsupported statistic")
		}
	}
	cols := make([]dataset.Column, 0, len(req.Columns))
	for _, name := range req.Columns {
		col, err := requireColumn(cat, name)
		if err != nil {
			return StatsResult{}, err
		}
		cols = append(cols, col)
	}
	if containsStat(req.Statistics, "distribution") && len(req.GroupBy) > 0 {
		return StatsResult{}, invalid("distribution does not support group_by")
	}
	if containsStat(req.Statistics, "outliers") && len(req.GroupBy) > 0 {
		return StatsResult{}, invalid("outlier detection does not support group_by")
	}
	percentiles, err := normalizePercentiles(req)
	if err != nil {
		return StatsResult{}, err
	}
	limit, err := clampLimit(0, e.cfg.MaxResultRows, e.cfg.MaxGroups, len(req.GroupBy) > 0)
	if err != nil {
		return StatsResult{}, err
	}
	b := &sqlBuilder{loc: e.loc}
	where, err := b.where(cat, req.Filters, req.TimeColumn, req.TimeStart, req.TimeEnd)
	if err != nil {
		return StatsResult{}, err
	}
	summary, assumptions, err := e.summaryQuery(ctx, cat, cols, req, percentiles, where, b, limit)
	if err != nil {
		return StatsResult{}, err
	}
	result := StatsResult{Dataset: cat.Name, Assumptions: assumptions}
	if len(req.GroupBy) == 0 {
		if len(summary) == 0 {
			summary = []map[string]any{{}}
		}
		result.Columns = e.readColumnStats(summary[0], cols, req, percentiles)
	} else {
		rows, truncated := trimLimit(summary, limit)
		result.Truncated = truncated
		for _, row := range rows {
			group := map[string]any{}
			for _, name := range req.GroupBy {
				group[name] = row[name]
			}
			result.Groups = append(result.Groups, GroupStat{Group: group, Columns: e.readColumnStats(row, cols, req, percentiles)})
		}
	}
	if containsStat(req.Statistics, "correlation") {
		corr, truncated, err := e.correlation(ctx, cat, req, where, b.args, limit)
		if err != nil {
			return StatsResult{}, err
		}
		result.Correlations = corr
		result.Truncated = result.Truncated || truncated
	}
	if containsStat(req.Statistics, "outliers") && len(req.GroupBy) == 0 {
		if err := e.attachOutliers(ctx, cat, result.Columns, cols, where, b.args); err != nil {
			return StatsResult{}, err
		}
	}
	if containsStat(req.Statistics, "distribution") && len(req.GroupBy) == 0 {
		if err := e.attachDistributions(ctx, cat, result.Columns, cols, where, b.args, req.HistogramBins); err != nil {
			return StatsResult{}, err
		}
	}
	return result, nil
}

func statAllowed(stat string) bool {
	switch stat {
	case "count", "missing", "mean", "stddev", "min", "max", "median", "percentile", "variance", "correlation", "distribution", "outliers":
		return true
	default:
		return false
	}
}

func normalizePercentiles(req StatsRequest) ([]float64, error) {
	want := containsStat(req.Statistics, "percentile") || containsStat(req.Statistics, "median")
	if !want {
		return nil, nil
	}
	values := req.Percentiles
	if containsStat(req.Statistics, "median") && len(values) == 0 && !containsStat(req.Statistics, "percentile") {
		return []float64{0.5}, nil
	}
	if containsStat(req.Statistics, "percentile") && len(values) == 0 {
		values = []float64{0.25, 0.5, 0.75}
	}
	if containsStat(req.Statistics, "median") {
		values = append(values, 0.5)
	}
	seen := map[string]bool{}
	var out []float64
	for _, p := range values {
		if p < 0 || p > 1 {
			return nil, invalid("percentiles must be between 0 and 1")
		}
		key := formatFloat(p)
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, p)
	}
	if len(out) > 20 {
		return nil, invalid("at most 20 percentiles are allowed")
	}
	return out, nil
}

func (e *Engine) summaryQuery(ctx context.Context, cat *dataset.Catalog, cols []dataset.Column, req StatsRequest, percentiles []float64, where string, b *sqlBuilder, limit int) ([]map[string]any, []string, error) {
	var selectList []string
	assumptions := []string{
		"Nulls are ignored by aggregates.",
		"Duplicate rows are included.",
		"Standard deviation and variance are the sample statistics stddev_samp and var_samp.",
		"Median and percentiles use quantile_cont, Hyndman-Fan type 7.",
	}
	selectList = append(selectList, "count(*) AS row_count")
	for _, col := range cols {
		quoted, err := quote(col.Name)
		if err != nil {
			return nil, nil, err
		}
		selectList = append(selectList, "count("+quoted+") AS "+mustQuote(col.Name+"_count"))
		selectList = append(selectList, "(count(*) - count("+quoted+")) AS "+mustQuote(col.Name+"_missing"))
		if needsNumeric(req.Statistics) && col.Kind != dataset.KindNumeric && wantsNumericStat(req.Statistics) {
			if containsStat(req.Statistics, "mean") || containsStat(req.Statistics, "stddev") || containsStat(req.Statistics, "variance") || containsStat(req.Statistics, "median") || containsStat(req.Statistics, "percentile") || containsStat(req.Statistics, "outliers") || containsStat(req.Statistics, "distribution") {
				return nil, nil, invalid("this statistic requires a numeric column")
			}
		}
		if col.Kind == dataset.KindNumeric {
			if containsStat(req.Statistics, "mean") {
				selectList = append(selectList, "CAST(avg("+quoted+") AS DOUBLE) AS "+mustQuote(col.Name+"_mean"))
			}
			if containsStat(req.Statistics, "stddev") {
				selectList = append(selectList, "stddev_samp("+quoted+") AS "+mustQuote(col.Name+"_stddev"))
			}
			if containsStat(req.Statistics, "variance") {
				selectList = append(selectList, "var_samp("+quoted+") AS "+mustQuote(col.Name+"_variance"))
			}
			for _, p := range percentiles {
				if (p == 0.5 && containsStat(req.Statistics, "median")) || containsStat(req.Statistics, "percentile") {
					selectList = append(selectList, "quantile_cont("+quoted+", "+formatFloat(p)+") AS "+mustQuote(col.Name+"_p"+percentileKey(p)))
				}
			}
		}
		if containsStat(req.Statistics, "min") {
			selectList = append(selectList, "min("+quoted+") AS "+mustQuote(col.Name+"_min"))
		}
		if containsStat(req.Statistics, "max") {
			selectList = append(selectList, "max("+quoted+") AS "+mustQuote(col.Name+"_max"))
		}
	}
	var groupList []string
	for _, name := range req.GroupBy {
		col, err := requireColumn(cat, name)
		if err != nil {
			return nil, nil, err
		}
		quoted, err := quote(col.Name)
		if err != nil {
			return nil, nil, err
		}
		selectList = append(selectList, quoted)
		groupList = append(groupList, quoted)
	}
	sql := "SELECT " + strings.Join(selectList, ", ") + " FROM " + fromRelation(cat)
	if where != "" {
		sql += " WHERE " + where
	}
	if len(groupList) > 0 {
		sql += " GROUP BY " + strings.Join(groupList, ", ")
		sql += " ORDER BY " + strings.Join(groupList, ", ")
		sql += fmt.Sprintf(" LIMIT %d", limit+1)
	}
	rows, err := e.query(ctx, Query{SQL: sql, Args: b.args})
	if err != nil {
		return nil, nil, err
	}
	return rows, assumptions, nil
}

func wantsNumericStat(stats []string) bool {
	for _, stat := range stats {
		switch stat {
		case "mean", "stddev", "variance", "median", "percentile", "outliers", "distribution", "correlation":
			return true
		}
	}
	return false
}

func needsNumeric(stats []string) bool { return wantsNumericStat(stats) }

func percentileKey(p float64) string {
	return strings.ReplaceAll(formatFloat(p), ".", "_")
}

func (e *Engine) readColumnStats(row map[string]any, cols []dataset.Column, req StatsRequest, percentiles []float64) []ColumnStat {
	var out []ColumnStat
	for _, col := range cols {
		stat := ColumnStat{Column: col.Name, Unit: col.Unit}
		count, _ := asInt(row[col.Name+"_count"])
		missing, _ := asInt(row[col.Name+"_missing"])
		if containsStat(req.Statistics, "count") {
			stat.Count = &count
		}
		if containsStat(req.Statistics, "missing") {
			stat.Missing = &missing
		}
		if containsStat(req.Statistics, "mean") {
			stat.Mean, _ = asFloat(row[col.Name+"_mean"])
		}
		if containsStat(req.Statistics, "stddev") {
			stat.StdDev, _ = asFloat(row[col.Name+"_stddev"])
			if count < 2 {
				stat.Status = "insufficient_sample"
			}
		}
		if containsStat(req.Statistics, "variance") {
			stat.Variance, _ = asFloat(row[col.Name+"_variance"])
			if count < 2 {
				stat.Status = "insufficient_sample"
			}
		}
		if containsStat(req.Statistics, "min") {
			stat.Min = row[col.Name+"_min"]
		}
		if containsStat(req.Statistics, "max") {
			stat.Max = row[col.Name+"_max"]
		}
		if containsStat(req.Statistics, "median") {
			stat.Median, _ = asFloat(row[col.Name+"_p0_5"])
		}
		if containsStat(req.Statistics, "percentile") {
			stat.Percentiles = map[string]float64{}
			for _, p := range percentiles {
				if value, ok := asFloat(row[col.Name+"_p"+percentileKey(p)]); ok {
					stat.Percentiles[formatFloat(p)] = *value
				}
			}
		}
		out = append(out, stat)
	}
	return out
}

func (e *Engine) correlation(ctx context.Context, cat *dataset.Catalog, req StatsRequest, where string, args []any, limit int) ([]CorrelationStat, bool, error) {
	pair := req.Correlation
	if len(pair) == 0 && len(req.Columns) == 2 {
		pair = req.Columns
	}
	if len(pair) != 2 {
		return nil, false, invalid("correlation requires exactly two numeric columns")
	}
	left, err := requireColumn(cat, pair[0])
	if err != nil {
		return nil, false, err
	}
	right, err := requireColumn(cat, pair[1])
	if err != nil {
		return nil, false, err
	}
	if left.Kind != dataset.KindNumeric || right.Kind != dataset.KindNumeric {
		return nil, false, invalid("correlation requires numeric columns")
	}
	lq, err := quote(left.Name)
	if err != nil {
		return nil, false, err
	}
	rq, err := quote(right.Name)
	if err != nil {
		return nil, false, err
	}
	var selectList []string
	var groupList []string
	for _, name := range req.GroupBy {
		quoted, err := quote(name)
		if err != nil {
			return nil, false, err
		}
		selectList = append(selectList, quoted)
		groupList = append(groupList, quoted)
	}
	selectList = append(selectList,
		"corr("+lq+", "+rq+") AS coefficient",
		"count(*) FILTER (WHERE "+lq+" IS NOT NULL AND "+rq+" IS NOT NULL) AS sample_size",
		"var_samp("+lq+") AS left_variance",
		"var_samp("+rq+") AS right_variance",
	)
	sqlText := "SELECT " + strings.Join(selectList, ", ") + " FROM " + fromRelation(cat)
	if where != "" {
		sqlText += " WHERE " + where
	}
	if len(groupList) > 0 {
		sqlText += " GROUP BY " + strings.Join(groupList, ", ") + fmt.Sprintf(" LIMIT %d", limit+1)
	}
	rows, err := e.query(ctx, Query{SQL: sqlText, Args: args})
	if err != nil {
		return nil, false, err
	}
	rows, truncated := trimLimit(rows, limit)
	out := make([]CorrelationStat, 0, len(rows))
	for _, row := range rows {
		n, _ := asInt(row["sample_size"])
		leftVar, _ := asFloat(row["left_variance"])
		rightVar, _ := asFloat(row["right_variance"])
		value, ok := asFloat(row["coefficient"])
		status := "pearson_corr"
		if n < 2 {
			status = "insufficient_sample"
			ok = false
		} else if leftVar == nil || rightVar == nil || *leftVar == 0 || *rightVar == 0 {
			status = "constant_input"
			ok = false
		}
		item := CorrelationStat{
			Left: left.Name, Right: right.Name, SampleSize: n,
			Method: "pearson_population_corr", Status: status,
		}
		if ok {
			item.Value = value
		}
		if len(req.GroupBy) > 0 {
			item.Group = map[string]any{}
			for _, name := range req.GroupBy {
				item.Group[name] = row[name]
			}
		}
		out = append(out, item)
	}
	return out, truncated, nil
}

func (e *Engine) attachOutliers(ctx context.Context, cat *dataset.Catalog, stats []ColumnStat, cols []dataset.Column, where string, args []any) error {
	for i, col := range cols {
		if col.Kind != dataset.KindNumeric {
			return invalid("outlier detection requires a numeric column")
		}
		quoted, err := quote(col.Name)
		if err != nil {
			return err
		}
		sqlText := "SELECT quantile_cont(" + quoted + ", 0.25) AS q1, quantile_cont(" + quoted + ", 0.75) AS q3, count(" + quoted + ") AS sample_size FROM " + fromRelation(cat)
		if where != "" {
			sqlText += " WHERE " + where
		}
		row, err := e.queryRow(ctx, Query{SQL: sqlText, Args: args})
		if err != nil {
			return err
		}
		n, _ := asInt(row["sample_size"])
		q1, ok1 := asFloat(row["q1"])
		q3, ok3 := asFloat(row["q3"])
		outlier := &OutlierStat{Method: "tukey_iqr_k_1.5", SampleSize: n, Status: "insufficient_sample"}
		if ok1 && ok3 && n > 0 {
			iqr := *q3 - *q1
			lower := *q1 - 1.5*iqr
			upper := *q3 + 1.5*iqr
			outlier.LowerFence = &lower
			outlier.UpperFence = &upper
			countSQL := "SELECT count(*) AS outlier_count FROM " + fromRelation(cat) + " WHERE " + quoted + " IS NOT NULL AND (" + quoted + " < ? OR " + quoted + " > ?)"
			countArgs := append(append([]any{}, args...), lower, upper)
			if where != "" {
				countSQL = "SELECT count(*) AS outlier_count FROM " + fromRelation(cat) + " WHERE (" + where + ") AND " + quoted + " IS NOT NULL AND (" + quoted + " < ? OR " + quoted + " > ?)"
			}
			counted, err := e.queryRow(ctx, Query{SQL: countSQL, Args: countArgs})
			if err != nil {
				return err
			}
			count, _ := asInt(counted["outlier_count"])
			outlier.OutlierCount = &count
			outlier.Status = "tukey_fences"
		}
		stats[i].Outliers = outlier
	}
	return nil
}

func (e *Engine) attachDistributions(ctx context.Context, cat *dataset.Catalog, stats []ColumnStat, cols []dataset.Column, where string, args []any, bins int) error {
	if bins <= 0 || bins > e.cfg.MaxHistogramBins {
		bins = e.cfg.MaxHistogramBins
	}
	for i, col := range cols {
		if col.Kind != dataset.KindNumeric {
			return invalid("distribution requires a numeric column")
		}
		quoted, err := quote(col.Name)
		if err != nil {
			return err
		}
		sqlText := "SELECT min(CAST(" + quoted + " AS DOUBLE)) AS lo, max(CAST(" + quoted + " AS DOUBLE)) AS hi, count(" + quoted + ") AS sample_size FROM " + fromRelation(cat)
		if where != "" {
			sqlText += " WHERE " + where
		}
		bounds, err := e.queryRow(ctx, Query{SQL: sqlText, Args: args})
		if err != nil {
			return err
		}
		n, _ := asInt(bounds["sample_size"])
		dist := &DistributionStat{Method: "equal_width_floor_bins", BinCount: bins, Status: "insufficient_sample"}
		lo, okLo := asFloat(bounds["lo"])
		hi, okHi := asFloat(bounds["hi"])
		if okLo && okHi && n > 0 {
			if *lo == *hi {
				dist.Status = "single_value"
				dist.BinCount = 1
				dist.Bins = []HistBin{{Bin: 1, Lower: *lo, Upper: *hi, Count: n}}
			} else {
				expr := fmt.Sprintf("least(greatest(CAST(floor((CAST(%s AS DOUBLE) - %s) / ((%s - %s) / %d)) AS INTEGER) + 1, 1), %d)", quoted, formatFloat(*lo), formatFloat(*hi), formatFloat(*lo), bins, bins)
				histSQL := "SELECT " + expr + " AS bucket, count(*) AS n FROM " + fromRelation(cat)
				var histArgs []any
				if where != "" {
					histSQL += " WHERE (" + where + ") AND " + quoted + " IS NOT NULL"
					histArgs = append(histArgs, args...)
				} else {
					histSQL += " WHERE " + quoted + " IS NOT NULL"
				}
				histSQL += " GROUP BY 1 ORDER BY 1"
				rows, err := e.query(ctx, Query{SQL: histSQL, Args: histArgs})
				if err != nil {
					return err
				}
				counts := map[int]int64{}
				for _, row := range rows {
					bucket, _ := asInt(row["bucket"])
					count, _ := asInt(row["n"])
					counts[int(bucket)] = count
				}
				width := (*hi - *lo) / float64(bins)
				dist.Bins = make([]HistBin, bins)
				for b := 1; b <= bins; b++ {
					upper := *lo + float64(b)*width
					if b == bins {
						upper = *hi
					}
					dist.Bins[b-1] = HistBin{Bin: b, Lower: *lo + float64(b-1)*width, Upper: upper, Count: counts[b]}
				}
				dist.Status = "equal_width_bins"
			}
		}
		stats[i].Distribution = dist
	}
	return nil
}
