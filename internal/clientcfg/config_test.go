package clientcfg

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestLoadDefaultsNeedToken(t *testing.T) {
	t.Setenv("CLIENT_CONFIG", "")
	t.Setenv("MCP_AUTH_TOKEN", "")
	t.Setenv("MCP_URL", "")
	t.Setenv("MCP_TRANSPORT", "")
	_, err := Load("")
	if err == nil || err.Error() != "MCP_AUTH_TOKEN is required for HTTP" {
		t.Fatalf("err %v", err)
	}
}

func TestLoadYAMLAndEnv(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.yaml")
	body := []byte("transport: http\nserver_url: http://127.0.0.1:9090/mcp\nmodel: grok-4.6\nmax_tool_rounds: 3\nrequest_timeout: 5s\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("CLIENT_CONFIG", "")
	t.Setenv("MCP_AUTH_TOKEN", "local-token")
	t.Setenv("XAI_API_KEY", "secret-key")
	t.Setenv("XAI_MODEL", "grok-4.6")
	t.Setenv("MCP_URL", "http://127.0.0.1:9091/mcp")
	t.Setenv("CLIENT_MAX_TOOL_ROUNDS", "4")
	cfg, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ServerURL != "http://127.0.0.1:9091/mcp" || cfg.AuthToken != "local-token" || cfg.APIKey != "secret-key" {
		t.Fatalf("%+v", cfg)
	}
	if cfg.MaxToolRounds != 4 || cfg.RequestTimeout != 5*time.Second || cfg.Model != "grok-4.6" {
		t.Fatalf("%+v", cfg)
	}
}

func TestRejectsBadTransportAndLimits(t *testing.T) {
	t.Setenv("MCP_TRANSPORT", "tcp")
	t.Setenv("MCP_AUTH_TOKEN", "t")
	t.Setenv("CLIENT_CONFIG", "")
	if _, err := Load(""); err == nil {
		t.Fatal("expected transport error")
	}
	t.Setenv("MCP_TRANSPORT", "http")
	t.Setenv("CLIENT_MAX_TOOL_ROUNDS", "0")
	if _, err := Load(""); err == nil {
		t.Fatal("expected round limit error")
	}
}

func TestStdioRequiresCommand(t *testing.T) {
	t.Setenv("CLIENT_CONFIG", "")
	t.Setenv("MCP_TRANSPORT", "stdio")
	t.Setenv("MCP_SERVER_COMMAND", "")
	t.Setenv("MCP_AUTH_TOKEN", "")
	if _, err := Load(""); err == nil {
		t.Fatal("expected command error")
	}
	t.Setenv("MCP_SERVER_COMMAND", "go run ./cmd/server")
	cfg, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ServerCommand) != 3 || cfg.ServerCommand[0] != "go" {
		t.Fatalf("%v", cfg.ServerCommand)
	}
}
