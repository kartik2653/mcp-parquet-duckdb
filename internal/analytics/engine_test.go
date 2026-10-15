package analytics

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Intellicars/intellicarai/internal/config"
	"github.com/Intellicars/intellicarai/internal/dataset"
)

func openTrips(t *testing.T) *Engine {
	t.Helper()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "trips.csv")
	if err := os.WriteFile(csvPath, []byte(miniFleetCSV), 0o644); err != nil {
		t.Fatal(err)
	}
	parquet := filepath.Join(dir, "trips.parquet")
	if _, err := dataset.Convert(context.Background(), dataset.ConvertOptions{
		Input:  csvPath,
		Output: parquet,
		Header: true,
		Types:  map[string]string{"vehicleid": "VARCHAR", "regno": "VARCHAR", "chassisno": "VARCHAR", "rangevalue": "DOUBLE"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Transport:            "stdio",
		ParquetPath:          parquet,
		DatasetName:          "trips",
		DatasetDescription:   "fleet",
		DuckDBMemoryLimit:    "256MB",
		TempDir:              filepath.Join(dir, "tmp"),
		QueryTimeout:         5 * time.Second,
		MaxConcurrentQueries: 2,
		MaxResultRows:        10,
		MaxGroups:            10,
		MaxHistogramBins:     5,
		LogLevel:             "error",
		DefaultTimezone:      "UTC",
		MaxRequestBytes:      1024,
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

func TestAnalyzeAverageAndNulls(t *testing.T) {
	engine := openTrips(t)
	result, err := engine.Analyze(context.Background(), AnalyzeInput{
		Metrics: []MetricInput{{Column: "distance", Aggregation: "avg"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.RowCount != 1 {
		t.Fatalf("rows %d", result.RowCount)
	}
	got, _ := asFloat(result.Rows[0]["avg_distance"])
	if got == nil || math.Abs(*got-160.5/7) > 1e-9 {
		t.Fatalf("avg %#v", result.Rows[0])
	}
	sample, _ := asInt(result.Rows[0]["avg_distance_n"])
	if sample != 7 {
		t.Fatalf("sample %d", sample)
	}

	missing, err := engine.StatisticalAnalysis(context.Background(), StatsInput{
		Columns:    []string{"distance"},
		Statistics: []string{"count", "missing"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if missing.Columns[0].Count == nil || *missing.Columns[0].Count != 7 {
		t.Fatalf("count %#v", missing.Columns[0])
	}
	if missing.Columns[0].Missing == nil || *missing.Columns[0].Missing != 1 {
		t.Fatalf("missing %#v", missing.Columns[0])
	}
}

func TestTopNTruncationAndSort(t *testing.T) {
	engine := openTrips(t)
	result, err := engine.Analyze(context.Background(), AnalyzeInput{
		Metrics: []MetricInput{{Aggregation: "count"}},
		GroupBy: []string{"vehicleid"},
		Sort:    []SortInput{{Field: "row_count", Direction: "desc"}},
		Limit:   1,
	})
	if err != nil {
		t.Fatal(err)
	}
	if !result.Truncated || result.RowCount != 1 {
		t.Fatalf("truncated %v rows %#v", result.Truncated, result.Rows)
	}
	if result.Rows[0]["vehicleid"] != "bgauss_in_1166" {
		t.Fatalf("top vehicle %#v", result.Rows[0])
	}
}

func TestTimeRangeAndPeriodComparison(t *testing.T) {
	engine := openTrips(t)
	filtered, err := engine.Analyze(context.Background(), AnalyzeInput{
		Metrics:    []MetricInput{{Column: "socconsumed", Aggregation: "sum"}},
		TimeColumn: "starttime",
		TimeStart:  "2024-01-01T00:00:00Z",
		TimeEnd:    "2024-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	got, _ := asFloat(filtered.Rows[0]["sum_socconsumed"])
	if got == nil || math.Abs(*got-320.5) > 1e-9 {
		t.Fatalf("january revenue %#v", filtered.Rows[0])
	}

	compared, err := engine.Compare(context.Background(), CompareInput{
		Column:        "socconsumed",
		Aggregation:   "sum",
		TimeColumn:    "starttime",
		CurrentStart:  "2024-02-01T00:00:00Z",
		CurrentEnd:    "2024-03-01T00:00:00Z",
		PreviousStart: "2024-01-01T00:00:00Z",
		PreviousEnd:   "2024-02-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	row := compared.Rows[0]
	if row.Current == nil || math.Abs(*row.Current-150) > 1e-9 {
		t.Fatalf("current %#v", row)
	}
	if row.Previous == nil || math.Abs(*row.Previous-320.5) > 1e-9 {
		t.Fatalf("previous %#v", row)
	}
	want := ((150 - 320.5) / 320.5) * 100
	if row.PercentageChange == nil || math.Abs(*row.PercentageChange-want) > 1e-9 {
		t.Fatalf("pct %#v", row)
	}

	zero, err := engine.Compare(context.Background(), CompareInput{
		Column:        "socconsumed",
		Aggregation:   "sum",
		TimeColumn:    "starttime",
		CurrentStart:  "2024-03-01T00:00:00Z",
		CurrentEnd:    "2024-04-01T00:00:00Z",
		PreviousStart: "2024-04-01T00:00:00Z",
		PreviousEnd:   "2024-05-01T00:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	if zero.Rows[0].PercentageChange != nil || zero.Rows[0].PercentageStatus != "percentage_change_undefined_zero_previous" {
		t.Fatalf("zero previous %#v", zero.Rows[0])
	}
}

func TestStatisticsOutliersCorrelationAndDistribution(t *testing.T) {
	engine := openTrips(t)
	result, err := engine.StatisticalAnalysis(context.Background(), StatsInput{
		Columns:     []string{"distance", "rangevalue"},
		Statistics:  []string{"median", "stddev", "correlation", "outliers", "distribution"},
		Percentiles: []float64{0.5},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Columns[0].Median == nil || math.Abs(*result.Columns[0].Median-12.5) > 1e-9 {
		t.Fatalf("median %#v", result.Columns[0])
	}
	if result.Columns[0].StdDev == nil {
		t.Fatal("missing stddev")
	}
	if result.Columns[0].Outliers == nil || result.Columns[0].Outliers.OutlierCount == nil || *result.Columns[0].Outliers.OutlierCount != 1 {
		t.Fatalf("outliers %#v", result.Columns[0].Outliers)
	}
	if result.Columns[0].Distribution == nil || result.Columns[0].Distribution.Status == "insufficient_sample" {
		t.Fatalf("distribution %#v", result.Columns[0].Distribution)
	}
	if len(result.Correlations) != 1 || result.Correlations[0].Value == nil {
		t.Fatalf("correlation %#v", result.Correlations)
	}
}

func TestInvalidRequestsAndLockdown(t *testing.T) {
	engine := openTrips(t)
	if _, err := engine.Analyze(context.Background(), AnalyzeInput{
		Metrics: []MetricInput{{Column: "nope", Aggregation: "sum"}},
	}); err == nil {
		t.Fatal("expected unknown column")
	}
	if _, err := engine.Analyze(context.Background(), AnalyzeInput{
		Metrics: []MetricInput{{Aggregation: "count"}},
		Filters: []FilterInput{{Column: "vehicleid", Operator: "like", Value: "%"}},
	}); err == nil {
		t.Fatal("expected bad operator")
	}
	if _, err := engine.Analyze(context.Background(), AnalyzeInput{
		Dataset: "other",
		Metrics: []MetricInput{{Aggregation: "count"}},
	}); err == nil {
		t.Fatal("expected unknown dataset")
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := engine.db.QueryContext(ctx, "SELECT * FROM read_csv('/etc/passwd')"); err == nil {
		t.Fatal("expected filesystem access to be denied")
	}
}

func TestQueryCancellation(t *testing.T) {
	engine := openTrips(t)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()
	_, err := engine.db.QueryContext(ctx, "SELECT count(*) FROM range(1000000000) a(i), range(1000000000) b(j) WHERE i = j AND i % 2 = 0")
	if err == nil {
		t.Fatal("expected cancellation")
	}
}

func TestInvalidParquet(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "bad.parquet")
	if err := os.WriteFile(path, []byte("not parquet"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Transport: "stdio", ParquetPath: path, DatasetName: "trips",
		DuckDBMemoryLimit: "256MB", TempDir: filepath.Join(dir, "tmp"),
		QueryTimeout: time.Second, MaxConcurrentQueries: 1, MaxResultRows: 10, MaxGroups: 10,
		MaxHistogramBins: 5, LogLevel: "error", DefaultTimezone: "UTC", MaxRequestBytes: 1024,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected invalid parquet error")
	}
}

func TestMissingParquet(t *testing.T) {
	dir := t.TempDir()
	cfg := config.Config{
		Transport: "stdio", ParquetPath: filepath.Join(dir, "missing.parquet"),
		DatasetName: "trips", DuckDBMemoryLimit: "256MB", TempDir: filepath.Join(dir, "tmp"),
		QueryTimeout: time.Second, MaxConcurrentQueries: 1, MaxResultRows: 10, MaxGroups: 10,
		MaxHistogramBins: 5, LogLevel: "error", DefaultTimezone: "UTC", MaxRequestBytes: 1024,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	if _, err := Open(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected missing parquet error")
	}
}

const miniFleetCSV = `fleetid,vehicleid,regno,chassisno,starttime,endtime,startodo,endodo,startsoc,endsoc,socconsumed,distance,duration,rangevalue,createdat
1,bgauss_in_1166,KA01AR9752,00123,2024-01-15T10:00:00,2024-01-15T10:20:00,10,22.5,80,70,100,12.5,1200000,40,2024-01-15T11:00:00
1,bgauss_in_1166,KA01AR9752,00123,2024-01-20T11:00:00,2024-01-20T11:20:00,22.5,32.5,70,60,80.5,10,1200000,35,2024-01-20T12:00:00
1,bgauss_in_00456,KA01AR0001,00456,2024-02-01T09:00:00,2024-02-01T09:40:00,100,120,90,70,150,20,2400000,50,2024-02-01T10:00:00
1,bgauss_in_00456,KA01AR0001,00456,2024-02-10T09:30:00,2024-02-10T09:50:00,120,120,55,55,,,600000,55.0,2024-02-10T10:00:00
1,bgauss_in_00789,MH03EZ0579,00789,2024-01-18T08:00:00,2024-01-18T08:15:00,50,55.5,60,50,40,5.5,900000,30,2024-01-18T09:00:00
1,bgauss_in_00789,MH03EZ0579,00789,2024-03-01T12:00:00,2024-03-01T12:10:00,55.5,155.5,40,20,10,100,600000,120,2024-03-01T13:00:00
1,bgauss_in_1166,KA01AR9752,00123,2024-01-15T10:00:00,2024-01-15T10:20:00,10,22.5,80,70,100,12.5,1200000,40,2024-01-15T11:00:00
1,bgauss_in_00999,DL3EV0001,00999,2024-04-01T00:00:00,2024-04-01T00:10:00,1,1,10,10,0,0,600000,0,2024-04-01T01:00:00
`
