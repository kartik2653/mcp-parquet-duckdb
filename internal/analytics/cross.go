package analytics

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/Intellicars/intellicarai/internal/config"
	"github.com/Intellicars/intellicarai/internal/dataset"
)

// CrossSource is one dataset in a cross-dataset query.
type CrossSource struct {
	Dataset    string
	Alias      string
	Metrics    []Metric
	GroupBy    []string
	Filters    []Filter
	TimeColumn string
	TimeStart  *time.Time
	TimeEnd    *time.Time
}

// CrossJoin attaches one source using a registered relationship.
type CrossJoin struct {
	Left         string
	Right        string
	Type         string
	Relationship string
}

// CrossRequest is a structured join of aggregated datasets.
type CrossRequest struct {
	Sources []CrossSource
	Joins   []CrossJoin
	Limit   int
}

// CrossResult is a compact joined aggregate.
type CrossResult struct {
	Rows        []map[string]any `json:"rows"`
	RowCount    int              `json:"row_count"`
	Truncated   bool             `json:"truncated"`
	Assumptions []string         `json:"assumptions"`
	Warnings    []string         `json:"warnings,omitempty"`
	Grain       string           `json:"grain"`
}

type preparedSource struct {
	src      CrossSource
	cat      *dataset.Catalog
	sql      string
	args     []any
	keys     []string
	warnings []string
}

// BuildCross compiles a join that follows registered relationships.
// Sources that can duplicate a join key must be aggregated to those keys first.
func BuildCross(catalogs map[string]*dataset.Catalog, rels []config.Relationship, req CrossRequest, maxRows int, loc *time.Location) (Query, int, []string, []string, error) {
	if len(req.Sources) < 2 {
		return Query{}, 0, nil, nil, invalid("a cross-dataset query needs at least two sources")
	}
	if len(req.Joins) != len(req.Sources)-1 {
		return Query{}, 0, nil, nil, invalid("joins must connect every source without a cartesian product")
	}
	byAlias := map[string]CrossSource{}
	for _, src := range req.Sources {
		if !dataset.ValidIdent(src.Alias) {
			return Query{}, 0, nil, nil, invalid("invalid source alias")
		}
		if _, ok := byAlias[src.Alias]; ok {
			return Query{}, 0, nil, nil, invalid("duplicate source alias")
		}
		if _, ok := catalogs[src.Dataset]; !ok {
			return Query{}, 0, nil, nil, invalid("unknown dataset")
		}
		byAlias[src.Alias] = src
	}
	limit, err := clampLimit(req.Limit, maxRows, maxRows, true)
	if err != nil {
		return Query{}, 0, nil, nil, err
	}
	connected := map[string]bool{req.Sources[0].Alias: true}
	prepared := map[string]preparedSource{}
	var warnings []string
	assumptions := []string{
		"Null join keys do not match.",
		"Joins use only configured relationships.",
		"Datasets that can duplicate a join key are aggregated to that key before the join.",
	}
	root, err := prepareSource(catalogs[req.Sources[0].Dataset], req.Sources[0], nil, loc)
	if err != nil {
		return Query{}, 0, nil, nil, err
	}
	prepared[root.src.Alias] = root
	warnings = append(warnings, root.warnings...)
	from := "(" + root.sql + ") AS " + mustQuote(root.src.Alias)
	args := append([]any{}, root.args...)
	for _, join := range req.Joins {
		if !connected[join.Left] || connected[join.Right] {
			return Query{}, 0, nil, nil, invalid("joins must extend one connected source at a time")
		}
		if join.Type != "inner" && join.Type != "left" {
			return Query{}, 0, nil, nil, invalid("join type must be inner or left")
		}
		rightSrc, ok := byAlias[join.Right]
		if !ok {
			return Query{}, 0, nil, nil, invalid("join references an unknown source")
		}
		leftSrc := byAlias[join.Left]
		rel, flipped, err := findRelationship(rels, join.Relationship, leftSrc.Dataset, rightSrc.Dataset)
		if err != nil {
			return Query{}, 0, nil, nil, err
		}
		leftKeys, rightKeys := orientedKeys(rel, flipped)
		if err := requireGrain(rel.Cardinality, flipped, leftSrc, leftKeys, rightSrc, rightKeys); err != nil {
			return Query{}, 0, nil, nil, err
		}
		right, err := prepareSource(catalogs[rightSrc.Dataset], rightSrc, rightKeys, loc)
		if err != nil {
			return Query{}, 0, nil, nil, err
		}
		prepared[right.src.Alias] = right
		warnings = append(warnings, right.warnings...)
		args = append(args, right.args...)
		var ons []string
		for i := range leftKeys {
			ons = append(ons, mustQuote(join.Left)+"."+mustQuote(leftKeys[i])+" = "+mustQuote(join.Right)+"."+mustQuote(rightKeys[i]))
		}
		kind := "INNER JOIN"
		if join.Type == "left" {
			kind = "LEFT JOIN"
		}
		from += " " + kind + " (" + right.sql + ") AS " + mustQuote(join.Right) + " ON " + strings.Join(ons, " AND ")
		connected[join.Right] = true
		assumptions = append(assumptions, "Join "+join.Relationship+" uses cardinality "+rel.Cardinality+".")
	}
	var selectList []string
	for _, src := range req.Sources {
		item := prepared[src.Alias]
		for _, key := range item.keys {
			selectList = append(selectList, mustQuote(src.Alias)+"."+mustQuote(key)+" AS "+mustQuote(src.Alias+"_"+key))
		}
		for _, metric := range src.Metrics {
			alias, err := metricAlias(metric)
			if err != nil {
				return Query{}, 0, nil, nil, err
			}
			selectList = append(selectList, mustQuote(src.Alias)+"."+mustQuote(alias)+" AS "+mustQuote(src.Alias+"_"+alias))
		}
	}
	if len(selectList) == 0 {
		return Query{}, 0, nil, nil, invalid("the query does not select any aggregate")
	}
	sql := "SELECT " + strings.Join(selectList, ", ") + " FROM " + from + fmt.Sprintf(" LIMIT %d", limit+1)
	warnings = uniqueStrings(warnings)
	return Query{SQL: sql, Args: args}, limit, assumptions, warnings, nil
}

