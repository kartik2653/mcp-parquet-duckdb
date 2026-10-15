package analytics

import (
	"context"
	"time"

	"github.com/Intellicars/intellicarai/internal/dataset"
)

// FilterInput is a tool-facing filter. Values stay strings until column types parse them.
type FilterInput struct {
	Column   string   `json:"column"`
	Operator string   `json:"operator"`
	Value    string   `json:"value,omitempty"`
	Values   []string `json:"values,omitempty"`
}

// MetricInput is a tool-facing aggregate.
type MetricInput struct {
	Column       string `json:"column,omitempty"`
	Aggregation  string `json:"aggregation"`
	Alias        string `json:"alias,omitempty"`
	WeightColumn string `json:"weight_column,omitempty"`
}

// SortInput is a tool-facing sort.
type SortInput struct {
	Field     string `json:"field"`
	Direction string `json:"direction,omitempty"`
}

// AnalyzeInput is the analyze tool payload.
type AnalyzeInput struct {
	Dataset     string        `json:"dataset,omitempty"`
	Metrics     []MetricInput `json:"metrics"`
	Filters     []FilterInput `json:"filters,omitempty"`
	GroupBy     []string      `json:"group_by,omitempty"`
	TimeColumn  string        `json:"time_column,omitempty"`
	TimeStart   string        `json:"time_start,omitempty"`
	TimeEnd     string        `json:"time_end,omitempty"`
	TimeBucket  string        `json:"time_bucket,omitempty"`
	Sort        []SortInput   `json:"sort,omitempty"`
	Limit       int           `json:"limit,omitempty"`
	IncludeRank bool          `json:"include_rank,omitempty"`
}

// AnalyzeResult is a compact aggregate result.
type AnalyzeResult struct {
	Dataset     string            `json:"dataset"`
	Rows        []map[string]any  `json:"rows"`
	RowCount    int               `json:"row_count"`
	Truncated   bool              `json:"truncated"`
	Assumptions []string          `json:"assumptions"`
	Units       map[string]string `json:"units,omitempty"`
}

// CompareInput is the compare_periods tool payload.
type CompareInput struct {
	Dataset       string        `json:"dataset,omitempty"`
	Column        string        `json:"column,omitempty"`
	Aggregation   string        `json:"aggregation"`
	WeightColumn  string        `json:"weight_column,omitempty"`
	Filters       []FilterInput `json:"filters,omitempty"`
	GroupBy       []string      `json:"group_by,omitempty"`
	TimeColumn    string        `json:"time_column,omitempty"`
	CurrentStart  string        `json:"current_start"`
	CurrentEnd    string        `json:"current_end"`
	PreviousStart string        `json:"previous_start"`
	PreviousEnd   string        `json:"previous_end"`
	Sort          []SortInput   `json:"sort,omitempty"`
	Limit         int           `json:"limit,omitempty"`
}

// CompareRow is one period comparison.
type CompareRow struct {
	Group              map[string]any `json:"group,omitempty"`
	Current            *float64       `json:"current"`
	Previous           *float64       `json:"previous"`
	AbsoluteDifference *float64       `json:"absolute_difference"`
	PercentageChange   *float64       `json:"percentage_change"`
	PercentageStatus   string         `json:"percentage_status"`
	CurrentSampleSize  int64          `json:"current_sample_size"`
	PreviousSampleSize int64          `json:"previous_sample_size"`
	Unit               string         `json:"unit,omitempty"`
}

// CompareResult is the compare_periods payload.
type CompareResult struct {
	Dataset     string       `json:"dataset"`
	Rows        []CompareRow `json:"rows"`
	Truncated   bool         `json:"truncated"`
	Method      string       `json:"method"`
	Assumptions []string     `json:"assumptions"`
	Unit        string       `json:"unit,omitempty"`
}

// StatsInput is the statistical_analysis tool payload.
type StatsInput struct {
	Dataset       string        `json:"dataset,omitempty"`
	Columns       []string      `json:"columns"`
	Statistics    []string      `json:"statistics"`
	Percentiles   []float64     `json:"percentiles,omitempty"`
	Correlation   []string      `json:"correlation_columns,omitempty"`
	Filters       []FilterInput `json:"filters,omitempty"`
	GroupBy       []string      `json:"group_by,omitempty"`
	TimeColumn    string        `json:"time_column,omitempty"`
	TimeStart     string        `json:"time_start,omitempty"`
	TimeEnd       string        `json:"time_end,omitempty"`
	HistogramBins int           `json:"histogram_bins,omitempty"`
}

