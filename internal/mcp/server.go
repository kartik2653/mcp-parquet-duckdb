package mcp

import (
	"log/slog"
	"net/http"

	"github.com/Intellicars/intellicarai/internal/analytics"
	"github.com/Intellicars/intellicarai/internal/security"
	sdkmcp "github.com/modelcontextprotocol/go-sdk/mcp"
)

const version = "1.0.0"

// New builds an MCP server with the analytics tools.
func New(engine *analytics.Engine, logger *slog.Logger) *sdkmcp.Server {
	if logger == nil {
		logger = slog.Default()
	}
	server := sdkmcp.NewServer(&sdkmcp.Implementation{
		Name:    "analytics",
		Version: version,
	}, &sdkmcp.ServerOptions{Logger: logger})
	registerTools(server, engine, logger)
	return server
}

// HTTPHandler serves the MCP streamable HTTP transport.
func HTTPHandler(server *sdkmcp.Server, logger *slog.Logger, maxBody int64) http.Handler {
	return sdkmcp.NewStreamableHTTPHandler(func(*http.Request) *sdkmcp.Server {
		return server
	}, &sdkmcp.StreamableHTTPOptions{
		Stateless:                    true,
		JSONResponse:                 true,
		Logger:                       logger,
		MaxRequestBodyBytes:          maxBody,
		PropagateRequestCancellation: true,
	})
}

// ProtectedHandler requires a bearer token.
func ProtectedHandler(token string, next http.Handler) http.Handler {
	return security.Bearer(token, next)
}
