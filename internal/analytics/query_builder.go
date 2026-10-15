package analytics

import (
	"fmt"
	"strings"
	"time"

	"github.com/Intellicars/intellicarai/internal/dataset"
)

const viewName = "analytics_dataset"

type sqlBuilder struct {
	args []any
	loc  *time.Location
}

func (s *sqlBuilder) bind(v any) {
	s.args = append(s.args, v)
}

// BuildAnalyze constructs a parameterized aggregate query.
// The SQL uses LIMIT max+1 so the caller can detect truncation.
func BuildAnalyze(cat *dataset.Catalog, req AnalysisRequest, maxRows, maxGroups int, loc *time.Location) (Query, int, []string, error) {
	if err := checkDataset(cat, req.Dataset); err != nil {
		return Query{}, 0, nil, err
	}
	if len(req.Metrics) == 0 {
		return Query{}, 0, nil, invalid("at least one metric is required")
	}
	grouped := len(req.GroupBy) > 0 || req.TimeBucket != ""
	limit, err := clampLimit(req.Limit, maxRows, maxGroups, grouped)
	if err != nil {
		return Query{}, 0, nil, err
	}
	b := &sqlBuilder{loc: loc}
	var selectList []string
	var groupList []string
	aliases := map[string]bool{}
	assumptions := []string{
		"Aggregates ignore nulls.",
		"Duplicate rows are included.",
		"avg is the unweighted mean of raw rows unless weight_column is set.",
	}

	if req.TimeBucket != "" {
		if !buckets[req.TimeBucket] {
			return Query{}, 0, nil, invalid("unsupported time bucket")
		}
		col, err := resolveTimeColumn(cat, req.TimeColumn, true)
		if err != nil {
			return Query{}, 0, nil, err
		}
		quoted, err := quote(col.Name)
		if err != nil {
			return Query{}, 0, nil, err
		}
		b.bind(req.TimeBucket)
		selectList = append(selectList, "date_trunc(?, "+quoted+") AS \"time_bucket\"")
		groupList = append(groupList, `"time_bucket"`)
		aliases["time_bucket"] = true
		assumptions = append(assumptions, "Time buckets use the stored wall clock. Weeks start on Monday.")
	}

	where, err := b.where(cat, req.Filters, req.TimeColumn, req.TimeStart, req.TimeEnd)
	if err != nil {
		return Query{}, 0, nil, err
	}

	for _, name := range req.GroupBy {
		col, err := requireColumn(cat, name)
		if err != nil {
			return Query{}, 0, nil, err
		}
		quoted, err := quote(col.Name)
		if err != nil {
			return Query{}, 0, nil, err
		}
		selectList = append(selectList, quoted)
		groupList = append(groupList, quoted)
		aliases[col.Name] = true
	}

	for _, metric := range req.Metrics {
		expr, alias, err := b.metricExpr(cat, metric, "")
		if err != nil {
			return Query{}, 0, nil, err
		}
		if aliases[alias] {
			return Query{}, 0, nil, invalid("duplicate metric alias")
		}
		aliases[alias] = true
		selectList = append(selectList, expr+" AS "+mustQuote(alias))
		nExpr := b.sampleExpr(metric, "")
		nAlias := alias + "_n"
		selectList = append(selectList, nExpr+" AS "+mustQuote(nAlias))
		aliases[nAlias] = true
		if metric.WeightColumn != "" {
			assumptions = append(assumptions, "Weighted average is sum(metric * weight) / sum(weight) for rows where both values are present.")
		}
	}

	order, err := orderBy(req.Sort, aliases)
	if err != nil {
		return Query{}, 0, nil, err
	}
	if req.IncludeRank {
		if order == "" {
			return Query{}, 0, nil, invalid("include_rank requires sort")
		}
		selectList = append(selectList, "row_number() OVER (ORDER BY "+order+") AS \"rank\"")
		assumptions = append(assumptions, "rank is row_number over the requested sort, within the returned limit.")
	}

	sql := "SELECT " + strings.Join(selectList, ", ") + " FROM " + viewName
	if where != "" {
		sql += " WHERE " + where
	}
	if len(groupList) > 0 {
		sql += " GROUP BY " + strings.Join(groupList, ", ")
	}
	if order == "" && len(groupList) > 0 {
		order = strings.Join(groupList, ", ")
	}
	if order != "" {
		sql += " ORDER BY " + order
	}
	sql += fmt.Sprintf(" LIMIT %d", limit+1)
	return Query{SQL: sql, Args: b.args}, limit, assumptions, nil
}