// StatisticalAnalysis parses tool input and runs Statistics.
func (e *Engine) StatisticalAnalysis(ctx context.Context, in StatsInput) (StatsResult, error) {
	filters, err := e.parseFilters(in.Filters)
	if err != nil {
		return StatsResult{}, err
	}
	start, end, err := e.parseRange(in.TimeStart, in.TimeEnd)
	if err != nil {
		return StatsResult{}, err
	}
	return e.Statistics(ctx, StatsRequest{
		Dataset: in.Dataset, Columns: in.Columns, Statistics: in.Statistics,
		Percentiles: in.Percentiles, Correlation: in.Correlation, Filters: filters,
		GroupBy: in.GroupBy, TimeColumn: in.TimeColumn, TimeStart: start, TimeEnd: end,
		HistogramBins: in.HistogramBins,
	})
}

type DescribeInput struct {
	Columns []string `json:"columns,omitempty"`
	Search  string   `json:"search,omitempty"`
}

// DescribeColumn is catalog metadata for one column.
type DescribeColumn struct {
	Name         string   `json:"name"`
	BusinessName string   `json:"business_name"`
	Type         string   `json:"type"`
	Kind         string   `json:"kind"`
	Nullable     bool     `json:"nullable"`
	Unit         string   `json:"unit,omitempty"`
	Description  string   `json:"description,omitempty"`
	Aggregations []string `json:"aggregations"`
	Role         string   `json:"role"`
}

// DescribeResult describes the dataset without returning rows.
type DescribeResult struct {
	Dataset     string           `json:"dataset"`
	Description string           `json:"description,omitempty"`
	Columns     []DescribeColumn `json:"columns"`
}

// Analyze runs a validated aggregate query.
func (e *Engine) Analyze(ctx context.Context, in AnalyzeInput) (AnalyzeResult, error) {
	req, err := e.analysisRequest(in)
	if err != nil {
		return AnalyzeResult{}, err
	}
	query, limit, assumptions, err := BuildAnalyze(e.catalog, req, e.cfg.MaxResultRows, e.cfg.MaxGroups, e.loc)
	if err != nil {
		return AnalyzeResult{}, err
	}
	rows, err := e.query(ctx, query)
	if err != nil {
		return AnalyzeResult{}, err
	}
	rows, truncated := trimLimit(rows, limit)
	return AnalyzeResult{
		Dataset:     e.catalog.Name,
		Rows:        rows,
		RowCount:    len(rows),
		Truncated:   truncated,
		Assumptions: assumptions,
		Units:       metricUnits(e.catalog, req.Metrics),
	}, nil
}

// Compare runs a two-period comparison.
func (e *Engine) Compare(ctx context.Context, in CompareInput) (CompareResult, error) {
	req, unit, err := e.compareRequest(in)
	if err != nil {
		return CompareResult{}, err
	}
	query, limit, err := BuildCompare(e.catalog, req, e.cfg.MaxResultRows, e.cfg.MaxGroups, e.loc)
	if err != nil {
		return CompareResult{}, err
	}
	rows, err := e.query(ctx, query)
	if err != nil {
		return CompareResult{}, err
	}
	rows, truncated := trimLimit(rows, limit)
	out := make([]CompareRow, 0, len(rows))
	for _, row := range rows {
		current, _ := asFloat(row["current_value"])
		previous, _ := asFloat(row["previous_value"])
		pct, status := percentChange(current, previous)
		var diff *float64
		if current != nil && previous != nil {
			value := *current - *previous
			diff = &value
		}
		currentN, _ := asInt(row["current_n"])
		previousN, _ := asInt(row["previous_n"])
		group := map[string]any{}
		for _, name := range req.GroupBy {
			group[name] = row[name]
		}
		item := CompareRow{
			Current: current, Previous: previous, AbsoluteDifference: diff,
			PercentageChange: pct, PercentageStatus: status,
			CurrentSampleSize: currentN, PreviousSampleSize: previousN, Unit: unit,
		}
		if len(group) > 0 {
			item.Group = group
		}
		out = append(out, item)
	}
	return CompareResult{
		Dataset: e.catalog.Name, Rows: out, Truncated: truncated, Unit: unit,
		Method: "((current - previous) / previous) * 100 when previous is nonzero",
		Assumptions: []string{
			"Periods are half-open: start inclusive, end exclusive.",
			"Naive timestamps are wall-clock values in the configured timezone.",
			"Percentage change is null when the previous value is zero or missing.",
			"Duplicate rows are included.",
		},
	}, nil
}

// Describe returns catalog metadata.
func (e *Engine) Describe(in DescribeInput) (DescribeResult, error) {
	cols, err := e.catalog.Select(in.Columns, in.Search)
	if err != nil {
		return DescribeResult{}, invalid(err.Error())
	}
	out := make([]DescribeColumn, 0, len(cols))
	for _, col := range cols {
		role := "dimension"
		if col.Kind == "numeric" {
			role = "metric"
		}
		if col.Temporal() {
			role = "time"
		}
		out = append(out, DescribeColumn{
			Name: col.Name, BusinessName: col.BusinessName, Type: col.DuckType,
			Kind: string(col.Kind), Nullable: col.Nullable, Unit: col.Unit,
			Description: col.Description, Aggregations: col.ApprovedAggregations, Role: role,
		})
	}
	return DescribeResult{Dataset: e.catalog.Name, Description: e.catalog.Description, Columns: out}, nil
}

