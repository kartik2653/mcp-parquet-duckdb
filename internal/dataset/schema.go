package dataset

import (
	"errors"
	"regexp"
)

// ErrInvalidIdent means a name cannot be used as a SQL identifier.
var ErrInvalidIdent = errors.New("invalid identifier")

var errInvalidIdent = ErrInvalidIdent

// Kind is the analytical role of a column.
type Kind string

const (
	KindNumeric     Kind = "numeric"
	KindBoolean     Kind = "boolean"
	KindDate        Kind = "date"
	KindTime        Kind = "time"
	KindTimestamp   Kind = "timestamp"
	KindTimestampTZ Kind = "timestamptz"
	KindCategorical Kind = "categorical"
)

// Column is one field discovered from Parquet or declared in metadata.
type Column struct {
	Name                 string
	DuckType             string
	Nullable             bool
	Kind                 Kind
	BusinessName         string
	Description          string
	Unit                 string
	ApprovedAggregations []string
}

// Temporal reports whether the column is a date or time.
func (c Column) Temporal() bool {
	switch c.Kind {
	case KindDate, KindTime, KindTimestamp, KindTimestampTZ:
		return true
	default:
		return false
	}
}

// identifierPattern is the only identifier shape placed into SQL.
var identifierPattern = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidIdent reports whether name can be quoted into SQL.
func ValidIdent(name string) bool {
	return identifierPattern.MatchString(name)
}

// QuoteIdent quotes a validated identifier.
func QuoteIdent(name string) (string, error) {
	if !ValidIdent(name) {
		return "", errInvalidIdent
	}
	return `"` + name + `"`, nil
}