// CrossSourceInput is the tool payload for one source.
type CrossSourceInput struct {
	Dataset    string        `json:"dataset"`
	Alias      string        `json:"alias"`
	Metrics    []MetricInput `json:"metrics"`
	GroupBy    []string      `json:"group_by"`
	Filters    []FilterInput `json:"filters,omitempty"`
	TimeColumn string        `json:"time_column,omitempty"`
	TimeStart  string        `json:"time_start,omitempty"`
	TimeEnd    string        `json:"time_end,omitempty"`
}

// CrossJoinInput is the tool payload for one join.
type CrossJoinInput struct {
	Left         string `json:"left"`
	Right        string `json:"right"`
	Type         string `json:"type"`
	Relationship string `json:"relationship"`
}

// CrossInput is the cross_dataset_query tool payload.
type CrossInput struct {
	Sources []CrossSourceInput `json:"sources"`
	Joins   []CrossJoinInput   `json:"joins"`
	Limit   int                `json:"limit,omitempty"`
}

// CrossDataset validates and runs a cross-dataset aggregate.
func (e *Engine) CrossDataset(ctx context.Context, in CrossInput) (CrossResult, error) {
	req := CrossRequest{Limit: in.Limit}
	for _, src := range in.Sources {
		cat, err := e.dataset(src.Dataset)
		if err != nil {
			return CrossResult{}, err
		}
		filters, err := e.parseFilters(cat, src.Filters)
		if err != nil {
			return CrossResult{}, err
		}
		start, end, err := e.parseRange(src.TimeStart, src.TimeEnd)
		if err != nil {
			return CrossResult{}, err
		}
		metrics := make([]Metric, len(src.Metrics))
		for i, metric := range src.Metrics {
			metrics[i] = Metric{Column: metric.Column, Aggregation: metric.Aggregation, Alias: metric.Alias, WeightColumn: metric.WeightColumn}
		}
		req.Sources = append(req.Sources, CrossSource{
			Dataset: src.Dataset, Alias: src.Alias, Metrics: metrics, GroupBy: src.GroupBy,
			Filters: filters, TimeColumn: src.TimeColumn, TimeStart: start, TimeEnd: end,
		})
	}
	for _, join := range in.Joins {
		req.Joins = append(req.Joins, CrossJoin{Left: join.Left, Right: join.Right, Type: join.Type, Relationship: join.Relationship})
	}
	query, limit, assumptions, warnings, err := BuildCross(e.catalogs, e.relationships, req, e.cfg.MaxResultRows, e.loc)
	if err != nil {
		return CrossResult{}, err
	}
	rows, err := e.query(ctx, query)
	if err != nil {
		return CrossResult{}, err
	}
	rows, truncated := trimLimit(rows, limit)
	return CrossResult{
		Rows: rows, RowCount: len(rows), Truncated: truncated,
		Assumptions: assumptions, Warnings: warnings,
		Grain: "pre-aggregated to configured relationship keys",
	}, nil
}

// ListDatasets returns the registered datasets without file paths.
func (e *Engine) ListDatasets() []DatasetBrief {
	out := make([]DatasetBrief, 0, len(e.catalogs))
	for _, cat := range e.catalogList() {
		out = append(out, DatasetBrief{Name: cat.Name, Description: cat.Description, ColumnCount: len(cat.Columns())})
	}
	return out
}

// RelationshipInfo is a configured join exposed to clients.
type RelationshipInfo struct {
	Name        string    `json:"name"`
	Left        string    `json:"left"`
	Right       string    `json:"right"`
	Cardinality string    `json:"cardinality"`
	Keys        []JoinKey `json:"keys"`
}

