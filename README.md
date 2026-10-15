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

`configs/config.example.yaml` lists every setting. The server and client read `.env` from the working directory at startup and apply those variables. A variable already set in the process is left unchanged. `.env.example` is the template. Copy it to `.env` and put the xAI key there. `.env` is not committed.

HTTP transport requires `ANALYTICS_AUTH_TOKEN`. Stdio transport does not, because it trusts the local process. The default HTTP mode is stateless streamable HTTP so clients can negotiate protocol `2026-07-28`.

On startup the process allows DuckDB to read only the configured Parquet file and to write only the temp directory, disables extension installation, then locks DuckDB configuration. Query slots, timeouts, and row limits are enforced around every tool call. Cancellation uses the request context. DuckDB honors interrupts at checkpoints, so a pathological plan can still run until the process is stopped.

## Run locally

```bash
cp .env.example .env
make test
make convert
make run
```

Each analytics statement is written to the server stderr as JSON, with the SQL and its bound arguments, before DuckDB runs it.

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
docker compose up --build
```

Compose reads `ANALYTICS_AUTH_TOKEN` from the `.env` file in this directory.

The image does not contain a dataset. The container runs as uid 65532, with a read-only root filesystem and a writable `/tmp` for DuckDB spill files.

## Tests

`go test ./...` covers conversion, schema discovery, overrides, aggregates, filters, sort and limit, time ranges, period comparison, statistics, nulls, invalid columns and operators, a zero denominator, SQL injection text, a missing Parquet path, query cancellation, truncation, and MCP tool calls over the in-memory and HTTP transports. No external database or API key is required.

## Client

The terminal client connects to this server, discovers its tools, and asks xAI Grok to choose those tools. Grok sees column metadata and aggregate results. It does not see the CSV or Parquet file, and it cannot submit SQL. The server still enforces allowlists, parameters, and limits.

```text
question → client → Grok → MCP tool → DuckDB aggregate → Grok explanation
```

Start the server in one terminal, then the client in another:

```bash
make convert
make run
```

```bash
make run-client
```

Put `XAI_API_KEY` in `.env` before starting the client. `MCP_AUTH_TOKEN` must match `ANALYTICS_AUTH_TOKEN`. The key is sent only to `https://api.x.ai/v1/chat/completions`. The model name is the `XAI_MODEL` value from `.env`. If the account rejects `grok-4.6`, the client reports that error and does not switch models.

`configs/client.example.yaml` holds the server URL, timeouts, and tool-round limit. Do not commit a real key.

Questions are ordinary sentences, for example:

- Which columns are available for analysis?
- How many records are in this dataset?
- What is the average distance by vehicle?
- Compare total distance for 14 October 2026 with the previous day.
- Is there a correlation between distance and rangevalue?
- Are there outliers in distance?

The sample trips start on 2026-10-14 UTC. A time filter has to name `time_column` because `starttime`, `endtime`, and `createdat` are all timestamps. The client puts that rule, plus the current UTC time and the `describe_dataset` result, in the model prompt. Answers are aggregates, not trip rows. Type `exit` or `quit`, or press Ctrl+C, to stop.

For a server you launch yourself over stdio, set `transport: stdio` and `server_command` in the client YAML. The child process does not inherit `XAI_API_KEY`.

`go test ./...` uses a fake xAI server and an in-process analytics server. It does not call xAI. Set `MCP_URL` and `MCP_AUTH_TOKEN` to also run the test that talks to a server you already started.

If the client cannot connect, confirm `make run` is listening and the bearer token matches. A 401 means the token is wrong. A timeout means the server or xAI did not answer within `request_timeout`. Tool-call loops stop after `max_tool_rounds`. The client does not store the conversation after it exits.
