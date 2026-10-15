package analytics

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/duckdb/duckdb-go/v2"
)

func scanAll(rows *sql.Rows) ([]map[string]any, error) {
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var out []map[string]any
	for rows.Next() {
		values := make([]any, len(names))
		ptrs := make([]any, len(names))
		for i := range values {
			ptrs[i] = &values[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, err
		}
		row := make(map[string]any, len(names))
		for i, name := range names {
			row[name] = normalizeValue(values[i])
		}
		out = append(out, row)
	}
	if out == nil {
		out = []map[string]any{}
	}
	return out, rows.Err()
}

func normalizeValue(v any) any {
	switch t := v.(type) {
	case nil:
		return nil
	case time.Time:
		return t.UTC().Format(time.RFC3339Nano)
	case duckdb.Decimal:
		return t.String()
	case []byte:
		return string(t)
	case float32:
		return float64(t)
	case int8:
		return int64(t)
	case int16:
		return int64(t)
	case int32:
		return int64(t)
	case uint8:
		return int64(t)
	case uint16:
		return int64(t)
	case uint32:
		return int64(t)
	case uint64:
		if t > uint64(^uint64(0)>>1) {
			return fmt.Sprint(t)
		}
		return int64(t)
	default:
		return v
	}
}

func trimLimit(rows []map[string]any, limit int) ([]map[string]any, bool) {
	if len(rows) > limit {
		return rows[:limit], true
	}
	return rows, false
}
