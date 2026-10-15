package analytics

import (
	"strings"
	"testing"
	"time"

	"github.com/Intellicars/intellicarai/internal/dataset"
)

func testCatalog(t *testing.T) *dataset.Catalog {
	t.Helper()
	cat, err := dataset.NewCatalog("trips", "fleet", []dataset.Column{
		{Name: "vehicleid", DuckType: "VARCHAR", Kind: dataset.KindCategorical, ApprovedAggregations: []string{"count", "count_distinct"}},
		{Name: "regno", DuckType: "VARCHAR", Kind: dataset.KindCategorical, ApprovedAggregations: []string{"count", "count_distinct"}},
		{Name: "starttime", DuckType: "TIMESTAMP", Kind: dataset.KindTimestamp, ApprovedAggregations: []string{"count", "min", "max"}},
		{Name: "distance", DuckType: "DOUBLE", Kind: dataset.KindNumeric, Unit: "km", ApprovedAggregations: []string{"count", "sum", "avg", "min", "max"}},
	}, dataset.Metadata{})
	if err != nil {
		t.Fatal(err)
	}
	return cat
}

func TestQueryBuilderRejectsInjection(t *testing.T) {
	cat := testCatalog(t)
	_, _, _, err := BuildAnalyze(cat, AnalysisRequest{
		Metrics: []Metric{{Aggregation: "count"}},
		GroupBy: []string{"regno; DROP TABLE analytics_dataset"},
	}, 10, 10, time.UTC)
	if err == nil || !strings.Contains(err.Error(), "invalid column") {
		t.Fatalf("error %v", err)
	}
}

func TestQueryBuilderUsesParameters(t *testing.T) {
	cat := testCatalog(t)
	start := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC)
	query, limit, _, err := BuildAnalyze(cat, AnalysisRequest{
		Metrics:    []Metric{{Column: "distance", Aggregation: "avg"}},
		Filters:    []Filter{{Column: "regno", Operator: "eq", Value: "KA01' OR 1=1"}},
		TimeColumn: "starttime",
		TimeStart:  &start,
		TimeEnd:    &end,
		Limit:      5,
	}, 10, 10, time.UTC)
	if err != nil {
		t.Fatal(err)
	}
	if limit != 5 {
		t.Fatalf("limit %d", limit)
	}
	if strings.Contains(query.SQL, "OR 1=1") {
		t.Fatalf("value interpolated into SQL: %s", query.SQL)
	}
	if len(query.Args) != 1 || query.Args[0] != "KA01' OR 1=1" {
		t.Fatalf("args %#v sql %s", query.Args, query.SQL)
	}
}

func TestPercentChangeZeroPrevious(t *testing.T) {
	previous := 0.0
	current := 10.0
	value, status := percentChange(&current, &previous)
	if value != nil || status != "percentage_change_undefined_zero_previous" {
		t.Fatalf("value %v status %s", value, status)
	}
	previous = 5
	value, status = percentChange(&current, &previous)
	if value == nil || *value != 100 || status != "percentage_change_computed" {
		t.Fatalf("value %v status %s", value, status)
	}
}
