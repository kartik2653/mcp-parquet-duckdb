package dataset

import (
	"fmt"
	"os"
	"strings"

	"gopkg.in/yaml.v3"
)

// ColumnMeta is optional business metadata for one column.
type ColumnMeta struct {
	Name         string
	Description  string
	Unit         string
	Aggregations []string
}

// Metadata is optional interpretation layered on the discovered schema.
type Metadata struct {
	Description string
	Columns     map[string]ColumnMeta
}

// Catalog is the in-memory description of the configured dataset.
type Catalog struct {
	Name        string
	Description string
	columns     []Column
	byName      map[string]Column
}

// NewCatalog builds a catalog from an inspected schema and optional metadata.
func NewCatalog(name, description string, columns []Column, meta Metadata) (*Catalog, error) {
	if !ValidIdent(name) {
		return nil, fmt.Errorf("dataset name is not an identifier")
	}
	if meta.Columns == nil {
		meta.Columns = map[string]ColumnMeta{}
	} else {
		copied := make(map[string]ColumnMeta, len(meta.Columns))
		for k, v := range meta.Columns {
			copied[k] = v
		}
		meta.Columns = copied
	}
	if description == "" {
		description = meta.Description
	}
	byName := make(map[string]Column, len(columns))
	out := make([]Column, len(columns))
	for i, col := range columns {
		if !ValidIdent(col.Name) {
			return nil, fmt.Errorf("column is not a supported identifier")
		}
		if extra, ok := meta.Columns[col.Name]; ok {
			if extra.Name != "" {
				col.BusinessName = extra.Name
			}
			if extra.Description != "" {
				col.Description = extra.Description
			}
			if extra.Unit != "" {
				col.Unit = extra.Unit
			}
			if len(extra.Aggregations) > 0 {
				for _, agg := range extra.Aggregations {
					if !knownAggregation(agg) {
						return nil, fmt.Errorf("unsupported aggregation in metadata")
					}
				}
				col.ApprovedAggregations = append([]string(nil), extra.Aggregations...)
			}
			delete(meta.Columns, col.Name)
		}
		if col.BusinessName == "" {
			col.BusinessName = col.Name
		}
		if len(col.ApprovedAggregations) == 0 {
			col.ApprovedAggregations = defaultAggregations(col.Kind)
		}
		out[i] = col
		byName[col.Name] = col
	}
	if len(meta.Columns) > 0 {
		return nil, fmt.Errorf("metadata names a column that is not in the dataset")
	}
	return &Catalog{Name: name, Description: description, columns: out, byName: byName}, nil
}

func knownAggregation(agg string) bool {
	switch agg {
	case "count", "count_distinct", "sum", "avg", "min", "max":
		return true
	default:
		return false
	}
}

// Columns returns a copy of the schema.
func (c *Catalog) Columns() []Column {
	out := make([]Column, len(c.columns))
	copy(out, c.columns)
	return out
}

// Column looks up a column by name.
func (c *Catalog) Column(name string) (Column, bool) {
	col, ok := c.byName[name]
	return col, ok
}

// TimeColumns returns date and time columns.
func (c *Catalog) TimeColumns() []Column {
	var out []Column
	for _, col := range c.columns {
		if col.Temporal() {
			out = append(out, col)
		}
	}
	return out
}

// Select returns columns matching an optional name list and search string.
func (c *Catalog) Select(names []string, search string) ([]Column, error) {
	search = strings.ToLower(strings.TrimSpace(search))
	if len(names) == 0 && search == "" {
		return c.Columns(), nil
	}
	allowed := map[string]bool{}
	for _, name := range names {
		if _, ok := c.byName[name]; !ok {
			return nil, fmt.Errorf("unknown column")
		}
		allowed[name] = true
	}
	var out []Column
	for _, col := range c.columns {
		if len(allowed) > 0 && !allowed[col.Name] {
			continue
		}
		if search != "" && !matchesSearch(col, search) {
			continue
		}
		out = append(out, col)
	}
	return out, nil
}

func matchesSearch(col Column, search string) bool {
	blob := strings.ToLower(col.Name + " " + col.BusinessName + " " + col.Description + " " + col.Unit)
	return strings.Contains(blob, search)
}

// LoadMetadata reads a YAML metadata file. An empty path returns empty metadata.
func LoadMetadata(path string) (Metadata, error) {
	if path == "" {
		return Metadata{}, nil
	}
	body, err := os.ReadFile(path)
	if err != nil {
		return Metadata{}, fmt.Errorf("metadata file is missing or unreadable")
	}
	return parseMetadata(body)
}

type metadataFile struct {
	Description string `yaml:"description"`
	Columns     map[string]struct {
		Name         string   `yaml:"name"`
		Description  string   `yaml:"description"`
		Unit         string   `yaml:"unit"`
		Aggregations []string `yaml:"aggregations"`
	} `yaml:"columns"`
}

func parseMetadata(body []byte) (Metadata, error) {
	var file metadataFile
	if err := yaml.Unmarshal(body, &file); err != nil {
		return Metadata{}, fmt.Errorf("metadata file is invalid")
	}
	meta := Metadata{Description: file.Description, Columns: map[string]ColumnMeta{}}
	for name, col := range file.Columns {
		if !ValidIdent(name) {
			return Metadata{}, fmt.Errorf("metadata column is not an identifier")
		}
		meta.Columns[name] = ColumnMeta{
			Name:         col.Name,
			Description:  col.Description,
			Unit:         col.Unit,
			Aggregations: col.Aggregations,
		}
	}
	return meta, nil
}
