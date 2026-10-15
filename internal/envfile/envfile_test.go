package envfile

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadSetsMissingAndKeepsExisting(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env")
	body := []byte("# comment\nexport ANALYTICS_AUTH_TOKEN=\"from-file\"\nXAI_API_KEY=\nMCP_URL=http://127.0.0.1:8080/mcp\n")
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ANALYTICS_AUTH_TOKEN", "already-set")
	restore := func(key string) {
		prev, ok := os.LookupEnv(key)
		os.Unsetenv(key)
		t.Cleanup(func() {
			if ok {
				os.Setenv(key, prev)
				return
			}
			os.Unsetenv(key)
		})
	}
	restore("XAI_API_KEY")
	restore("MCP_URL")
	if err := Load(path); err != nil {
		t.Fatal(err)
	}
	if os.Getenv("ANALYTICS_AUTH_TOKEN") != "already-set" {
		t.Fatalf("token %q", os.Getenv("ANALYTICS_AUTH_TOKEN"))
	}
	if os.Getenv("XAI_API_KEY") != "" {
		t.Fatalf("key %q", os.Getenv("XAI_API_KEY"))
	}
	if _, ok := os.LookupEnv("XAI_API_KEY"); !ok {
		t.Fatal("empty key was not loaded")
	}
	if os.Getenv("MCP_URL") != "http://127.0.0.1:8080/mcp" {
		t.Fatalf("url %q", os.Getenv("MCP_URL"))
	}
}

func TestMissingFile(t *testing.T) {
	err := Load(filepath.Join(t.TempDir(), ".env"))
	if err == nil {
		t.Fatal("expected missing file error")
	}
}