// BuildCompare constructs one conditional aggregation over two half-open periods.
func BuildCompare(cat *dataset.Catalog, req CompareRequest, maxRows, maxGroups int, loc *time.Location) (Query, int, error) {
	if err := checkDataset(cat, req.Dataset); err != nil {
		return Query{}, 0, err
	}
	if !req.Current.End.After(req.Current.Start) || !req.Previous.End.After(req.Previous.Start) {
		return Query{}, 0, invalid("each period end must be after its start")
	}
	col, err := resolveTimeColumn(cat, req.TimeColumn, true)
	if err != nil {
		return Query{}, 0, err
	}
	limit, err := clampLimit(req.Limit, maxRows, maxGroups, len(req.GroupBy) > 0)
	if err != nil {
		return Query{}, 0, err
	}
	b := &sqlBuilder{loc: loc}
	currentPred, err := b.timePredicate(col, req.Current.Start, req.Current.End)
	if err != nil {
		return Query{}, 0, err
	}
	previousPred, err := b.timePredicate(col, req.Previous.Start, req.Previous.End)
	if err != nil {
		return Query{}, 0, err
	}
	where, err := b.where(cat, req.Filters, "", nil, nil)
	if err != nil {
		return Query{}, 0, err
	}
	var selectList []string
	var groupList []string
	aliases := map[string]bool{}
	for _, name := range req.GroupBy {
		group, err := requireColumn(cat, name)
		if err != nil {
			return Query{}, 0, err
		}
		quoted, err := quote(group.Name)
		if err != nil {
			return Query{}, 0, err
		}
		selectList = append(selectList, quoted)
		groupList = append(groupList, quoted)
		aliases[group.Name] = true
	}
	currentExpr, _, err := b.metricExpr(cat, req.Metric, currentPred)
	if err != nil {
		return Query{}, 0, err
	}
	previousExpr, _, err := b.metricExpr(cat, req.Metric, previousPred)
	if err != nil {
		return Query{}, 0, err
	}
	selectList = append(selectList,
		currentExpr+" AS \"current_value\"",
		b.sampleExpr(req.Metric, currentPred)+" AS \"current_n\"",
		previousExpr+" AS \"previous_value\"",
		b.sampleExpr(req.Metric, previousPred)+" AS \"previous_n\"",
	)
	aliases["current_value"] = true
	aliases["previous_value"] = true
	aliases["current_n"] = true
	aliases["previous_n"] = true
	order, err := orderBy(req.Sort, aliases)
	if err != nil {
		return Query{}, 0, err
	}
	if order == "" {
		order = "\"current_value\" DESC NULLS LAST"
	}
	sql := "SELECT " + strings.Join(selectList, ", ") + " FROM " + viewName
	if where != "" {
		sql += " WHERE " + where
	}
	if len(groupList) > 0 {
		sql += " GROUP BY " + strings.Join(groupList, ", ")
		order += ", " + strings.Join(groupList, ", ")
	}
	sql += " ORDER BY " + order + fmt.Sprintf(" LIMIT %d", limit+1)
	return Query{SQL: sql, Args: b.args}, limit, nil
}

func (s *sqlBuilder) metricExpr(cat *dataset.Catalog, metric Metric, pred string) (string, string, error) {
	agg := metric.Aggregation
	if !aggregations[agg] {
		return "", "", invalid("unsupported aggregation")
	}
	alias, err := metricAlias(metric)
	if err != nil {
		return "", "", err
	}
	if agg == "count" && metric.Column == "" {
		return filtered("count(*)", pred), alias, nil
	}
	if metric.Column == "" {
		return "", "", invalid("aggregation requires a column")
	}
	col, err := requireColumn(cat, metric.Column)
	if err != nil {
		return "", "", err
	}
	if !aggregationAllowed(col, agg) {
		return "", "", invalid("aggregation is not approved for this column")
	}
	quoted, err := quote(col.Name)
	if err != nil {
		return "", "", err
	}
	switch agg {
	case "count":
		return filtered("count("+quoted+")", pred), alias, nil
	case "count_distinct":
		return filtered("count(DISTINCT "+quoted+")", pred), alias, nil
	case "sum":
		if col.Kind != dataset.KindNumeric {
			return "", "", invalid("sum requires a numeric column")
		}
		return "CAST(" + filtered("sum("+quoted+")", pred) + " AS DOUBLE)", alias, nil
	case "min":
		return filtered("min("+quoted+")", pred), alias, nil
	case "max":
		return filtered("max("+quoted+")", pred), alias, nil
	case "avg":
		if col.Kind != dataset.KindNumeric {
			return "", "", invalid("avg requires a numeric column")
		}
		if metric.WeightColumn == "" {
			return "CAST(" + filtered("avg("+quoted+")", pred) + " AS DOUBLE)", alias, nil
		}
		weight, err := requireColumn(cat, metric.WeightColumn)
		if err != nil {
			return "", "", err
		}
		if weight.Kind != dataset.KindNumeric {
			return "", "", invalid("weight_column must be numeric")
		}
		wq, err := quote(weight.Name)
		if err != nil {
			return "", "", err
		}
		sumW := filtered("sum("+wq+")", pred)
		sumP := filtered("sum("+quoted+" * "+wq+")", pred)
		expr := "CAST(CASE WHEN " + sumW + " IS NULL OR " + sumW + " = 0 THEN NULL ELSE " + sumP + " / " + sumW + " END AS DOUBLE)"
		return expr, alias, nil
	default:
		return "", "", invalid("unsupported aggregation")
	}
}

