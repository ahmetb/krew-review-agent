// Command agent is the entrypoint of the krew-review-agent. It runs in one of
// two modes selected by CLI flags:
//
//   - Production: a long-lived HTTP server receiving Pub/Sub push deliveries on
//     $PORT (Cloud Run contract).
//   - Test (--test-payload=FILE): a run-to-completion CLI that executes a single
//     review in dry-run mode, intercepting write-side tools.
//
// See design/AGENT_CLI.md for the full specification.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	systemprompt "github.com/ahmetb/krew-review-agent"
	"github.com/ahmetb/krew-review-agent/internal/agent"
	"github.com/ahmetb/krew-review-agent/internal/config"
	"github.com/ahmetb/krew-review-agent/internal/githubclient"
	"github.com/ahmetb/krew-review-agent/internal/llm"
	"github.com/ahmetb/krew-review-agent/internal/log"
	"github.com/ahmetb/krew-review-agent/internal/server"
	"github.com/ahmetb/krew-review-agent/internal/tools"
)

func main() {
	var (
		testPayload string
		port        int
	)
	flag.StringVar(&testPayload, "test-payload", "",
		"path to a raw GitHub event JSON file; enables dry-run test mode (run-to-completion)")
	flag.IntVar(&port, "port", 0,
		"HTTP listen port (production mode); overrides $PORT (default 8080)")
	flag.Parse()

	cfg, err := config.Load(os.LookupEnv)
	logger := newLogger(cfg.LogLevel)

	if err != nil {
		logger.Error("configuration error", "error", err)
		// LLM_API_KEY and LLM_BASE_URL are always required. GITHUB_TOKEN is
		// required in both modes (read calls happen in dry-run too).
		os.Exit(exitConfig)
	}

	if testPayload != "" {
		os.Exit(runTest(cfg, testPayload, logger))
	}
	os.Exit(runServer(cfg, port, logger))
}

// Exit code constants.
const (
	exitOK       = 0
	exitFailure  = 1
	exitConfig   = 2
	exitBadInput = 3
)

func newLogger(level string) *slog.Logger {
	lvl, err := log.ParseLevel(level)
	if err != nil {
		// Build a temporary logger to warn, then proceed with the defaulted level.
		log.New(slog.LevelInfo, os.Stderr).Error("log level", "error", err)
	}
	return log.New(lvl, os.Stderr)
}

func runServer(cfg config.Config, flagPort int, logger *slog.Logger) int {
	listenPort := cfg.Port
	if flagPort > 0 {
		listenPort = flagPort
	}
	addr := fmt.Sprintf(":%d", listenPort)

	deps := server.Deps{
		LLM:           llm.NewClient(llm.Config{APIKey: cfg.LLMAPIKey, BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel}),
		GH:            githubclient.New(cfg.GitHubToken),
		Clone:         tools.DefaultKrewIndexClone(),
		SystemPrompt:  systemprompt.Content,
		MaxIterations: cfg.MaxIterations,
		Logger:        logger,
		Stdout:        os.Stdout,
	}
	srv := server.New(deps, addr)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	logger.Info("starting production HTTP server", "addr", addr, "model", cfg.LLMModel, "max_iterations", cfg.MaxIterations)
	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("server exited with error", "error", err)
		return exitFailure
	}
	return exitOK
}

func runTest(cfg config.Config, payloadPath string, logger *slog.Logger) int {
	data, err := os.ReadFile(payloadPath)
	if err != nil {
		logger.Error("reading test payload", "path", payloadPath, "error", err)
		return exitBadInput
	}

	// --test-payload files are raw GitHub webhook events (not Pub/Sub
	// envelopes), so they bypass the HTTP body parser.
	eventType := githubclient.DetectEventType("", data)
	tLogger := logger.With("event_type", eventType)
	if eventType != githubclient.EventPullRequest {
		tLogger.Info("non-pull_request event; nothing to review")
		return exitOK
	}

	prEvent, err := githubclient.ParsePullRequestEvent(data)
	if err != nil {
		tLogger.Error("parsing pull_request event", "error", err)
		return exitBadInput
	}

	traceID := log.NewTraceID()
	rLogger := log.WithTraceID(tLogger, traceID).With("pr", prEvent.PRRef())

	deps := server.Deps{
		LLM:           llm.NewClient(llm.Config{APIKey: cfg.LLMAPIKey, BaseURL: cfg.LLMBaseURL, Model: cfg.LLMModel}),
		GH:            githubclient.New(cfg.GitHubToken),
		Clone:         tools.DefaultKrewIndexClone(),
		SystemPrompt:  systemprompt.Content,
		MaxIterations: cfg.MaxIterations,
		Logger:        rLogger,
		Stdout:        os.Stdout,
	}

	rc := agent.ReviewContextFromEvent(prEvent)
	ag := deps.BuildAgent(rc, true /* dryRun */, rLogger)

	rLogger.Info("starting test-mode review (dry-run)", "max_iterations", cfg.MaxIterations)
	outcome, err := ag.Run(context.Background())
	if err != nil {
		rLogger.Error("review failed", "outcome", outcome.String(), "error", err)
		return exitFailure
	}
	// In dry-run, a fallback outcome (even when intercepted) is a non-zero
	// exit: the agent did not complete a normal review.
	if outcome == agent.OutcomeFallback {
		rLogger.Error("review ended via fallback (dry-run)", "outcome", outcome.String())
		return exitFailure
	}
	rLogger.Info("review completed", "outcome", outcome.String(), "dry_run", true)
	return exitOK
}
