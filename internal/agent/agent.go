// Package agent implements the agentic orchestration loop: it drives the LLM,
// routes its responses to tools, and applies the circuit breaker. The loop is
// described in AGENT_CLI.md §7 and HIGH_LEVEL_DESIGN.md §5.
//
// An Agent is single-use: Run executes one complete review. Concurrent use is
// not supported; each request constructs its own Agent.
package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"

	"github.com/ahmetb/krew-review-agent/internal/llm"
	"github.com/ahmetb/krew-review-agent/internal/tools"
)

// Outcome classifies how a review ended. Callers (the HTTP server in
// production, the CLI in test mode) interpret Outcome together with the
// returned error to pick an HTTP status or exit code.
type Outcome int

const (
	// OutcomeReviewSubmitted: submit_review_comment executed successfully
	// (posted in production, intercepted in dry-run).
	OutcomeReviewSubmitted Outcome = iota

	// OutcomeNoop: noop executed successfully (no comment posted).
	OutcomeNoop

	// OutcomeFallback: the circuit breaker fired and a fallback comment was
	// submitted (posted in production, intercepted in dry-run). In production
	// this maps to HTTP 200; in test mode this is a non-zero exit.
	OutcomeFallback

	// OutcomeError: an error aborted the review (transient infra failure or a
	// failed fallback post). Always returned with a non-nil error.
	OutcomeError
)

// Config configures a single review run.
type Config struct {
	// LLM is the chat-completions client.
	LLM llm.Client

	// Tools is the per-review tool registry.
	Tools *tools.Registry

	// SystemPrompt is the embedded system prompt content.
	SystemPrompt string

	// MaxIterations is the circuit-breaker limit.
	MaxIterations int

	// RC is the pull request context.
	RC tools.ReviewContext

	// DryRun is true in test mode (intercepts side-effecting tools).
	DryRun bool

	// Logger is the per-request logger (already carrying trace_id).
	Logger *slog.Logger

	// FallbackBody is the comment posted when the circuit breaker fires and the
	// LLM still fails to call a terminal tool. Defaults to FallbackCommentBody
	// when empty.
	FallbackBody string
}

// Agent runs a single review.
type Agent struct {
	cfg     Config
	logger  *slog.Logger
	fallback string
}

