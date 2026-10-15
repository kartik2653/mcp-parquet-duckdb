package mcpclient

import (
	"context"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Intellicars/intellicarai/internal/analytics"
	"github.com/Intellicars/intellicarai/internal/config"
	"github.com/Intellicars/intellicarai/internal/dataset"
	"github.com/Intellicars/intellicarai/internal/mcp"
	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

func TestConnectUnavailable(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err := Connect(ctx, Config{
		Transport: "http",
		ServerURL: "http://127.0.0.1:1",
		AuthToken: "change-me",
		Timeout:   time.Second,
	})
	if err == nil || !strings.Contains(err.Error(), "unavailable") {
		t.Fatalf("err %v", err)
	}
}

func TestRejectsUnknownAndInvalidArguments(t *testing.T) {
	c := &Client{}
	c.remember(sdkmcp.Tool{
		Name: "analyze",
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"metrics": map[string]any{"type": "array"},
			},
			"additionalProperties": false,
			"required":             []any{"metrics"},
		},
	})
	_, err := c.Call(context.Background(), "missing", []byte(`{}`))
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("unknown %v", err)
	}
	_, err = c.Call(context.Background(), "analyze", []byte(`{"metrics":[],"sql":"drop table"}`))
	if !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("invalid %v", err)
	}
	_, err = c.Call(context.Background(), "analyze", []byte(`{`))
	if !errors.Is(err, ErrInvalidArgs) {
		t.Fatalf("malformed %v", err)
	}
}

func TestResultPreservesNumbers(t *testing.T) {
	body, err := resultJSON(&sdkmcp.CallToolResult{
		StructuredContent: map[string]any{"avg_distance": json.Number("5.132")},
	})
	if err != nil {
		t.Fatal(err)
	}
	if string(body) != `{"avg_distance":5.132}` {
		t.Fatalf("body %s", body)
	}
	text, err := resultJSON(&sdkmcp.CallToolResult{
		Content: []sdkmcp.Content{&sdkmcp.TextContent{Text: "not json"}},
	})
	if err != nil || string(text) != `"not json"` {
		t.Fatalf("text %s %v", text, err)
	}
}

func TestHTTPDiscoveryAndCall(t *testing.T) {
	engine := openEngine(t)
	server := mcp.New(engine, nil)
	handler := mcp.ProtectedHandler("change-me", mcp.HTTPHandler(server, nil, 1<<20))
	httpServer := httptest.NewServer(handler)
	defer httpServer.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := Connect(ctx, Config{
		Transport: "http",
		ServerURL: httpServer.URL + "/mcp",
		AuthToken: "change-me",
		Timeout:   10 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	tools, err := client.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	names := map[string]bool{}
	for _, tool := range tools {
		names[tool.Name] = true
		if len(tool.Parameters) == 0 {
			t.Fatalf("missing schema %s", tool.Name)
		}
	}
	for _, name := range []string{"describe_dataset", "analyze", "statistical_analysis", "compare_periods", "list_datasets", "list_relationships", "cross_dataset_query"} {
		if !names[name] {
			t.Fatalf("missing %s in %v", name, names)
		}
	}
	res, err := client.Call(ctx, "describe_dataset", []byte(`{"search":"distance"}`))
	if err != nil || res.IsError {
		t.Fatalf("call %v %#v", err, res)
	}
	if !strings.Contains(string(res.Body), "distance") {
		t.Fatalf("body %s", res.Body)
	}
	_, err = client.Call(ctx, "not_a_tool", []byte(`{}`))
	if !errors.Is(err, ErrUnknownTool) {
		t.Fatalf("unknown live %v", err)
	}
}

func TestLiveServerWhenConfigured(t *testing.T) {
	endpoint := os.Getenv("MCP_URL")
	if endpoint == "" {
		t.Skip("MCP_URL is not set")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	client, err := Connect(ctx, Config{
		Transport: "http",
		ServerURL: endpoint,
		AuthToken: os.Getenv("MCP_AUTH_TOKEN"),
		Timeout:   15 * time.Second,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	res, err := client.Call(ctx, "describe_dataset", []byte(`{}`))
	if err != nil || res.IsError {
		t.Fatalf("%v %#v", err, res)
	}
}

func (c *Client) remember(tool sdkmcp.Tool) {
	if c.tools == nil {
		c.tools = map[string]advertised{}
	}
	body, err := json.Marshal(tool.InputSchema)
	if err != nil {
		panic(err)
	}
	item := advertised{tool: Tool{Name: tool.Name, Description: tool.Description, Parameters: body}}
	var schema jsonschema.Schema
	if json.Unmarshal(body, &schema) == nil {
		if resolved, err := schema.Resolve(nil); err == nil {
			item.schema = resolved
		}
	}
	c.tools[tool.Name] = item
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
		Input: csvPath, Output: parquet, Header: true,
		Types: map[string]string{"vehicleid": "VARCHAR", "regno": "VARCHAR", "chassisno": "VARCHAR"},
	}); err != nil {
		t.Fatal(err)
	}
	cfg := config.Config{
		Transport: "stdio", ParquetPath: parquet, DatasetName: "trips",
		DuckDBMemoryLimit: "256MB", TempDir: filepath.Join(dir, "tmp"),
		QueryTimeout: 5 * time.Second, MaxConcurrentQueries: 2, MaxResultRows: 10,
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
