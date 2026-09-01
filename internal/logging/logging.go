// Package logging builds the application's structured logger (slog) from
// configuration and carries request-scoped loggers through context. Invalid
// configuration fails open: safe defaults with a warning, never a dead
// process (observability must not take the service down).
package logging

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"os"
	"strings"
)

type Config struct {
	Format string // text (default) | json
	Level  string // debug | info (default) | warn | error
	Output string // stdout (default) | stderr
	Source bool   // include source file:line
}

// Wrap, when set before Setup, wraps the built handler — the documented seam
// for downstream builds to attach their own exporter without forking.
var Wrap func(slog.Handler) slog.Handler

// Setup builds the logger, installs it as slog's default, and returns it.
func Setup(cfg Config) *slog.Logger {
	var warnings []string

	level := slog.LevelInfo
	switch strings.ToLower(cfg.Level) {
	case "", "info":
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		warnings = append(warnings, "unknown log level "+cfg.Level+", using info")
	}

	var out io.Writer = os.Stdout
	switch strings.ToLower(cfg.Output) {
	case "", "stdout":
	case "stderr":
		out = os.Stderr
	default:
		warnings = append(warnings, "unknown log output "+cfg.Output+", using stdout")
	}

	opts := &slog.HandlerOptions{Level: level, AddSource: cfg.Source}
	var handler slog.Handler
	switch strings.ToLower(cfg.Format) {
	case "json":
		handler = slog.NewJSONHandler(out, opts)
	case "", "text":
		handler = slog.NewTextHandler(out, opts)
	default:
		warnings = append(warnings, "unknown log format "+cfg.Format+", using text")
		handler = slog.NewTextHandler(out, opts)
	}
	if Wrap != nil {
		handler = Wrap(handler)
	}

	logger := slog.New(handler)
	slog.SetDefault(logger)
	for _, w := range warnings {
		logger.Warn(w)
	}
	return logger
}

type ctxKey struct{}

// IntoContext returns a context carrying the logger.
func IntoContext(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, ctxKey{}, logger)
}

// From returns the context's logger, or slog.Default().
func From(ctx context.Context) *slog.Logger {
	if logger, ok := ctx.Value(ctxKey{}).(*slog.Logger); ok {
		return logger
	}
	return slog.Default()
}

// NewRequestID generates a 16-hex-char correlation id.
func NewRequestID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "unknown"
	}
	return hex.EncodeToString(b[:])
}
