package dataset

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// ConvertOptions controls a CSV to Parquet conversion.
// Types overrides DuckDB inference for named columns.
// SampleSize nil uses DuckDB's default sniff. -1 scans the whole file.
type ConvertOptions struct {
	Input       string
	Output      string
	Delimiter   string
	Header      bool
	Types       map[string]string
	SampleSize  *int
	NullStrings []string
}

// ConvertResult describes a successful conversion.
type ConvertResult struct {
	InputRows   int64
	OutputRows  int64
	Columns     []Column
	OutputBytes int64
	Duration    time.Duration
}

var decimalType = regexp.MustCompile(`^DECIMAL\([0-9]{1,2},[0-9]{1,2}\)$`)

func validateOptions(opt *ConvertOptions) error {
	if opt.Delimiter == "" {
		opt.Delimiter = ","
	}
	if opt.Delimiter == `\t` {
		opt.Delimiter = "\t"
	}
	if len(opt.Delimiter) != 1 {
		return fmt.Errorf("delimiter must be a single character")
	}
	if opt.NullStrings == nil {
		opt.NullStrings = []string{"", "NULL", "null"}
	}
	in, err := filepath.Abs(opt.Input)
	if err != nil {
		return err
	}
	out, err := filepath.Abs(opt.Output)
	if err != nil {
		return err
	}
	opt.Input, opt.Output = in, out
	if opt.Input == opt.Output {
		return fmt.Errorf("output path must not replace the csv")
	}
	info, err := os.Stat(opt.Input)
	if err != nil {
		return fmt.Errorf("csv is missing or unreadable")
	}
	if info.IsDir() {
		return fmt.Errorf("csv path is a directory")
	}
	f, err := os.Open(opt.Input)
	if err != nil {
		return fmt.Errorf("csv is missing or unreadable")
	}
	f.Close()
	if err := os.MkdirAll(filepath.Dir(opt.Output), 0o755); err != nil {
		return err
	}
	for name, typ := range opt.Types {
		if !ValidIdent(name) {
			return fmt.Errorf("type override column is not an identifier")
		}
		canon, ok := canonicalType(typ)
		if !ok {
			return fmt.Errorf("unsupported type override")
		}
		opt.Types[name] = canon
	}
	if opt.SampleSize != nil && *opt.SampleSize < -1 {
		return fmt.Errorf("sample size must be -1 or greater")
	}
	return nil
}

func canonicalType(typ string) (string, bool) {
	u := strings.ToUpper(strings.TrimSpace(typ))
	switch u {
	case "BOOLEAN", "TINYINT", "SMALLINT", "INTEGER", "BIGINT", "FLOAT", "DOUBLE", "VARCHAR", "DATE", "TIME", "TIMESTAMP", "TIMESTAMPTZ", "EPOCH_MS":
		return u, true
	default:
		if decimalType.MatchString(u) {
			return u, true
		}
		return "", false
	}
}
