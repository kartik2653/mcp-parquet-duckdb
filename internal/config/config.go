package config

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the process configuration. Environment variables override the YAML file.
type Config struct {
	ListenAddr           string        `yaml:"listen_addr"`
	Transport            string        `yaml:"transport"`
	ParquetPath          string        `yaml:"parquet_path"`
	DatasetName          string        `yaml:"dataset_name"`
	DatasetDescription   string        `yaml:"dataset_description"`
	MetadataPath         string        `yaml:"metadata_path"`
	DuckDBMemoryLimit    string        `yaml:"duckdb_memory_limit"`
	TempDir              string        `yaml:"temp_dir"`
	QueryTimeout         time.Duration `yaml:"-"`
	MaxConcurrentQueries int           `yaml:"max_concurrent_queries"`
	MaxResultRows        int           `yaml:"max_result_rows"`
	MaxGroups            int           `yaml:"max_groups"`
	MaxHistogramBins     int           `yaml:"max_histogram_bins"`
	AuthToken            string        `yaml:"auth_token"`
	LogLevel             string        `yaml:"log_level"`
	DefaultTimezone      string        `yaml:"default_timezone"`
	MaxRequestBytes      int64         `yaml:"max_request_bytes"`
}

type fileConfig struct {
	ListenAddr           string `yaml:"listen_addr"`
	Transport            string `yaml:"transport"`
	ParquetPath          string `yaml:"parquet_path"`
	DatasetName          string `yaml:"dataset_name"`
	DatasetDescription   string `yaml:"dataset_description"`
	MetadataPath         string `yaml:"metadata_path"`
	DuckDBMemoryLimit    string `yaml:"duckdb_memory_limit"`
	TempDir              string `yaml:"temp_dir"`
	QueryTimeout         string `yaml:"query_timeout"`
	MaxConcurrentQueries *int   `yaml:"max_concurrent_queries"`
	MaxResultRows        *int   `yaml:"max_result_rows"`
	MaxGroups            *int   `yaml:"max_groups"`
	MaxHistogramBins     *int   `yaml:"max_histogram_bins"`
	AuthToken            string `yaml:"auth_token"`
	LogLevel             string `yaml:"log_level"`
	DefaultTimezone      string `yaml:"default_timezone"`
	MaxRequestBytes      *int64 `yaml:"max_request_bytes"`
}

func (f fileConfig) apply(cfg *Config) error {
	assign := func(src string, dest *string) {
		if src != "" {
			*dest = src
		}
	}
	assign(f.ListenAddr, &cfg.ListenAddr)
	assign(f.Transport, &cfg.Transport)
	assign(f.ParquetPath, &cfg.ParquetPath)
	assign(f.DatasetName, &cfg.DatasetName)
	assign(f.DatasetDescription, &cfg.DatasetDescription)
	assign(f.MetadataPath, &cfg.MetadataPath)
	assign(f.DuckDBMemoryLimit, &cfg.DuckDBMemoryLimit)
	assign(f.TempDir, &cfg.TempDir)
	assign(f.AuthToken, &cfg.AuthToken)
	assign(f.LogLevel, &cfg.LogLevel)
	assign(f.DefaultTimezone, &cfg.DefaultTimezone)
	if f.QueryTimeout != "" {
		d, err := time.ParseDuration(f.QueryTimeout)
		if err != nil {
			return fmt.Errorf("query timeout: %w", err)
		}
		cfg.QueryTimeout = d
	}
	if f.MaxConcurrentQueries != nil {
		cfg.MaxConcurrentQueries = *f.MaxConcurrentQueries
	}
	if f.MaxResultRows != nil {
		cfg.MaxResultRows = *f.MaxResultRows
	}
	if f.MaxGroups != nil {
		cfg.MaxGroups = *f.MaxGroups
	}
	if f.MaxHistogramBins != nil {
		cfg.MaxHistogramBins = *f.MaxHistogramBins
	}
	if f.MaxRequestBytes != nil {
		cfg.MaxRequestBytes = *f.MaxRequestBytes
	}
	return nil
}

// Load reads an optional YAML file from ANALYTICS_CONFIG or path, then applies
// ANALYTICS_* environment variables.
func Load(path string) (Config, error) {
	if path == "" {
		path = os.Getenv("ANALYTICS_CONFIG")
	}
	cfg := defaults()
	if path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		var file fileConfig
		if err := yaml.Unmarshal(body, &file); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		if err := file.apply(&cfg); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		ListenAddr:           ":8080",
		Transport:            "http",
		DatasetName:          "dataset",
		DatasetDescription:   "",
		DuckDBMemoryLimit:    "512MB",
		QueryTimeout:         30 * time.Second,
		MaxConcurrentQueries: 4,
		MaxResultRows:        100,
		MaxGroups:            100,
		MaxHistogramBins:     20,
		LogLevel:             "info",
		DefaultTimezone:      "UTC",
		MaxRequestBytes:      1 << 20,
	}
}

