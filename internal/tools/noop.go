package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// Noop implements the noop terminal tool.
type Noop struct {
	logger *slog.Logger
}

// NewNoop builds a noop tool. logger records the no-op reason.
func NewNoop(logger *slog.Logger) *Noop {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Noop{logger: logger}
}

func (t *Noop) Name() string   { return "noop" }
func (t *Noop) Terminal() bool { return true }

// Run logs the reason and ends the review loop with no GitHub side effect.
func (t *Noop) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	var p struct {
		Reason string `json:"reason"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parsing noop args: %w", err)
	}
	t.logger.Info("noop", "tool", t.Name(), "reason", p.Reason, "dry_run", dryRun)
	return "noop", nil
}
