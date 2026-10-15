package clientcfg

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// Config is the analytics MCP client configuration.
// API keys and the MCP bearer token are read from the environment, not from YAML.
type Config struct {
	Transport          string
	ServerURL          string
	AuthToken          string
	ServerCommand      []string
	APIKey             string
	Model              string
	BaseURL            string
	RequestTimeout     time.Duration
	MaxToolRounds      int
	MaxHistoryMessages int
	MaxToolResultBytes int
	LogLevel           string
}

type fileConfig struct {
	Transport          string   `yaml:"transport"`
	ServerURL          string   `yaml:"server_url"`
	ServerCommand      []string `yaml:"server_command"`
	Model              string   `yaml:"model"`
	BaseURL            string   `yaml:"base_url"`
	RequestTimeout     string   `yaml:"request_timeout"`
	MaxToolRounds      *int     `yaml:"max_tool_rounds"`
	MaxHistoryMessages *int     `yaml:"max_history_messages"`
	MaxToolResultBytes *int     `yaml:"max_tool_result_bytes"`
	LogLevel           string   `yaml:"log_level"`
}

// Load reads CLIENT_CONFIG or path, then applies environment overrides.
func Load(path string) (Config, error) {
	if path == "" {
		path = os.Getenv("CLIENT_CONFIG")
	}
	cfg := defaults()
	if path != "" {
		body, err := os.ReadFile(path)
		if err != nil {
			return Config{}, fmt.Errorf("read config: %w", err)
		}
		var file fileConfig
		if err := yaml.Unmarshal(body, &file); err != nil {
			return Config{}, fmt.Errorf("parse config: %w", err)
		}
		if err := file.apply(&cfg); err != nil {
			return Config{}, err
		}
	}
	if err := applyEnv(&cfg); err != nil {
		return Config{}, err
	}
	if err := cfg.Validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func defaults() Config {
	return Config{
		Transport:          "http",
		ServerURL:          "http://127.0.0.1:8080/mcp",
		Model:              "grok-4.6",
		BaseURL:            "https://api.x.ai/v1",
		RequestTimeout:     60 * time.Second,
		MaxToolRounds:      8,
		MaxHistoryMessages: 20,
		MaxToolResultBytes: 16 * 1024,
		LogLevel:           "info",
	}
}

func (f fileConfig) apply(cfg *Config) error {
	if f.Transport != "" {
		cfg.Transport = f.Transport
	}
	if f.ServerURL != "" {
		cfg.ServerURL = f.ServerURL
	}
	if len(f.ServerCommand) > 0 {
		cfg.ServerCommand = append([]string(nil), f.ServerCommand...)
	}
	if f.Model != "" {
		cfg.Model = f.Model
	}
	if f.BaseURL != "" {
		cfg.BaseURL = f.BaseURL
	}
	if f.LogLevel != "" {
		cfg.LogLevel = f.LogLevel
	}
	if f.RequestTimeout != "" {
		d, err := time.ParseDuration(f.RequestTimeout)
		if err != nil {
			return fmt.Errorf("request_timeout: %w", err)
		}
		cfg.RequestTimeout = d
	}
	if f.MaxToolRounds != nil {
		cfg.MaxToolRounds = *f.MaxToolRounds
	}
	if f.MaxHistoryMessages != nil {
		cfg.MaxHistoryMessages = *f.MaxHistoryMessages
	}
	if f.MaxToolResultBytes != nil {
		cfg.MaxToolResultBytes = *f.MaxToolResultBytes
	}
	return nil
}

func applyEnv(cfg *Config) error {
	set := func(key string, dest *string) {
		if v, ok := os.LookupEnv(key); ok && strings.TrimSpace(v) != "" {
			*dest = v
		}
	}
	set("MCP_TRANSPORT", &cfg.Transport)
	set("MCP_URL", &cfg.ServerURL)
	set("MCP_AUTH_TOKEN", &cfg.AuthToken)
	set("XAI_API_KEY", &cfg.APIKey)
	set("XAI_MODEL", &cfg.Model)
	set("XAI_BASE_URL", &cfg.BaseURL)
	set("CLIENT_LOG_LEVEL", &cfg.LogLevel)
	if v, ok := os.LookupEnv("MCP_SERVER_COMMAND"); ok && strings.TrimSpace(v) != "" {
		cfg.ServerCommand = strings.Fields(v)
	}
	if v, ok := os.LookupEnv("CLIENT_REQUEST_TIMEOUT"); ok && strings.TrimSpace(v) != "" {
		d, err := time.ParseDuration(v)
		if err != nil {
			return fmt.Errorf("CLIENT_REQUEST_TIMEOUT: %w", err)
		}
		cfg.RequestTimeout = d
	}
	setInt := func(key string, dest *int) error {
		v, ok := os.LookupEnv(key)
		if !ok || strings.TrimSpace(v) == "" {
			return nil
		}
		n, err := strconv.Atoi(v)
		if err != nil {
			return fmt.Errorf("%s: %w", key, err)
		}
		*dest = n
		return nil
	}
	if err := setInt("CLIENT_MAX_TOOL_ROUNDS", &cfg.MaxToolRounds); err != nil {
		return err
	}
	if err := setInt("CLIENT_MAX_HISTORY_MESSAGES", &cfg.MaxHistoryMessages); err != nil {
		return err
	}
	return setInt("CLIENT_MAX_TOOL_RESULT_BYTES", &cfg.MaxToolResultBytes)
}

// Validate checks transport and limit settings. The API key is optional here so
// tests can connect to MCP without calling the model.
func (c Config) Validate() error {
	switch c.Transport {
	case "http":
		u, err := url.Parse(c.ServerURL)
		if err != nil || u.Scheme == "" || u.Host == "" {
			return fmt.Errorf("server_url must be an absolute URL")
		}
		if u.Scheme != "http" && u.Scheme != "https" {
			return fmt.Errorf("server_url scheme must be http or https")
		}
		if strings.TrimSpace(c.AuthToken) == "" {
			return fmt.Errorf("MCP_AUTH_TOKEN is required for HTTP")
		}
	case "stdio":
		if len(c.ServerCommand) == 0 || strings.TrimSpace(c.ServerCommand[0]) == "" {
			return fmt.Errorf("server_command is required for stdio")
		}
	default:
		return fmt.Errorf("transport must be http or stdio")
	}
	if strings.TrimSpace(c.Model) == "" {
		return fmt.Errorf("model is required")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" {
		return fmt.Errorf("base_url must be an absolute http(s) URL")
	}
	if c.RequestTimeout <= 0 {
		return fmt.Errorf("request_timeout must be positive")
	}
	if c.MaxToolRounds < 1 {
		return fmt.Errorf("max_tool_rounds must be at least 1")
	}
	if c.MaxHistoryMessages < 1 {
		return fmt.Errorf("max_history_messages must be at least 1")
	}
	if c.MaxToolResultBytes < 256 {
		return fmt.Errorf("max_tool_result_bytes must be at least 256")
	}
	return nil
}