func applyEnv(cfg *Config) error {
	setString := func(key string, dest *string) {
		if v, ok := os.LookupEnv(key); ok {
			*dest = v
		}
	}
	setString("ANALYTICS_LISTEN_ADDR", &cfg.ListenAddr)
	setString("ANALYTICS_TRANSPORT", &cfg.Transport)
	setString("ANALYTICS_PARQUET_PATH", &cfg.ParquetPath)
	setString("ANALYTICS_DATASET_NAME", &cfg.DatasetName)
	setString("ANALYTICS_DATASET_DESCRIPTION", &cfg.DatasetDescription)
	setString("ANALYTICS_METADATA_PATH", &cfg.MetadataPath)
	setString("ANALYTICS_DUCKDB_MEMORY_LIMIT", &cfg.DuckDBMemoryLimit)
	setString("ANALYTICS_TEMP_DIR", &cfg.TempDir)
	setString("ANALYTICS_AUTH_TOKEN", &cfg.AuthToken)
	setString("ANALYTICS_LOG_LEVEL", &cfg.LogLevel)
	setString("ANALYTICS_DEFAULT_TIMEZONE", &cfg.DefaultTimezone)
	if v, ok := os.LookupEnv("ANALYTICS_QUERY_TIMEOUT"); ok && v != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("ANALYTICS_QUERY_TIMEOUT: %w", err)
		}
		cfg.QueryTimeout = d
	}
	setInt := func(key string, dest *int) error {
		if v, ok := os.LookupEnv(key); ok && v != "" {
			n, err := strconv.Atoi(v)
			if err != nil {
				return fmt.Errorf("%s: %w", key, err)
			}
			*dest = n
		}
		return nil
	}
	if err := setInt("ANALYTICS_MAX_CONCURRENT_QUERIES", &cfg.MaxConcurrentQueries); err != nil {
		return err
	}
	if err := setInt("ANALYTICS_MAX_RESULT_ROWS", &cfg.MaxResultRows); err != nil {
		return err
	}
	if err := setInt("ANALYTICS_MAX_GROUPS", &cfg.MaxGroups); err != nil {
		return err
	}
	if err := setInt("ANALYTICS_MAX_HISTOGRAM_BINS", &cfg.MaxHistogramBins); err != nil {
		return err
	}
	if v, ok := os.LookupEnv("ANALYTICS_MAX_REQUEST_BYTES"); ok && v != "" {
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			return fmt.Errorf("ANALYTICS_MAX_REQUEST_BYTES: %w", err)
		}
		cfg.MaxRequestBytes = n
	}
	return nil
}

// Validate checks required settings and normalizes paths.
func (c *Config) Validate() error {
	c.Transport = strings.ToLower(strings.TrimSpace(c.Transport))
	switch c.Transport {
	case "http", "stdio":
	default:
		return fmt.Errorf("transport must be http or stdio")
	}
	if strings.TrimSpace(c.ParquetPath) == "" {
		return fmt.Errorf("parquet path is required")
	}
	abs, err := filepath.Abs(c.ParquetPath)
	if err != nil {
		return fmt.Errorf("parquet path: %w", err)
	}
	c.ParquetPath = abs
	if c.TempDir == "" {
		c.TempDir = filepath.Join(os.TempDir(), "analytics-mcp")
	}
	tmp, err := filepath.Abs(c.TempDir)
	if err != nil {
		return fmt.Errorf("temp dir: %w", err)
	}
	c.TempDir = tmp
	if c.MetadataPath != "" {
		meta, err := filepath.Abs(c.MetadataPath)
		if err != nil {
			return fmt.Errorf("metadata path: %w", err)
		}
		c.MetadataPath = meta
	}
	if !memoryLimitPattern.MatchString(c.DuckDBMemoryLimit) {
		return fmt.Errorf("duckdb memory limit must look like 512MB")
	}
	if _, err := time.LoadLocation(c.DefaultTimezone); err != nil {
		return fmt.Errorf("default timezone: %w", err)
	}
	if c.QueryTimeout <= 0 {
		return fmt.Errorf("query timeout must be positive")
	}
	if c.MaxConcurrentQueries < 1 {
		return fmt.Errorf("max concurrent queries must be at least 1")
	}
	if c.MaxResultRows < 1 || c.MaxGroups < 1 {
		return fmt.Errorf("result limits must be at least 1")
	}
	if c.MaxHistogramBins < 1 || c.MaxHistogramBins > 100 {
		return fmt.Errorf("histogram bins must be between 1 and 100")
	}
	if c.MaxRequestBytes < 1 {
		return fmt.Errorf("max request bytes must be positive")
	}
	if c.Transport == "http" && strings.TrimSpace(c.AuthToken) == "" {
		return fmt.Errorf("auth token is required for http transport")
	}
	switch strings.ToLower(c.LogLevel) {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log level must be debug, info, warn, or error")
	}
	if strings.TrimSpace(c.DatasetName) == "" {
		return fmt.Errorf("dataset name is required")
	}
	return nil
}

// memoryLimitPattern is applied before the value is placed in a SET statement.
var memoryLimitPattern = regexp.MustCompile(`^[1-9][0-9]{0,6}(KB|MB|GB)$`)
