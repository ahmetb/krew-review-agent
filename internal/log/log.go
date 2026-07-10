// Package log provides structured logging setup for the krew-review-agent.
//
// The package avoids global state: callers construct a *slog.Logger via New and
// pass it explicitly to the components that need it. Per-request loggers are
// produced by WithTraceID, which attaches a trace_id attribute used to correlate
// all log lines within a single review.
package log

import (
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/google/uuid"
)

// ParseLevel converts a case-insensitive level string (DEBUG, INFO, WARN, ERROR)
// into a slog.Level. Unknown values default to INFO and are reported via the
// returned error so callers can warn at startup.
func ParseLevel(s string) (slog.Level, error) {
	switch strings.ToUpper(strings.TrimSpace(s)) {
	case "", "INFO":
		return slog.LevelInfo, nil
	case "DEBUG":
		return slog.LevelDebug, nil
	case "WARN", "WARNING":
		return slog.LevelWarn, nil
	case "ERROR":
		return slog.LevelError, nil
	default:
		return slog.LevelInfo, fmt.Errorf("unknown log level %q, defaulting to INFO", s)
	}
}

// New creates a JSON-encoded slog.Logger writing to w at the given level.
func New(level slog.Level, w io.Writer) *slog.Logger {
	h := slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level: level,
	})
	return slog.New(h)
}

// NewTraceID returns a fresh random UUID v4 string used as a per-request
// correlation identifier.
func NewTraceID() string {
	return uuid.NewString()
}

// WithTraceID returns a child logger that always records the given trace_id on
// every emitted record.
func WithTraceID(logger *slog.Logger, traceID string) *slog.Logger {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return logger.With("trace_id", traceID)
}
