# Analytics MCP server

One Go process answers analytical questions about a static CSV. A setup command converts that CSV to Parquet. The server inspects the Parquet schema and answers MCP tool calls by running bounded SQL in embedded DuckDB. The model never receives the dataset and cannot submit its own SQL.

There is no source database, refresh job, or embedded language model. The connected MCP client supplies the model.

## Dependencies

Pinned in `go.mod`:

- Go 1.26
- `github.com/modelcontextprotocol/go-sdk` v1.8.0
- `github.com/duckdb/duckdb-go/v2` v2.10506.0, which bundles DuckDB 1.5.6

The DuckDB driver is the official embedded client. It uses cgo and statically links a prebuilt library for Linux amd64/arm64, macOS, and Windows. Parquet support is already in that library. A C compiler is required for `go test` and `go build`. The container uses Debian, not Alpine, because the bundled library is built for glibc.

## Layout

- `cmd/server` runs the MCP service.
- `scripts/convert_csv.go` is the one-shot CSV to Parquet command.
- `internal/dataset` converts files and caches schema metadata.
- `internal/analytics` validates requests and builds parameterized SQL.
- `internal/mcp` registers the tools.
- `internal/security` checks bearer tokens and limits concurrency.

## Type inference

DuckDB sniffs CSV types from a sample. The candidate order is boolean, time, date, timestamp, bigint, double, then varchar. Identifiers such as `bgauss_in_1166` and `KA01AR9752` must be overridden to `VARCHAR` or they can be inferred as numbers.

`testdata/trips.csv` stores `starttime`, `endtime`, and `createdat` as Unix epoch milliseconds. `EPOCH_MS` is a converter override, not a DuckDB type. It writes `epoch_ms(CAST(column AS BIGINT))`, which becomes a `TIMESTAMP`. `duration` stays a number of milliseconds. The file has three time columns, so a tool call that filters or buckets time must set `time_column`.

```bash
go run ./scripts/convert_csv.go \
  -input testdata/trips.csv \
  -output data/parquet/trips.parquet \
  -types vehicleid:VARCHAR,regno:VARCHAR,chassisno:VARCHAR,starttime:EPOCH_MS,endtime:EPOCH_MS,createdat:EPOCH_MS
```

`-types` accepts a comma-separated list of `column:TYPE`. Allowed types are BOOLEAN, TINYINT, SMALLINT, INTEGER, BIGINT, FLOAT, DOUBLE, DECIMAL(p,s), VARCHAR, DATE, TIME, TIMESTAMP, TIMESTAMPTZ, and EPOCH_MS. `-sample-size -1` sniffs the whole file. `-delimiter` sets a single-character separator (`\t` is tab). `-header=false` treats the first row as data.

Empty fields and the tokens `NULL` and `null` are written as SQL nulls. The command checks that the input exists, writes `output.partial`, checks the Parquet row count and schema, and renames the file into place only after that succeeds. It refuses to use the CSV path as the output. Column names must be identifiers: a letter or underscore, then letters, digits, and underscores.

`make convert` runs the sample command above.

## Statistical methods

DuckDB evaluates every aggregate. Go only computes percentage change and Tukey fences from SQL quantiles.

- `avg` is the unweighted mean of non-null raw rows. It is not an average of group averages. `weight_column` selects `sum(metric * weight) / sum(weight)` and returns null when the weight sum is null or zero.
- `stddev` and `variance` are the sample statistics `stddev_samp` and `var_samp`.
- Median and percentiles use `quantile_cont` (Hyndman-Fan type 7, interpolated).
- Correlation uses `corr`, Pearson's population correlation. The coefficient is null when fewer than two complete pairs exist or either column has zero sample variance.
- Outliers use Tukey fences with k = 1.5: lower = q1 - 1.5 * IQR and upper = q3 + 1.5 * IQR, with q1 and q3 from `quantile_cont`. The response contains fences and a count, not the outlier rows. Grouped outlier detection is rejected.
- Distributions are equal-width bins. DuckDB 1.5.6 does not provide `width_bucket`, so the bin index is `floor((value - min) / width) + 1`, clamped to the requested bin count. The maximum is `max_histogram_bins`. Grouped histograms are rejected.
- Nulls are ignored by SQL aggregates. `count` without a column is `count(*)`. Missing count is `count(*) - count(column)`.
- Duplicate rows are included. Nothing deduplicates them.
- Percentage change is `((current - previous) / previous) * 100`. It is null when the previous value is zero or either value is missing.
- Periods are half-open. Naive timestamps are wall-clock values in the configured timezone. Weeks start on Monday.

## Tools

`describe_dataset` returns names, types, units, and roles. It does not return rows.

`analyze` runs allowlisted aggregates, filters, grouping, time buckets, sort, rank, and limit.

`statistical_analysis` runs the statistics above.

`compare_periods` compares one metric across two time ranges. A dataset with no date or time column returns a validation error.

There is no `execute_sql` tool. Dataset names, column names, aggregations, operators, and time buckets come from allowlists. Filter values are bound as SQL parameters. Time bounds are formatted only after they parse as timestamps.

## Configuration

`configs/config.example.yaml` lists every setting. Environment variables prefixed with `ANALYTICS_` override the file. `ANALYTICS_CONFIG` selects the YAML path. `.env.example` has placeholders only.

HTTP transport requires `ANALYTICS_AUTH_TOKEN`. Stdio transport does not, because it trusts the local process. The default HTTP mode is stateless streamable HTTP so clients can negotiate protocol `2026-07-28`.

On startup the process allows DuckDB to read only the configured Parquet file and to write only the temp directory, disables extension installation, then locks DuckDB configuration. Query slots, timeouts, and row limits are enforced around every tool call. Cancellation uses the request context. DuckDB honors interrupts at checkpoints, so a pathological plan can still run until the process is stopped.

## Run locally

```bash
make test
make convert
export ANALYTICS_CONFIG=./configs/config.example.yaml
make run
```

The sample client entry is:

```json
{
  "mcpServers": {
    "analytics": {
      "url": "http://127.0.0.1:8080/mcp",
      "headers": {
        "Authorization": "Bearer change-me"
      }
    }
  }
}
```

Stdio:

```json
{
  "mcpServers": {
    "analytics": {
      "command": "analytics-mcp",
      "env": {
        "ANALYTICS_TRANSPORT": "stdio",
        "ANALYTICS_PARQUET_PATH": "/absolute/path/trips.parquet",
        "ANALYTICS_DATASET_NAME": "trips"
      }
    }
  }
}
```

A client asks a question, chooses a tool, and explains the compact JSON result. The same functions are covered by `go test` without a model.

## Docker

Convert the CSV on the host, then mount the Parquet directory read-only:

```bash
make convert
export ANALYTICS_AUTH_TOKEN=change-me
docker compose up --build
```

The image does not contain a dataset. The container runs as uid 65532, with a read-only root filesystem and a writable `/tmp` for DuckDB spill files.

## Tests

`go test ./...` covers conversion, schema discovery, overrides, aggregates, filters, sort and limit, time ranges, period comparison, statistics, nulls, invalid columns and operators, a zero denominator, SQL injection text, a missing Parquet path, query cancellation, truncation, and MCP tool calls over the in-memory and HTTP transports. No external database or API key is required.