// New creates an Agent for one review.
func New(cfg Config) *Agent {
	if cfg.MaxIterations < 1 {
		cfg.MaxIterations = 1
	}
	if cfg.FallbackBody == "" {
		cfg.FallbackBody = FallbackCommentBody
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &Agent{cfg: cfg, logger: cfg.Logger.With("pr", cfg.PRRef()), fallback: cfg.FallbackBody}
}

// PRRef returns the "{owner}/{repo}#{number}" reference for the review.
func (c Config) PRRef() string {
	return fmt.Sprintf("%s/%s#%d", c.RC.Owner, c.RC.Repo, c.RC.PRNumber)
}

// Run executes the orchestration loop and returns the outcome.
func (a *Agent) Run(ctx context.Context) (Outcome, error) {
	hist := NewMessageHistory()
	hist.AddSystem(a.cfg.SystemPrompt)
	hist.AddUser(a.buildUserPrompt())

	a.logger.Info("agent review started",
		"dry_run", a.cfg.DryRun,
		"max_iterations", a.cfg.MaxIterations,
		"title", a.cfg.RC.Title,
		"author", a.cfg.RC.Author,
		"action", a.cfg.RC.Action,
	)

	for iter := 1; iter <= a.cfg.MaxIterations; iter++ {
		resp, err := a.llmInfer(ctx, hist, iter)
		if err != nil {
			return OutcomeError, err
		}

		// Case C: conversational text, no tool call.
		if len(resp.ToolCalls) == 0 {
			hist.AddAssistantText(resp.Content)
			hist.AddUser(WarningNoToolCall)
			a.logger.Warn("llm returned no tool call", "iteration", iter, "content_len", len(resp.Content))
			continue
		}

		// Record the assistant's tool-call request in history.
		hist.AddAssistantToolCalls(resp.Content, resp.ToolCalls)

		// Process tool calls. A terminal tool ends the loop.
		for _, tc := range resp.ToolCalls {
			tool, ok := a.cfg.Tools.Get(tc.Name)
			if !ok {
				hist.AddTool(tc.ID, fmt.Sprintf("error: unknown tool %q", tc.Name))
				a.logger.Warn("unknown tool called", "iteration", iter, "tool", tc.Name)
				continue
			}
			result, err := a.runTool(ctx, tool, tc, iter)
			if err != nil {
				hist.AddTool(tc.ID, "error: "+err.Error())
				return OutcomeError, fmt.Errorf("tool %q (iter %d): %w", tc.Name, iter, err)
			}
			hist.AddTool(tc.ID, result)
			if tool.Terminal() {
				outcome := OutcomeReviewSubmitted
				if tc.Name == llm.ToolNoop {
					outcome = OutcomeNoop
				}
				a.logger.Info("review completed via terminal tool",
					"iteration", iter, "tool", tc.Name, "outcome", outcomeString(outcome))
				return outcome, nil
			}
		}
	}

	// Circuit breaker: exhausted all iterations.
	return a.circuitBreak(ctx, hist)
}

// circuitBreak appends the circuit-breaker prompt, makes a final inference,
// and either honors a terminal tool or posts the fallback comment.
func (a *Agent) circuitBreak(ctx context.Context, hist *MessageHistory) (Outcome, error) {
	a.logger.Warn("circuit breaker reached", "max_iterations", a.cfg.MaxIterations)
	hist.AddUser(CircuitBreakerPrompt)

	resp, err := a.llmInfer(ctx, hist, a.cfg.MaxIterations+1)
	if err != nil {
		return OutcomeError, fmt.Errorf("circuit breaker inference: %w", err)
	}

	if len(resp.ToolCalls) > 0 {
		hist.AddAssistantToolCalls(resp.Content, resp.ToolCalls)
		for _, tc := range resp.ToolCalls {
			tool, ok := a.cfg.Tools.Get(tc.Name)
			if !ok || !tool.Terminal() {
				continue
			}
			if _, err := a.runTool(ctx, tool, tc, a.cfg.MaxIterations+1); err != nil {
				return OutcomeError, fmt.Errorf("circuit breaker terminal tool %q: %w", tc.Name, err)
			}
			outcome := OutcomeReviewSubmitted
			if tc.Name == llm.ToolNoop {
				outcome = OutcomeNoop
			}
			a.logger.Info("review completed via terminal tool after circuit breaker", "tool", tc.Name)
			return outcome, nil
		}
	}

	// Fallback: post the generic comment via submit_review_comment.
	a.logger.Warn("llm failed to terminate; posting fallback comment")
	submit, ok := a.cfg.Tools.Get(llm.ToolSubmitReviewComment)
	if !ok {
		return OutcomeError, errors.New("submit_review_comment tool not registered")
	}
	args, err := json.Marshal(map[string]string{"body": a.fallback})
	if err != nil {
		return OutcomeError, fmt.Errorf("encoding fallback body: %w", err)
	}
	if _, err := submit.Run(ctx, string(args), a.cfg.DryRun); err != nil {
		return OutcomeError, fmt.Errorf("posting fallback comment: %w", err)
	}
	return OutcomeFallback, nil
}

// llmInfer calls the LLM and logs the request/response metadata.
func (a *Agent) llmInfer(ctx context.Context, hist *MessageHistory, iter int) (llm.Response, error) {
	a.logger.Debug("llm inference", "iteration", iter, "messages", hist.Len())
	resp, err := a.cfg.LLM.Complete(ctx, hist.Messages())
	if err != nil {
		a.logger.Error("llm inference failed", "iteration", iter, "error", err)
		return llm.Response{}, fmt.Errorf("llm inference (iter %d): %w", iter, err)
	}
	a.logger.Info("llm inference complete",
		"iteration", iter,
		"finish_reason", resp.FinishReason,
		"tool_calls", len(resp.ToolCalls),
		"content_len", len(resp.Content),
	)
	return resp, nil
}

// runTool executes a tool with structured logging.
func (a *Agent) runTool(ctx context.Context, tool tools.Tool, tc llm.ToolCall, iter int) (string, error) {
	log := a.logger.With(
		"iteration", iter,
		"tool", tc.Name,
		"tool_args", scrubArgs(tc.Name, tc.Arguments),
		"dry_run", a.cfg.DryRun,
	)
	log.Info("tool call start")
	result, err := tool.Run(ctx, tc.Arguments, a.cfg.DryRun)
	if err != nil {
		log.Error("tool call error", "tool_status", "error", "error", err)
		return "", err
	}
	log.Info("tool call ok", "tool_status", "ok", "result_len", len(result))
	return result, nil
}

// buildUserPrompt constructs the initial user message describing the PR.
func (a *Agent) buildUserPrompt() string {
	rc := a.cfg.RC
	body := rc.Body
	if strings.TrimSpace(body) == "" {
		body = "(no description provided)"
	}
	return fmt.Sprintf("Please review pull request #%d in %s/%s.\n\nTitle: %s\nAuthor: %s\nAction: %s\n\nPR body:\n%s",
		rc.PRNumber, rc.Owner, rc.Repo, rc.Title, rc.Author, rc.Action, body)
}

// scrubArgs redacts or truncates tool arguments for logging.
func scrubArgs(name, args string) string {
	if name == llm.ToolSubmitReviewComment {
		return "(body omitted; logged separately)"
	}
	const max = 500
	if len(args) > max {
		return args[:max] + "...(truncated)"
	}
	return args
}

// outcomeString returns a human-readable outcome name for logging.
func outcomeString(o Outcome) string { return o.String() }

// String returns a human-readable outcome name.
func (o Outcome) String() string {
	switch o {
	case OutcomeReviewSubmitted:
		return "review_submitted"
	case OutcomeNoop:
		return "noop"
	case OutcomeFallback:
		return "fallback"
	case OutcomeError:
		return "error"
	default:
		return "unknown"
	}
}
