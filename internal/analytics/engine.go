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

// Engine runs allowlisted analytics against one Parquet dataset.
type Engine struct {
	db      *sql.DB
	catalog *dataset.Catalog
	cfg     config.Config
	loc     *time.Location
	gate    *security.Gate
	log     *slog.Logger
}

// Open inspects the Parquet file and locks DuckDB to that file and the temp directory.
func Open(ctx context.Context, cfg config.Config, logger *slog.Logger) (*Engine, error) {
	if logger == nil {
		logger = slog.Default()
	}
	info, err := os.Stat(cfg.ParquetPath)
	if err != nil || info.IsDir() {
		logger.Error("parquet dataset unavailable", "error", err)
		return nil, fmt.Errorf("parquet dataset is missing or unreadable")
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
	catalog, err := dataset.NewCatalog(cfg.DatasetName, cfg.DatasetDescription, cols, meta)
	if err != nil {
		db.Close()
		return nil, err
	}
	db.SetMaxOpenConns(cfg.MaxConcurrentQueries)
	return &Engine{
		db:      db,
		catalog: catalog,
		cfg:     cfg,
		loc:     loc,
		gate:    security.NewGate(cfg.MaxConcurrentQueries),
		log:     logger,
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
	statements := []string{
		"SET memory_limit=" + sqlQuote(cfg.DuckDBMemoryLimit),
		"SET temp_directory=" + sqlQuote(cfg.TempDir),
		"SET max_temp_directory_size='1GB'",
		"SET TimeZone=" + sqlQuote(cfg.DefaultTimezone),
		"SET extension_directory=" + sqlQuote(cfg.TempDir),
		"SET autoinstall_known_extensions=false",
		"SET autoload_known_extensions=false",
		"SET allow_community_extensions=false",
		"SET allowed_paths=[" + sqlQuote(cfg.ParquetPath) + "]",
		"SET allowed_directories=[" + sqlQuote(cfg.TempDir) + "]",
		"CREATE OR REPLACE VIEW analytics_dataset AS SELECT * FROM read_parquet(" + sqlQuote(cfg.ParquetPath) + ")",
		"SET enable_external_access=false",
		"SET lock_configuration=true",
	}
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

func (e *Engine) query(ctx context.Context, q Query) ([]map[string]any, error) {
	if err := e.gate.Acquire(ctx); err != nil {
		return nil, &security.SafeError{Public: "query cancelled", Err: err}
	}
	defer e.gate.Release()
	ctx, cancel := context.WithTimeout(ctx, e.cfg.QueryTimeout)
	defer cancel()
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
