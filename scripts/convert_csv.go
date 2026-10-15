package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/Intellicars/intellicarai/internal/dataset"
)

func main() {
	logger := slog.New(slog.NewJSONHandler(os.Stderr, nil))
	if err := run(logger); err != nil {
		logger.Error("conversion failed", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	input := flag.String("input", "", "input CSV path")
	output := flag.String("output", "", "output Parquet path")
	delimiter := flag.String("delimiter", ",", "field delimiter; use \\t for tab")
	header := flag.Bool("header", true, "CSV has a header row")
	types := flag.String("types", "", "column type overrides, for example vehicleid:VARCHAR,starttime:EPOCH_MS,alert_time:STRPTIME:%d %b %y | %H:%M:%S")
	rename := flag.String("rename", "", "header renames, for example alert time:alert_time")
	sampleSize := flag.Int("sample-size", 0, "type sniff sample size; -1 scans the whole file; 0 uses the DuckDB default")
	flag.Parse()
	if *input == "" || *output == "" {
		return fmt.Errorf("input and output are required")
	}
	opt := dataset.ConvertOptions{
		Input:     *input,
		Output:    *output,
		Delimiter: *delimiter,
		Header:    *header,
		Types:     parseTypes(*types),
		Rename:    parseTypes(*rename),
	}
	if *sampleSize != 0 {
		n := *sampleSize
		opt.SampleSize = &n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, err := dataset.Convert(ctx, opt)
	if err != nil {
		return err
	}
	logger.Info("converted csv to parquet",
		"input_rows", result.InputRows,
		"output_rows", result.OutputRows,
		"output_bytes", result.OutputBytes,
		"duration", result.Duration.String(),
		"columns", columnSummary(result.Columns),
	)
	return nil
}

func parseTypes(raw string) map[string]string {
	if strings.TrimSpace(raw) == "" {
		return nil
	}
	out := map[string]string{}
	for _, part := range strings.Split(raw, ",") {
		name, typ, ok := strings.Cut(part, ":")
		if !ok {
			continue
		}
		out[strings.TrimSpace(name)] = strings.TrimSpace(typ)
	}
	return out
}

func columnSummary(cols []dataset.Column) []string {
	out := make([]string, len(cols))
	for i, col := range cols {
		out[i] = col.Name + ":" + col.DuckType
	}
	return out
}