// JoinKey is one configured key pair.
type JoinKey struct {
	Left  string `json:"left"`
	Right string `json:"right"`
}

// ListRelationships returns configured relationships. It is empty when none are registered.
func (e *Engine) ListRelationships() []RelationshipInfo {
	out := make([]RelationshipInfo, 0, len(e.relationships))
	for _, rel := range e.relationships {
		info := RelationshipInfo{Name: rel.Name, Left: rel.Left, Right: rel.Right, Cardinality: rel.Cardinality}
		for _, key := range rel.Keys {
			info.Keys = append(info.Keys, JoinKey{Left: key.Left, Right: key.Right})
		}
		out = append(out, info)
	}
	return out
}

func prepareSource(cat *dataset.Catalog, src CrossSource, keys []string, loc *time.Location) (preparedSource, error) {
	if len(src.Metrics) == 0 {
		return preparedSource{}, invalid("aggregate each source before joining")
	}
	group := src.GroupBy
	if len(keys) > 0 && !sameSet(group, keys) {
		return preparedSource{}, invalid("group_by must equal the relationship keys before joining")
	}
	b := &sqlBuilder{loc: loc}
	where, err := b.where(cat, src.Filters, src.TimeColumn, src.TimeStart, src.TimeEnd)
	if err != nil {
		return preparedSource{}, err
	}
	var selectList []string
	var groupList []string
	for _, name := range group {
		col, err := requireColumn(cat, name)
		if err != nil {
			return preparedSource{}, err
		}
		quoted, err := quote(col.Name)
		if err != nil {
			return preparedSource{}, err
		}
		selectList = append(selectList, quoted)
		groupList = append(groupList, quoted)
	}
	var warnings []string
	for _, metric := range src.Metrics {
		expr, alias, err := b.metricExpr(cat, metric, "")
		if err != nil {
			return preparedSource{}, err
		}
		selectList = append(selectList, expr+" AS "+mustQuote(alias))
		if metric.Column == "socgain" {
			warnings = append(warnings, "socgain is SOC percentage points, not energy in kWh")
		}
	}
	sql := "SELECT " + strings.Join(selectList, ", ") + " FROM " + fromRelation(cat)
	if where != "" {
		sql += " WHERE " + where
	}
	if len(groupList) > 0 {
		sql += " GROUP BY " + strings.Join(groupList, ", ")
	}
	if len(keys) == 0 {
		keys = append([]string(nil), group...)
	}
	return preparedSource{src: src, cat: cat, sql: sql, args: b.args, keys: keys, warnings: warnings}, nil
}

func findRelationship(rels []config.Relationship, name, leftDataset, rightDataset string) (config.Relationship, bool, error) {
	for _, rel := range rels {
		if rel.Name != name {
			continue
		}
		if rel.Left == leftDataset && rel.Right == rightDataset {
			return rel, false, nil
		}
		if rel.Left == rightDataset && rel.Right == leftDataset {
			return rel, true, nil
		}
		return config.Relationship{}, false, invalid("relationship does not connect these datasets")
	}
	return config.Relationship{}, false, invalid("no relationship is registered for this join")
}

func orientedKeys(rel config.Relationship, flipped bool) ([]string, []string) {
	left := make([]string, len(rel.Keys))
	right := make([]string, len(rel.Keys))
	for i, key := range rel.Keys {
		if flipped {
			left[i] = key.Right
			right[i] = key.Left
		} else {
			left[i] = key.Left
			right[i] = key.Right
		}
	}
	return left, right
}

func requireGrain(cardinality string, flipped bool, left CrossSource, leftKeys []string, right CrossSource, rightKeys []string) error {
	oriented := cardinality
	if flipped {
		switch cardinality {
		case "one_to_many":
			oriented = "many_to_one"
		case "many_to_one":
			oriented = "one_to_many"
		}
	}
	switch oriented {
	case "one_to_one", "many_to_one":
		if !sameSet(left.GroupBy, leftKeys) || len(left.Metrics) == 0 {
			return invalid("aggregate the dataset that can duplicate join keys before joining")
		}
		if !sameSet(right.GroupBy, rightKeys) {
			return invalid("group_by must equal the relationship keys before joining")
		}
		return nil
	case "one_to_many", "many_to_many":
		if !sameSet(left.GroupBy, leftKeys) || !sameSet(right.GroupBy, rightKeys) || len(left.Metrics) == 0 || len(right.Metrics) == 0 {
			return invalid("aggregate each dataset to the join keys before joining")
		}
		return nil
	default:
		return invalid("relationship cardinality is invalid")
	}
}

func sameSet(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	seen := map[string]int{}
	for _, name := range left {
		seen[name]++
	}
	for _, name := range right {
		seen[name]--
		if seen[name] < 0 {
			return false
		}
	}
	return true
}

func uniqueStrings(values []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		out = append(out, value)
	}
	return out
}