func (s *sqlBuilder) sampleExpr(metric Metric, pred string) string {
	if metric.Aggregation == "count" && metric.Column == "" {
		return filtered("count(*)", pred)
	}
	quoted := mustQuote(metric.Column)
	if metric.WeightColumn != "" {
		cond := quoted + " IS NOT NULL AND " + mustQuote(metric.WeightColumn) + " IS NOT NULL"
		if pred != "" {
			cond = "(" + pred + ") AND " + cond
		}
		return "count(*) FILTER (WHERE " + cond + ")"
	}
	return filtered("count("+quoted+")", pred)
}

func filtered(expr, pred string) string {
	if pred == "" {
		return expr
	}
	return expr + " FILTER (WHERE " + pred + ")"
}

func (s *sqlBuilder) where(cat *dataset.Catalog, filters []Filter, timeColumn string, start, end *time.Time) (string, error) {
	var parts []string
	for _, filter := range filters {
		part, err := s.predicate(cat, filter)
		if err != nil {
			return "", err
		}
		parts = append(parts, part)
	}
	if start != nil || end != nil {
		if start == nil || end == nil {
			return "", invalid("time range needs both start and end")
		}
		if !end.After(*start) {
			return "", invalid("time range end must be after start")
		}
		col, err := resolveTimeColumn(cat, timeColumn, true)
		if err != nil {
			return "", err
		}
		pred, err := s.timePredicate(col, *start, *end)
		if err != nil {
			return "", err
		}
		parts = append(parts, pred)
	}
	return strings.Join(parts, " AND "), nil
}

func (s *sqlBuilder) timePredicate(col dataset.Column, start, end time.Time) (string, error) {
	quoted, err := quote(col.Name)
	if err != nil {
		return "", err
	}
	return quoted + " >= " + literalTime(col, start, s.loc) + " AND " + quoted + " < " + literalTime(col, end, s.loc), nil
}

func literalTime(col dataset.Column, ts time.Time, loc *time.Location) string {
	switch col.Kind {
	case dataset.KindDate:
		return "CAST(" + sqlQuote(ts.In(loc).Format("2006-01-02")) + " AS DATE)"
	case dataset.KindTime:
		return "CAST(" + sqlQuote(ts.In(loc).Format("15:04:05")) + " AS TIME)"
	case dataset.KindTimestamp:
		return "CAST(" + sqlQuote(wallTime(ts, loc)) + " AS TIMESTAMP)"
	default:
		return "CAST(" + sqlQuote(ts.UTC().Format(time.RFC3339Nano)) + " AS TIMESTAMPTZ)"
	}
}

func (s *sqlBuilder) predicate(cat *dataset.Catalog, filter Filter) (string, error) {
	if !operators[filter.Operator] {
		return "", invalid("unsupported filter operator")
	}
	col, err := requireColumn(cat, filter.Column)
	if err != nil {
		return "", err
	}
	quoted, err := quote(col.Name)
	if err != nil {
		return "", err
	}
	switch filter.Operator {
	case "is_null":
		return quoted + " IS NULL", nil
	case "is_not_null":
		return quoted + " IS NOT NULL", nil
	case "in":
		if len(filter.Values) == 0 || len(filter.Values) > 100 {
			return "", invalid("in filters need between 1 and 100 values")
		}
		holders := make([]string, len(filter.Values))
		for i, value := range filter.Values {
			holders[i] = castPlaceholder(col)
			s.bind(value)
		}
		return quoted + " IN (" + strings.Join(holders, ", ") + ")", nil
	case "gt", "gte", "lt", "lte":
		if !comparableOrdered(col.Kind) {
			return "", invalid("ordered comparison is not supported for this column")
		}
		op := map[string]string{"gt": ">", "gte": ">=", "lt": "<", "lte": "<="}[filter.Operator]
		s.bind(filter.Value)
		return quoted + " " + op + " " + castPlaceholder(col), nil
	case "eq":
		s.bind(filter.Value)
		return quoted + " = " + castPlaceholder(col), nil
	case "neq":
		s.bind(filter.Value)
		return quoted + " <> " + castPlaceholder(col), nil
	default:
		return "", invalid("unsupported filter operator")
	}
}

func orderBy(sorts []Sort, aliases map[string]bool) (string, error) {
	if len(sorts) == 0 {
		return "", nil
	}
	parts := make([]string, 0, len(sorts))
	for _, sort := range sorts {
		if !dataset.ValidIdent(sort.Field) || !aliases[sort.Field] {
			return "", invalid("sort field must be a selected alias or grouping column")
		}
		quoted, err := quote(sort.Field)
		if err != nil {
			return "", err
		}
		dir := "ASC"
		if sort.Descending {
			dir = "DESC"
		}
		parts = append(parts, quoted+" "+dir+" NULLS LAST")
	}
	return strings.Join(parts, ", "), nil
}

func mustQuote(name string) string {
	quoted, err := quote(name)
	if err != nil {
		return ""
	}
	return quoted
}
