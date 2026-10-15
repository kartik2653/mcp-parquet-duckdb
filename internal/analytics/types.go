package analytics

import "time"

// Metric is one aggregate in an analysis request.
type Metric struct {
	Column       string
	Aggregation  string
	Alias        string
	WeightColumn string
}

// Filter is one allowlisted predicate. Values are parsed Go values, never raw SQL.
type Filter struct {
	Column   string
	Operator string
	Value    any
	Values   []any
}

// Sort orders by a selected alias or grouping column.
type Sort struct {
	Field      string
	Descending bool
}

// AnalysisRequest is the validated shape of an analytical query.
type AnalysisRequest struct {
	Dataset     string
	Metrics     []Metric
	Filters     []Filter
	GroupBy     []string
	TimeColumn  string
	TimeStart   *time.Time
	TimeEnd     *time.Time
	TimeBucket  string
	Sort        []Sort
	Limit       int
	IncludeRank bool
}

// Period is a half-open time range [Start, End).
type Period struct {
	Start time.Time
	End   time.Time
}

// CompareRequest compares one metric across two periods.
type CompareRequest struct {
	Dataset    string
	Metric     Metric
	Filters    []Filter
	GroupBy    []string
	TimeColumn string
	Current    Period
	Previous   Period
	Sort       []Sort
	Limit      int
}

// StatsRequest asks for allowlisted statistics on numeric columns.
type StatsRequest struct {
	Dataset       string
	Columns       []string
	Statistics    []string
	Percentiles   []float64
	Correlation   []string
	Filters       []Filter
	GroupBy       []string
	TimeColumn    string
	TimeStart     *time.Time
	TimeEnd       *time.Time
	HistogramBins int
}

// Query is a parameterized statement.
type Query struct {
	SQL  string
	Args []any
}
