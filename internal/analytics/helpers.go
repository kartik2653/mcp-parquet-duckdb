package analytics

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"

	"github.com/Intellicars/intellicarai/internal/dataset"
	"github.com/Intellicars/intellicarai/internal/security"
)

func invalid(msg string) error {
	return &security.SafeError{Public: msg}
}

func parseClock(value string, loc *time.Location) (time.Time, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}, invalid("a time value is required")
	}
	if ts, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return ts, nil
	}
	if ts, err := time.Parse(time.RFC3339, value); err == nil {
		return ts, nil
	}
	for _, layout := range []string{"2006-01-02 15:04:05", "2006-01-02T15:04:05", "2006-01-02"} {
		if ts, err := time.ParseInLocation(layout, value, loc); err == nil {
			return ts, nil
		}
	}
	return time.Time{}, invalid("time values must be RFC3339 or YYYY-MM-DD")
}

func wallTime(ts time.Time, loc *time.Location) string {
	return ts.In(loc).Format("2006-01-02 15:04:05.999999999")
}

func parseFilterValue(col dataset.Column, raw string, loc *time.Location) (any, error) {
	raw = strings.TrimSpace(raw)
	switch col.Kind {
	case dataset.KindNumeric:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
			return nil, invalid("numeric filter values must be finite numbers")
		}
		return f, nil
	case dataset.KindBoolean:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return nil, invalid("boolean filter values must be true or false")
		}
		return b, nil
	case dataset.KindDate:
		ts, err := parseClock(raw, loc)
		if err != nil {
			return nil, err
		}
		return ts.In(loc).Format("2006-01-02"), nil
	case dataset.KindTime:
		ts, err := parseClock(raw, loc)
		if err != nil {
			return nil, err
		}
		return ts.In(loc).Format("15:04:05"), nil
	case dataset.KindTimestamp:
		ts, err := parseClock(raw, loc)
		if err != nil {
			return nil, err
		}
		return wallTime(ts, loc), nil
	case dataset.KindTimestampTZ:
		ts, err := parseClock(raw, loc)
		if err != nil {
			return nil, err
		}
		return ts.UTC(), nil
	default:
		return raw, nil
	}
}

func castPlaceholder(col dataset.Column) string {
	switch col.Kind {
	case dataset.KindDate:
		return "CAST(? AS DATE)"
	case dataset.KindTime:
		return "CAST(? AS TIME)"
	case dataset.KindTimestamp:
		return "CAST(? AS TIMESTAMP)"
	case dataset.KindTimestampTZ:
		return "CAST(? AS TIMESTAMPTZ)"
	default:
		return "?"
	}
}

func quote(name string) (string, error) {
	quoted, err := dataset.QuoteIdent(name)
	if err != nil {
		return "", invalid("invalid column")
	}
	return quoted, nil
}

func requireColumn(cat *dataset.Catalog, name string) (dataset.Column, error) {
	if !dataset.ValidIdent(name) {
		return dataset.Column{}, invalid("invalid column")
	}
	col, ok := cat.Column(name)
	if !ok {
		return dataset.Column{}, invalid("unknown column")
	}
	return col, nil
}

func aggregationAllowed(col dataset.Column, agg string) bool {
	for _, allowed := range col.ApprovedAggregations {
		if allowed == agg {
			return true
		}
	}
	return false
}

func checkDataset(cat *dataset.Catalog, name string) error {
	if name == "" {
		name = cat.Name
	}
	if name != cat.Name {
		return invalid("unknown dataset")
	}
	return nil
}

var aggregations = map[string]bool{
	"count": true, "count_distinct": true, "sum": true, "avg": true, "min": true, "max": true,
}

var operators = map[string]bool{
	"eq": true, "neq": true, "gt": true, "gte": true, "lt": true, "lte": true,
	"in": true, "is_null": true, "is_not_null": true,
}

var buckets = map[string]bool{
	"hour": true, "day": true, "week": true, "month": true, "quarter": true, "year": true,
}

func resolveTimeColumn(cat *dataset.Catalog, requested string, needed bool) (dataset.Column, error) {
	if requested != "" {
		col, err := requireColumn(cat, requested)
		if err != nil {
			return dataset.Column{}, err
		}
		if !col.Temporal() {
			return dataset.Column{}, invalid("time column must be a date or time")
		}
		return col, nil
	}
	times := cat.TimeColumns()
	if len(times) == 1 {
		return times[0], nil
	}
	if !needed {
		return dataset.Column{}, nil
	}
	if len(times) == 0 {
		return dataset.Column{}, invalid("dataset has no date or time column")
	}
	return dataset.Column{}, invalid("time_column is required")
}

func comparableOrdered(kind dataset.Kind) bool {
	switch kind {
	case dataset.KindNumeric, dataset.KindDate, dataset.KindTime, dataset.KindTimestamp, dataset.KindTimestampTZ:
		return true
	default:
		return false
	}
}

func metricAlias(m Metric) (string, error) {
	if m.Alias != "" {
		if !dataset.ValidIdent(m.Alias) {
			return "", invalid("invalid alias")
		}
		return m.Alias, nil
	}
	if m.Column == "" {
		return "row_count", nil
	}
	alias := m.Aggregation + "_" + m.Column
	if m.WeightColumn != "" {
		alias = "weighted_avg_" + m.Column
	}
	if !dataset.ValidIdent(alias) {
		return "", invalid("invalid alias")
	}
	return alias, nil
}

func formatFloat(v float64) string {
	return strconv.FormatFloat(v, 'f', -1, 64)
}

func containsStat(stats []string, name string) bool {
	for _, s := range stats {
		if s == name {
			return true
		}
	}
	return false
}

func clampLimit(requested, maxRows, maxGroups int, grouped bool) (int, error) {
	limit := requested
	if limit <= 0 {
		limit = maxRows
	}
	if limit > maxRows {
		limit = maxRows
	}
	if grouped && limit > maxGroups {
		limit = maxGroups
	}
	if limit < 1 {
		return 0, invalid("result limit must be positive")
	}
	return limit, nil
}

func percentChange(current, previous *float64) (*float64, string) {
	if previous == nil {
		return nil, "percentage_change_undefined_missing_previous"
	}
	if *previous == 0 {
		return nil, "percentage_change_undefined_zero_previous"
	}
	if current == nil {
		return nil, "percentage_change_undefined_missing_current"
	}
	value := ((*current - *previous) / *previous) * 100
	return &value, "percentage_change_computed"
}

func asInt(v any) (int64, bool) {
	switch t := v.(type) {
	case int64:
		return t, true
	case int32:
		return int64(t), true
	case int:
		return int64(t), true
	case float64:
		return int64(t), true
	default:
		return 0, false
	}
}

func asFloat(v any) (*float64, bool) {
	if v == nil {
		return nil, false
	}
	switch t := v.(type) {
	case float64:
		return &t, true
	case float32:
		f := float64(t)
		return &f, true
	case int64:
		f := float64(t)
		return &f, true
	case int32:
		f := float64(t)
		return &f, true
	case int:
		f := float64(t)
		return &f, true
	case string:
		f, err := strconv.ParseFloat(t, 64)
		if err != nil {
			return nil, false
		}
		return &f, true
	default:
		f, err := strconv.ParseFloat(fmt.Sprint(t), 64)
		if err != nil {
			return nil, false
		}
		return &f, true
	}
}
