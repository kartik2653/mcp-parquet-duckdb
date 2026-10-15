package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadYAMLAndEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	body := []byte("transport: stdio\nparquet_path: ./data.parquet\nquery_timeout: 5s\ndataset_name: fleet\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANALYTICS_CONFIG", "")
	t.Setenv("ANALYTICS_DATASET_NAME", "override")
	t.Setenv("ANALYTICS_AUTH_TOKEN", "")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Transport != "stdio" {
		t.Fatalf("transport %s", cfg.Transport)
	}
	if cfg.DatasetName != "override" {
		t.Fatalf("dataset %s", cfg.DatasetName)
	}
	if cfg.QueryTimeout != 5*time.Second {
		t.Fatalf("timeout %s", cfg.QueryTimeout)
	}
	if !filepath.IsAbs(cfg.ParquetPath) {
		t.Fatalf("parquet path %s", cfg.ParquetPath)
	}
}

func TestHTTPRequiresToken(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("ANALYTICS_TRANSPORT", "http")
	t.Setenv("ANALYTICS_PARQUET_PATH", filepath.Join(dir, "data.parquet"))
	t.Setenv("ANALYTICS_AUTH_TOKEN", "")
	t.Setenv("ANALYTICS_CONFIG", "")
	if _, err := Load(""); err == nil {
		t.Fatal("expected missing token error")
	}
}

func TestDatasetPathMustStayInDataDir(t *testing.T) {
	dir := t.TempDir()
	cfg := Config{
		Transport: "stdio", DataDir: filepath.Join(dir, "parquet"),
		Datasets: []DatasetSpec{{
			Name: "charge", File: filepath.Join(dir, "secret.parquet"),
		}},
		DuckDBMemoryLimit: "256MB", QueryTimeout: time.Second,
		MaxConcurrentQueries: 1, MaxResultRows: 10, MaxGroups: 10, MaxHistogramBins: 5,
		LogLevel: "error", DefaultTimezone: "UTC", MaxRequestBytes: 1024,
	}
	if err := cfg.Validate(); err == nil {
		t.Fatal("expected path rejection")
	}
}
