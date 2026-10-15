package analytics

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Intellicars/intellicarai/internal/config"
	"github.com/Intellicars/intellicarai/internal/dataset"
)

func TestCrossDatasetDoesNotInflateTotals(t *testing.T) {
	engine := openJoined(t)
	rejected, err := engine.CrossDataset(context.Background(), CrossInput{
		Sources: []CrossSourceInput{
			{Dataset: "distance", Alias: "d", Metrics: []MetricInput{{Column: "distance", Aggregation: "sum", Alias: "total_distance"}}},
			{Dataset: "charge", Alias: "c", Metrics: []MetricInput{{Column: "socgain", Aggregation: "sum", Alias: "total_soc"}}},
		},
		Joins: []CrossJoinInput{{Left: "d", Right: "c", Type: "inner", Relationship: "vehicle_charge"}},
	})
	if err == nil {
		t.Fatalf("expected fanout rejection, got %#v", rejected)
	}
	if !strings.Contains(err.Error(), "aggregate each dataset") {
		t.Fatalf("err %v", err)
	}

	result, err := engine.CrossDataset(context.Background(), CrossInput{
		Sources: []CrossSourceInput{
			{Dataset: "distance", Alias: "d", GroupBy: []string{"vehicleid"}, Metrics: []MetricInput{{Column: "distance", Aggregation: "sum", Alias: "total_distance"}}},
			{Dataset: "charge", Alias: "c", GroupBy: []string{"vehicleid"}, Metrics: []MetricInput{{Column: "socgain", Aggregation: "sum", Alias: "total_soc"}}},
		},
		Joins: []CrossJoinInput{{Left: "d", Right: "c", Type: "inner", Relationship: "vehicle_charge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 {
		t.Fatalf("rows %#v", result.Rows)
	}
	distance := result.Rows[0]["d_total_distance"]
	soc := result.Rows[0]["c_total_soc"]
	if distance != 20.0 || soc != 10.0 {
		t.Fatalf("inflated totals %#v warnings %v", result.Rows, result.Warnings)
	}
	if len(result.Warnings) == 0 {
		t.Fatal("expected socgain warning")
	}
}

func TestLeftJoinAndUnknownRelationship(t *testing.T) {
	engine := openJoined(t)
	result, err := engine.CrossDataset(context.Background(), CrossInput{
		Sources: []CrossSourceInput{
			{Dataset: "distance", Alias: "d", GroupBy: []string{"vehicleid"}, Metrics: []MetricInput{{Column: "distance", Aggregation: "sum", Alias: "total_distance"}}},
			{Dataset: "charge", Alias: "c", GroupBy: []string{"vehicleid"}, Metrics: []MetricInput{{Column: "socgain", Aggregation: "sum", Alias: "total_soc"}}},
		},
		Joins: []CrossJoinInput{{Left: "d", Right: "c", Type: "left", Relationship: "vehicle_charge"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 3 {
		t.Fatalf("left rows %#v", result.Rows)
	}
	var unmatched int
	for _, row := range result.Rows {
		if row["c_total_soc"] == nil {
			unmatched++
		}
	}
	if unmatched != 2 {
		t.Fatalf("unmatched %d %#v", unmatched, result.Rows)
	}
	_, err = engine.CrossDataset(context.Background(), CrossInput{
		Sources: []CrossSourceInput{
			{Dataset: "distance", Alias: "d", GroupBy: []string{"vehicleid"}, Metrics: []MetricInput{{Column: "distance", Aggregation: "sum"}}},
			{Dataset: "charge", Alias: "c", GroupBy: []string{"regno"}, Metrics: []MetricInput{{Column: "socgain", Aggregation: "sum"}}},
		},
		Joins: []CrossJoinInput{{Left: "d", Right: "c", Type: "inner", Relationship: "not_registered"}},
	})
	if err == nil || !strings.Contains(err.Error(), "no relationship") {
		t.Fatalf("err %v", err)
	}
}

func TestCompositeKeysAndMissingDataset(t *testing.T) {
	engine := openJoined(t)
	result, err := engine.CrossDataset(context.Background(), CrossInput{
		Sources: []CrossSourceInput{
			{Dataset: "distance", Alias: "d", GroupBy: []string{"fleetid", "vehicleid"}, Metrics: []MetricInput{{Column: "distance", Aggregation: "sum", Alias: "total_distance"}}},
			{Dataset: "charge", Alias: "c", GroupBy: []string{"fleetid", "vehicleid"}, Metrics: []MetricInput{{Column: "socgain", Aggregation: "sum", Alias: "total_soc"}}},
		},
		Joins: []CrossJoinInput{{Left: "d", Right: "c", Type: "inner", Relationship: "fleet_vehicle"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 || result.Rows[0]["d_total_distance"] != 20.0 {
		t.Fatalf("composite %#v", result.Rows)
	}
	missing := filepath.Join(t.TempDir(), "missing.parquet")
	cfg := config.Config{
		Transport: "stdio", DataDir: filepath.Dir(missing),
		Datasets:          []config.DatasetSpec{{Name: "charge", File: missing}},
		DuckDBMemoryLimit: "256MB", TempDir: t.TempDir(), QueryTimeout: time.Second,
		MaxConcurrentQueries: 1, MaxResultRows: 10, MaxGroups: 10, MaxHistogramBins: 5,
		LogLevel: "error", DefaultTimezone: "UTC", MaxRequestBytes: 1024,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	_, err = Open(context.Background(), cfg, nil)
	if err == nil || !strings.Contains(err.Error(), "missing datasets") {
		t.Fatalf("err %v", err)
	}
}

func openJoined(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	distance := filepath.Join(dir, "distance.parquet")
	charge := filepath.Join(dir, "charge.parquet")
	writeParquet(t, filepath.Join(dir, "distance.csv"), distance, "fleetid,vehicleid,distance\n1,v1,10\n1,v1,10\n1,v2,7\n,v3,3\n", map[string]string{"vehicleid": "VARCHAR", "distance": "DOUBLE"})
	writeParquet(t, filepath.Join(dir, "charge.csv"), charge, "fleetid,vehicleid,socgain\n1,v1,5\n1,v1,5\n2,v9,4\n", map[string]string{"vehicleid": "VARCHAR", "socgain": "DOUBLE"})
	cfg := config.Config{
		Transport: "stdio", DataDir: dir,
		Datasets: []config.DatasetSpec{
			{Name: "distance", File: distance, Description: "trips"},
			{Name: "charge", File: charge, Description: "charges"},
		},
		Relationships: []config.Relationship{
			{Name: "vehicle_charge", Left: "distance", Right: "charge", Cardinality: "many_to_many", Keys: []config.JoinKey{{Left: "vehicleid", Right: "vehicleid"}}},
			{Name: "fleet_vehicle", Left: "distance", Right: "charge", Cardinality: "many_to_many", Keys: []config.JoinKey{{Left: "fleetid", Right: "fleetid"}, {Left: "vehicleid", Right: "vehicleid"}}},
		},
		DuckDBMemoryLimit: "256MB", TempDir: filepath.Join(dir, "tmp"),
		QueryTimeout: 5 * time.Second, MaxConcurrentQueries: 2, MaxResultRows: 10,
		MaxGroups: 10, MaxHistogramBins: 5, LogLevel: "error", DefaultTimezone: "UTC", MaxRequestBytes: 1024,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	engine, err := Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	return engine
}

func writeParquet(t *testing.T, csvPath, parquet, body string, types map[string]string) {
	t.Helper()
	if err := os.WriteFile(csvPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := dataset.Convert(context.Background(), dataset.ConvertOptions{
		Input: csvPath, Output: parquet, Header: true, Types: types,
	}); err != nil {
		t.Fatal(err)
	}
}
