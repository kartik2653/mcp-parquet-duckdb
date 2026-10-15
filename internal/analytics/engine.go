package analytics

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Intellicars/intellicarai/internal/config"
	"github.com/Intellicars/intellicarai/internal/dataset"
	"github.com/Intellicars/intellicarai/internal/security"
	"github.com/duckdb/duckdb-go/v2"
)

// Engine runs allowlisted analytics against the configured Parquet datasets.
type Engine struct {
	db            *sql.DB
	catalog       *dataset.Catalog
	catalogs      map[string]*dataset.Catalog
	relationships []config.Relationship
	cfg           config.Config
	loc           *time.Location
	gate          *security.Gate
	log           *slog.Logger
}

// Open inspects the Parquet file and locks DuckDB to that file and the temp directory.
func Open(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}
	if len(cfg.Datasets) == 0 {
		info, err := os.Stat(cfg.ParquetPath)
		if err != nil || info.IsDir() {
			logger.Error("parquet dataset unavailable", "error", err)
			return nil, fmt.Errorf("parquet dataset is missing or unreadable")
		}
	} else {
		var missing []string
		for _, spec := range cfg.Datasets {
			info, statErr := os.Stat(spec.File)
			if statErr != nil || info.IsDir() {
				missing = append(missing, spec.Name+" ("+spec.File+")")
			}
		}
		if len(missing) > 0 {
			return nil, fmt.Errorf("missing datasets: %s", strings.Join(missing, ", "))
		}
	}
	if err := os.MkdirAll(cfg.TempDir, 0o700); err != nil {
		return nil, fmt.Errorf("temp directory is unavailable")
	}
	loc, err := time.LoadLocation(cfg.DefaultTimezone)
	if err != nil {
		return nil, fmt.Errorf("default timezone: %w", err)
	}
	db, err := openDatabase(ctx, cfg)
	if err != nil {
		logger.Error("duckdb open failed", "error", err)
		return nil, fmt.Errorf("analytics engine is unavailable")
	}
	catalogs := map[string]*dataset.Catalog{}
	var primary *dataset.Catalog
	if len(cfg.Datasets) == 0 {
		cols, err := dataset.DescribeRelation(ctx, db, "analytics_dataset")
		if err != nil {
			db.Close()
			logger.Error("schema inspection failed", "error", err)
			return nil, fmt.Errorf("parquet dataset is invalid")
		}
		meta, err := dataset.LoadMetadata(cfg.MetadataPath)
		if err != nil {
			db.Close()
			return nil, err
		}
		primary, err = dataset.NewCatalog(cfg.DatasetName, cfg.DatasetDescription, cols, meta)
		if err != nil {
			db.Close()
			return nil, err
		}
		if err := primary.SetRelation("analytics_dataset"); err != nil {
			db.Close()
			return nil, err
		}
		catalogs[primary.Name] = primary
	} else {
		for _, spec := range cfg.Datasets {
			cols, err := dataset.DescribeRelation(ctx, db, spec.Name)
			if err != nil {
				db.Close()
				logger.Error("schema inspection failed", "dataset", spec.Name, "error", err)
				return nil, fmt.Errorf("parquet dataset is invalid")
			}
			meta, err := dataset.LoadMetadata(spec.MetadataPath)
			if err != nil {
				db.Close()
				return nil, err
			}
			cat, err := dataset.NewCatalog(spec.Name, spec.Description, cols, meta)
			if err != nil {
				db.Close()
				return nil, err
			}
			if err := cat.SetRelation(spec.Name); err != nil {
				db.Close()
				return nil, err
			}
			if err := validateRelationshipColumns(cat, cfg.Relationships); err != nil {
				db.Close()
				return nil, err
			}
			catalogs[cat.Name] = cat
			if primary == nil {
				primary = cat
			}
		}
		if err := validateRelationshipPairs(catalogs, cfg.Relationships); err != nil {
			db.Close()
			return nil, err
		}
	}
	db.SetMaxOpenConns(cfg.MaxConcurrentQueries)
	return &Engine{
		db:            db,
		catalog:       primary,
		catalogs:      catalogs,
		relationships: append([]config.Relationship(nil), cfg.Relationships...),
		cfg:           cfg,
		loc:           loc,
		gate:          security.NewGate(cfg.MaxConcurrentQueries),
		log:           logger,
	}, nil
}

// Close releases DuckDB.
func (e *Engine) Close() error {
	if e.db == nil {
		return nil
	}
	return e.db.Close()
}

// Catalog returns the discovered schema.
func (e *Engine) Catalog() *dataset.Catalog { return e.catalog }

