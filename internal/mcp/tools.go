package mcp

import (
	"context"
	"log/slog"

	"github.com/Intellicars/intellicarai/internal/analytics"
	"github.com/Intellicars/intellicarai/internal/security"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func registerTools(server *sdkmcp.Server, engine *analytics.Engine, logger *slog.Logger) {
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "describe_dataset",
		Description: "Describe the configured dataset: column names, types, units, and which columns are metrics, dimensions, or time columns. " +
			"Use this before other tools. It does not return dataset rows.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in analytics.DescribeInput) (*sdkmcp.CallToolResult, analytics.DescribeResult, error) {
		out, err := engine.Describe(in)
		return nil, out, toolError(logger, err)
	})
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "analyze",
		Description: "Aggregate the configured dataset. Use for averages, sums, counts, grouped totals, top-N, bottom-N, and time buckets. " +
			"avg is the unweighted mean of raw rows unless weight_column is set. Results are compact aggregates, never raw rows.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in analytics.AnalyzeInput) (*sdkmcp.CallToolResult, analytics.AnalyzeResult, error) {
		out, err := engine.Analyze(ctx, in)
		return nil, out, toolError(logger, err)
	})
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "statistical_analysis",
		Description: "Compute sample standard deviation (stddev_samp), sample variance (var_samp), median and percentiles (quantile_cont, Hyndman-Fan type 7), " +
			"Pearson correlation, missing-value counts, a bounded histogram, and Tukey outlier counts with k=1.5. " +
			"Correlation is null when a column is constant or the sample has fewer than two complete rows. Does not return raw rows.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in analytics.StatsInput) (*sdkmcp.CallToolResult, analytics.StatsResult, error) {
		out, err := engine.StatisticalAnalysis(ctx, in)
		return nil, out, toolError(logger, err)
	})
	sdkmcp.AddTool(server, &sdkmcp.Tool{
		Name: "compare_periods",
		Description: "Compare one metric across two half-open time ranges. Percentage change is ((current - previous) / previous) * 100 " +
			"and is null when the previous value is zero or missing. Requires a date or time column.",
	}, func(ctx context.Context, _ *sdkmcp.CallToolRequest, in analytics.CompareInput) (*sdkmcp.CallToolResult, analytics.CompareResult, error) {
		out, err := engine.Compare(ctx, in)
		return nil, out, toolError(logger, err)
	})
}

func toolError(logger *slog.Logger, err error) error {
	if err == nil {
		return nil
	}
	logger.Error("tool failed", "error", err)
	return errPublic(err)
}

func errPublic(err error) error {
	return errString(security.Public(err))
}

type publicError string

func (e publicError) Error() string { return string(e) }

func errString(msg string) error { return publicError(msg) }
