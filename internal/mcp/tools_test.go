package mcp_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Intellicars/intellicarai/internal/analytics"
	"github.com/Intellicars/intellicarai/internal/config"
	"github.com/Intellicars/intellicarai/internal/dataset"
	"github.com/Intellicars/intellicarai/internal/mcp"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestToolFlow(t *testing.T) {
	engine := openEngine(t)
	server := mcp.New(engine, nil)
	ctx := context.Background()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	serverTransport, clientTransport := sdkmcp.NewInMemoryTransports()
	if _, err := server.Connect(ctx, serverTransport, nil); err != nil {
		t.Fatal(err)
	}
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()

	described, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name:      "describe_dataset",
		Arguments: analytics.DescribeInput{Search: "distance"},
	})
	if err != nil || described.IsError {
		t.Fatalf("describe err %v result %#v", err, described)
	}
	var description analytics.DescribeResult
	decode(t, described.StructuredContent, &description)
	if len(description.Columns) != 1 || description.Columns[0].Name != "distance" {
		t.Fatalf("describe %#v", description)
	}

	analyzed, err := session.CallTool(ctx, &sdkmcp.CallToolParams{
		Name: "analyze",
		Arguments: analytics.AnalyzeInput{
			Metrics: []analytics.MetricInput{{Column: "distance", Aggregation: "avg"}},
		},
	})
	if err != nil || analyzed.IsError {
		t.Fatalf("analyze err %v result %#v", err, analyzed)
	}
	var result analytics.AnalyzeResult
	decode(t, analyzed.StructuredContent, &result)
	if result.RowCount != 1 || len(result.Rows) != 1 {
		t.Fatalf("analyze %#v", result)
	}
}

func TestHTTPAuth(t *testing.T) {
	engine := openEngine(t)
	server := mcp.New(engine, nil)
	handler := mcp.ProtectedHandler("change-me", mcp.HTTPHandler(server, nil, 1<<20))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx := context.Background()
	client := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "test", Version: "0"}, nil)
	_, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true,
		HTTPClient:           &http.Client{Timeout: 5 * time.Second},
	}, nil)
	if err == nil {
		t.Fatal("expected unauthorized client")
	}

	authed := &http.Client{Timeout: 5 * time.Second, Transport: headerTripper{base: http.DefaultTransport, token: "change-me"}}
	session, err := client.Connect(ctx, &sdkmcp.StreamableClientTransport{
		Endpoint:             httpServer.URL,
		DisableStandaloneSSE: true,
		HTTPClient:           authed,
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(tools.Tools) != 4 {
		t.Fatalf("tools %d", len(tools.Tools))
	}
}

type headerTripper struct {
	base  http.RoundTripper
	token string
}

func (h headerTripper) RoundTrip(r *http.Request) (*http.Response, error) {
	clone := r.Clone(r.Context())
	clone.Header.Set("Authorization", "Bearer "+h.token)
	return h.base.RoundTrip(clone)
}

func openEngine(t *testing.T) *analytics.Engine {
	t.Helper()
	dir := t.TempDir()
	csvPath := filepath.Join(dir, "trips.csv")
	body := "fleetid,vehicleid,regno,chassisno,starttime,endtime,startodo,endodo,startsoc,endsoc,socconsumed,distance,duration,rangevalue,createdat\n" +
		"1,bgauss_in_1166,KA01AR9752,00123,2024-01-15T10:00:00,2024-01-15T10:20:00,10,15,80,70,10,5.132,1200000,51.32,2024-01-15T11:00:00\n"
	if err := os.WriteFile(csvPath, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	parquet := filepath.Join(dir, "trips.parquet")
	if _, err := dataset.Convert(context.Background(), dataset.ConvertOptions{
		Input:  csvPath,
		Output: parquet,
		Header: true,
		Types:  map[string]string{"vehicleid": "VARCHAR", "regno": "VARCHAR", "chassisno": "VARCHAR"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Transport: "stdio", ParquetPath: parquet, DatasetName: "trips",
		DuckDBMemoryLimit: "256MB", TempDir: filepath.Join(dir, "tmp"),
		QueryTimeout: time.Second * 5, MaxConcurrentQueries: 2, MaxResultRows: 10,
		MaxGroups: 10, MaxHistogramBins: 5, LogLevel: "error", DefaultTimezone: "UTC",
		MaxRequestBytes: 1024,
	}
	if err := cfg.Validate(); err != nil {
		t.Fatal(err)
	}
	engine, err := analytics.Open(context.Background(), cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { engine.Close() })
	return engine
}

func decode(t *testing.T, value any, dest any) {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, dest); err != nil {
		t.Fatal(err)
	}
}