func openDatabase(ctx context.Context, cfg config.Config) (*sql.DB, error) {
	var once sync.Once
	var onceErr error
	connector, err := duckdb.NewConnector("", func(execer driver.ExecerContext) error {
		once.Do(func() {
			onceErr = configureDatabase(ctx, execer, cfg)
		})
		return onceErr
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

func configureDatabase(ctx context.Context, execer driver.ExecerContext, cfg config.Config) error {
	views := []struct{ name, path string }{{name: "analytics_dataset", path: cfg.ParquetPath}}
	if len(cfg.Datasets) > 0 {
		views = nil
		for _, spec := range cfg.Datasets {
			views = append(views, struct{ name, path string }{spec.Name, spec.File})
		}
	}
	var paths []string
	for _, view := range views {
		if strings.TrimSpace(view.path) == "" {
			continue
		}
		paths = append(paths, sqlQuote(view.path))
	}
	statements := []string{
		"SET memory_limit=" + sqlQuote(cfg.DuckDBMemoryLimit),
		"SET temp_directory=" + sqlQuote(cfg.TempDir),
		"SET max_temp_directory_size='1GB'",
		"SET TimeZone=" + sqlQuote(cfg.DefaultTimezone),
		"SET extension_directory=" + sqlQuote(cfg.TempDir),
		"SET autoinstall_known_extensions=false",
		"SET autoload_known_extensions=false",
		"SET allow_community_extensions=false",
		"SET allowed_paths=[" + strings.Join(paths, ", ") + "]",
		"SET allowed_directories=[" + sqlQuote(cfg.TempDir) + "]",
	}
	for _, view := range views {
		quoted, err := dataset.QuoteIdent(view.name)
		if err != nil {
			return err
		}
		statements = append(statements, "CREATE OR REPLACE VIEW "+quoted+" AS SELECT * FROM read_parquet("+sqlQuote(view.path)+")")
	}
	statements = append(statements,
		"SET enable_external_access=false",
		"SET lock_configuration=true",
	)
	for _, stmt := range statements {
		if _, err := execer.ExecContext(ctx, stmt, nil); err != nil {
			return fmt.Errorf("duckdb setup failed: %w", err)
		}
	}
	return nil
}

func sqlQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "''") + "'"
}

func (e *Engine) dataset(name string) (*dataset.Catalog, error) {
	if name == "" {
		if len(e.catalogs) == 1 {
			return e.catalog, nil
		}
		return nil, invalid("dataset is required")
	}
	cat, ok := e.catalogs[name]
	if !ok {
		return nil, invalid("unknown dataset")
	}
	return cat, nil
}

func validateRelationshipColumns(cat *dataset.Catalog, rels []config.Relationship) error {
	for _, rel := range rels {
		var keys []config.JoinKey
		switch cat.Name {
		case rel.Left:
			keys = rel.Keys
			for _, key := range keys {
				if _, ok := cat.Column(key.Left); !ok {
					return fmt.Errorf("relationship %s column %s is not in %s", rel.Name, key.Left, cat.Name)
				}
			}
		case rel.Right:
			for _, key := range rel.Keys {
				if _, ok := cat.Column(key.Right); !ok {
					return fmt.Errorf("relationship %s column %s is not in %s", rel.Name, key.Right, cat.Name)
				}
			}
		}
	}
	return nil
}

func validateRelationshipPairs(catalogs map[string]*dataset.Catalog, rels []config.Relationship) error {
	for _, rel := range rels {
		left := catalogs[rel.Left]
		right := catalogs[rel.Right]
		for _, key := range rel.Keys {
			lcol, _ := left.Column(key.Left)
			rcol, _ := right.Column(key.Right)
			if !kindsCompatible(lcol, rcol) {
				return fmt.Errorf("relationship %s keys %s and %s have incompatible types", rel.Name, key.Left, key.Right)
			}
		}
	}
	return nil
}

func kindsCompatible(left, right dataset.Column) bool {
	if left.Kind == right.Kind {
		return true
	}
	return left.Kind == dataset.KindNumeric && right.Kind == dataset.KindNumeric
}

func (e *Engine) query(ctx context.Context, q Query) ([]map[string]any, error) {
	if err := e.gate.Acquire(ctx); err != nil {
		return nil, &security.SafeError{Public: "query cancelled", Err: err}
	}
	defer e.gate.Release()
	ctx, cancel := context.WithTimeout(ctx, e.cfg.QueryTimeout)
	defer cancel()
	e.log.Info("query", "sql", q.SQL, "args", q.Args)
	rows, err := e.db.QueryContext(ctx, q.SQL, q.Args...)
	if err != nil {
		return nil, e.wrapQueryError(ctx, err)
	}
	defer rows.Close()
	scanned, err := scanAll(rows)
	if err != nil {
		return nil, e.wrapQueryError(ctx, err)
	}
	return scanned, nil
}

func (e *Engine) queryRow(ctx context.Context, q Query) (map[string]any, error) {
	rows, err := e.query(ctx, q)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]any{}, nil
	}
	return rows[0], nil
}

func (e *Engine) wrapQueryError(ctx context.Context, err error) error {
	e.log.Error("query failed", "error", err)
	if ctx.Err() != nil {
		return &security.SafeError{Public: "query cancelled", Err: err}
	}
	return &security.SafeError{Public: "query failed", Err: err}
}
