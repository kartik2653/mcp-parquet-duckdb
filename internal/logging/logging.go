package logging

import (
	"io"
	"log/slog"
	"strings"
)

// New writes logs to w. Callers must not pass API keys, bearer tokens, or
// authorization headers to the logger.
func New(w io.Writer, level string) *slog.Logger {
	return slog.New(slog.NewTextHandler(w, &slog.HandlerOptions{Level: parse(level)}))
}

func parse(level string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
