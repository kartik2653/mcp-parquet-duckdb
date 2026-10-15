package mcpclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/google/jsonschema-go/jsonschema"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

// ErrUnknownTool means the model asked for a tool the server did not advertise.
var ErrUnknownTool = errors.New("tool is not available")

// ErrInvalidArgs means the arguments do not match the advertised schema.
var ErrInvalidArgs = errors.New("tool arguments are invalid")

// Tool is one tool discovered from the server.
type Tool struct {
	Name        string
	Description string
	Parameters  json.RawMessage
}

// Result is the structured tool payload returned to the model.
type Result struct {
	Body    json.RawMessage
	IsError bool
}

// Config is the subset of client settings needed to reach the server.
type Config struct {
	Transport     string
	ServerURL     string
	AuthToken     string
	ServerCommand []string
	Timeout       time.Duration
}

// Client talks to the analytics MCP server and caches discovered tools.
type Client struct {
	cfg     Config
	mu      sync.Mutex
	session *sdkmcp.ClientSession
	tools   map[string]advertised
}

type advertised struct {
	tool   Tool
	schema *jsonschema.Resolved
}

// Connect initializes a session and discovers tools.
func Connect(ctx context.Context, cfg Config) (*Client, error) {
	if cfg.Timeout <= 0 {
		cfg.Timeout = 60 * time.Second
	}
	c := &Client{cfg: cfg, tools: map[string]advertised{}}
	if err := c.dial(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

// Discover returns the tools advertised by the connected server.
func (c *Client) Discover(context.Context) ([]Tool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]Tool, 0, len(c.tools))
	for _, item := range c.tools {
		out = append(out, item.tool)
	}
	return out, nil
}

// Call invokes one discovered tool. Unknown names and invalid arguments are rejected
// before the server is contacted.
func (c *Client) Call(ctx context.Context, name string, arguments json.RawMessage) (Result, error) {
	args, err := c.prepare(name, arguments)
	if err != nil {
		return Result{}, err
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	res, err := c.call(ctx, name, args)
	if err != nil && ctx.Err() == nil {
		if recErr := c.dial(ctx); recErr == nil {
			res, err = c.call(ctx, name, args)
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			return Result{}, ctx.Err()
		}
		return Result{}, fmt.Errorf("analytics server is unavailable: %w", err)
	}
	return res, nil
}

// Close ends the session and, for stdio, the server process.
func (c *Client) Close() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.session == nil {
		return nil
	}
	err := c.session.Close()
	c.session = nil
	return err
}

func (c *Client) prepare(name string, arguments json.RawMessage) (any, error) {
	c.mu.Lock()
	item, ok := c.tools[name]
	c.mu.Unlock()
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrUnknownTool, name)
	}
	if len(bytes.TrimSpace(arguments)) == 0 {
		arguments = []byte("{}")
	}
	var decoded any
	dec := json.NewDecoder(bytes.NewReader(arguments))
	dec.UseNumber()
	if err := dec.Decode(&decoded); err != nil {
		return nil, fmt.Errorf("%w: %s", ErrInvalidArgs, err.Error())
	}
	if item.schema != nil {
		var plain any
		if err := json.Unmarshal(arguments, &plain); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidArgs, err.Error())
		}
		if err := item.schema.Validate(plain); err != nil {
			return nil, fmt.Errorf("%w: %s", ErrInvalidArgs, err.Error())
		}
	}
	return decoded, nil
}

func (c *Client) call(ctx context.Context, name string, args any) (Result, error) {
	if c.session == nil {
		return Result{}, errors.New("analytics server is unavailable")
	}
	res, err := c.session.CallTool(ctx, &sdkmcp.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		return Result{}, err
	}
	body, err := resultJSON(res)
	if err != nil {
		return Result{}, fmt.Errorf("tool result could not be read: %w", err)
	}
	return Result{Body: body, IsError: res.IsError}, nil
}

func (c *Client) dial(ctx context.Context) error {
	if c.session != nil {
		_ = c.session.Close()
		c.session = nil
	}
	transport, err := c.transport()
	if err != nil {
		return err
	}
	sdk := sdkmcp.NewClient(&sdkmcp.Implementation{Name: "analytics-client", Version: "1.0.0"}, nil)
	session, err := sdk.Connect(ctx, transport, nil)
	if err != nil {
		return fmt.Errorf("analytics server is unavailable: %w", err)
	}
	c.session = session
	if err := c.discover(ctx); err != nil {
		_ = session.Close()
		c.session = nil
		return err
	}
	return nil
}

func (c *Client) transport() (sdkmcp.Transport, error) {
	switch c.cfg.Transport {
	case "stdio":
		if len(c.cfg.ServerCommand) == 0 {
			return nil, errors.New("server command is required")
		}
		cmd := exec.Command(c.cfg.ServerCommand[0], c.cfg.ServerCommand[1:]...)
		cmd.Env = withoutSecrets(os.Environ())
		cmd.Stderr = io.Discard
		return &sdkmcp.CommandTransport{Command: cmd}, nil
	default:
		httpClient := &http.Client{
			Timeout: c.cfg.Timeout,
			Transport: bearerTransport{
				base:  http.DefaultTransport,
				token: c.cfg.AuthToken,
			},
		}
		return &sdkmcp.StreamableClientTransport{
			Endpoint:             c.cfg.ServerURL,
			HTTPClient:           httpClient,
			DisableStandaloneSSE: true,
			MaxRetries:           1,
		}, nil
	}
}

func (c *Client) discover(ctx context.Context) error {
	listed, err := c.session.ListTools(ctx, nil)
	if err != nil {
		return fmt.Errorf("tool discovery failed: %w", err)
	}
	next := make(map[string]advertised, len(listed.Tools))
	for _, tool := range listed.Tools {
		raw, err := json.Marshal(tool.InputSchema)
		if err != nil {
			return fmt.Errorf("tool %s schema: %w", tool.Name, err)
		}
		item := advertised{tool: Tool{Name: tool.Name, Description: tool.Description, Parameters: raw}}
		var schema jsonschema.Schema
		if err := json.Unmarshal(raw, &schema); err == nil {
			if resolved, err := schema.Resolve(nil); err == nil {
				item.schema = resolved
			}
		}
		next[tool.Name] = item
	}
	c.tools = next
	return nil
}

func resultJSON(res *sdkmcp.CallToolResult) ([]byte, error) {
	if res == nil {
		return []byte("null"), nil
	}
	if res.StructuredContent != nil {
		return marshalNumbers(res.StructuredContent)
	}
	var texts []string
	for _, item := range res.Content {
		if text, ok := item.(*sdkmcp.TextContent); ok && text.Text != "" {
			texts = append(texts, text.Text)
		}
	}
	switch len(texts) {
	case 0:
		return []byte("null"), nil
	case 1:
		if json.Valid([]byte(texts[0])) {
			return []byte(texts[0]), nil
		}
		return json.Marshal(texts[0])
	default:
		return json.Marshal(texts)
	}
}

func marshalNumbers(value any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return bytes.TrimSpace(buf.Bytes()), nil
}

type bearerTransport struct {
	base  http.RoundTripper
	token string
}

func (b bearerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header = req.Header.Clone()
	if b.token != "" {
		clone.Header.Set("Authorization", "Bearer "+b.token)
	}
	base := b.base
	if base == nil {
		base = http.DefaultTransport
	}
	return base.RoundTrip(clone)
}

func withoutSecrets(env []string) []string {
	out := make([]string, 0, len(env))
	for _, item := range env {
		if strings.HasPrefix(item, "XAI_API_KEY=") {
			continue
		}
		out = append(out, item)
	}
	return out
}