func (e *Engine) analysisRequest(in AnalyzeInput) (AnalysisRequest, error) {
	filters, err := e.parseFilters(in.Filters)
	if err != nil {
		return AnalysisRequest{}, err
	}
	start, end, err := e.parseRange(in.TimeStart, in.TimeEnd)
	if err != nil {
		return AnalysisRequest{}, err
	}
	metrics := make([]Metric, len(in.Metrics))
	for i, metric := range in.Metrics {
		metrics[i] = Metric{Column: metric.Column, Aggregation: metric.Aggregation, Alias: metric.Alias, WeightColumn: metric.WeightColumn}
	}
	return AnalysisRequest{
		Dataset: in.Dataset, Metrics: metrics, Filters: filters, GroupBy: in.GroupBy,
		TimeColumn: in.TimeColumn, TimeStart: start, TimeEnd: end, TimeBucket: in.TimeBucket,
		Sort: parseSorts(in.Sort), Limit: in.Limit, IncludeRank: in.IncludeRank,
	}, nil
}

func (e *Engine) compareRequest(in CompareInput) (CompareRequest, string, error) {
	filters, err := e.parseFilters(in.Filters)
	if err != nil {
		return CompareRequest{}, "", err
	}
	currentStart, err := parseClock(in.CurrentStart, e.loc)
	if err != nil {
		return CompareRequest{}, "", err
	}
	currentEnd, err := parseClock(in.CurrentEnd, e.loc)
	if err != nil {
		return CompareRequest{}, "", err
	}
	previousStart, err := parseClock(in.PreviousStart, e.loc)
	if err != nil {
		return CompareRequest{}, "", err
	}
	previousEnd, err := parseClock(in.PreviousEnd, e.loc)
	if err != nil {
		return CompareRequest{}, "", err
	}
	unit := ""
	if in.Column != "" {
		if col, ok := e.catalog.Column(in.Column); ok {
			unit = col.Unit
		}
	}
	return CompareRequest{
		Dataset: in.Dataset,
		Metric:  Metric{Column: in.Column, Aggregation: in.Aggregation, WeightColumn: in.WeightColumn},
		Filters: filters, GroupBy: in.GroupBy, TimeColumn: in.TimeColumn,
		Current:  Period{Start: currentStart, End: currentEnd},
		Previous: Period{Start: previousStart, End: previousEnd},
		Sort:     parseSorts(in.Sort), Limit: in.Limit,
	}, unit, nil
}

func (e *Engine) parseFilters(inputs []FilterInput) ([]Filter, error) {
	out := make([]Filter, 0, len(inputs))
	for _, in := range inputs {
		col, err := requireColumn(e.catalog, in.Column)
		if err != nil {
			return nil, err
		}
		filter := Filter{Column: in.Column, Operator: in.Operator}
		switch in.Operator {
		case "is_null", "is_not_null":
		case "in":
			if len(in.Values) == 0 {
				return nil, invalid("in filters need values")
			}
			for _, raw := range in.Values {
				value, err := parseFilterValue(col, raw, e.loc)
				if err != nil {
					return nil, err
				}
				filter.Values = append(filter.Values, value)
			}
		default:
			value, err := parseFilterValue(col, in.Value, e.loc)
			if err != nil {
				return nil, err
			}
			filter.Value = value
		}
		out = append(out, filter)
	}
	return out, nil
}

func (e *Engine) parseRange(start, end string) (*time.Time, *time.Time, error) {
	if start == "" && end == "" {
		return nil, nil, nil
	}
	if start == "" || end == "" {
		return nil, nil, invalid("time range needs both start and end")
	}
	s, err := parseClock(start, e.loc)
	if err != nil {
		return nil, nil, err
	}
	en, err := parseClock(end, e.loc)
	if err != nil {
		return nil, nil, err
	}
	return &s, &en, nil
}

func parseSorts(in []SortInput) []Sort {
	out := make([]Sort, len(in))
	for i, sort := range in {
		out[i] = Sort{Field: sort.Field, Descending: sort.Direction == "desc"}
	}
	return out
}

func metricUnits(cat *dataset.Catalog, metrics []Metric) map[string]string {
	units := map[string]string{}
	for _, metric := range metrics {
		alias, err := metricAlias(metric)
		if err != nil || metric.Column == "" {
			continue
		}
		col, ok := cat.Column(metric.Column)
		if ok && col.Unit != "" {
			units[alias] = col.Unit
		}
	}
	if len(units) == 0 {
		return nil
	}
	return units
}
