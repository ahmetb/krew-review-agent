// Package server implements the production HTTP server that receives Pub/Sub
// push deliveries (or raw GitHub events) on POST /pubsub and dispatches each
// pull_request event to the agent orchestration loop.
//
// The server is the Cloud Run entrypoint: it listens on $PORT, handles
// SIGTERM-driven graceful shutdown, and translates review outcomes into HTTP
// status codes following the Pub/Sub ACK/retry contract (AGENT_CLI.md §4).
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"

	"github.com/ahmetb/krew-review-agent/internal/agent"
	"github.com/ahmetb/krew-review-agent/internal/githubclient"
	"github.com/ahmetb/krew-review-agent/internal/llm"
	"github.com/ahmetb/krew-review-agent/internal/log"
	"github.com/ahmetb/krew-review-agent/internal/tools"
)

// DefaultShutdownTimeout is the maximum time to wait for in-flight reviews to
// drain during graceful shutdown (AGENT_CLI.md §4.6).
const DefaultShutdownTimeout = 2 * time.Minute

// Deps bundles the shared dependencies used to build a per-review agent. The
// same Deps value is reused across requests; per-request state (ReviewContext,
// logger) is supplied to BuildAgent.
type Deps struct {
	LLM           llm.Client
	GH            *githubclient.Client
	Clone         *tools.Clone
	SystemPrompt  string
	MaxIterations int
	Logger        *slog.Logger
	Stdout        io.Writer
}

// BuildAgent constructs an agent for a single review.
func (d Deps) BuildAgent(rc tools.ReviewContext, dryRun bool, logger *slog.Logger) *agent.Agent {
	td := tools.Deps{GH: d.GH, Clone: d.Clone, Stdout: d.Stdout, Logger: logger}
	return agent.New(agent.Config{
		LLM:           d.LLM,
		Tools:         td.BuildRegistry(rc),
		SystemPrompt:  d.SystemPrompt,
		MaxIterations: d.MaxIterations,
		RC:            rc,
		DryRun:        dryRun,
		Logger:        logger,
	})
}

// Option configures a Server.
type Option func(*Server)

// WithShutdownTimeout overrides the graceful-shutdown drain timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}

// Server is the production HTTP server.
type Server struct {
	deps            Deps
	addr            string
	shutdownTimeout time.Duration
	logger          *slog.Logger
	httpServer      *http.Server
}

// New creates a server bound to addr with the given dependencies.
func New(deps Deps, addr string, opts ...Option) *Server {
	s := &Server{
		deps:            deps,
		addr:            addr,
		shutdownTimeout: DefaultShutdownTimeout,
		logger:          deps.Logger,
	}
	for _, o := range opts {
		o(s)
	}
	if s.logger == nil {
		s.logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return s
}

// Handler returns the HTTP handler used by the server. Exposed for testing.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/pubsub", s.handlePubSub)
	return mux
}

// handlePubSub processes a single Pub/Sub push delivery.
//
// TODO(#4.7): v1 does not verify the authenticity of the incoming request
// (Pub/Sub OIDC token or shared-secret bearer). Cloud Run ingress must be
// restricted (internal-only or IAM Invoker) as an interim measure. See
// design/AGENT_CLI.md §4.7.
func (s *Server) handlePubSub(w http.ResponseWriter, r *http.Request) {
	traceID := log.NewTraceID()
	logger := log.WithTraceID(s.logger, traceID)

	if r.Method != http.MethodPost {
		logger.Info("rejecting non-POST request", "method", r.Method, "path", r.URL.Path)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Error("reading request body", "error", err)
		// Malformed/unreadable payload: ACK (200) to avoid a poison retry loop.
		w.WriteHeader(http.StatusOK)
		return
	}

	rawEvent, err := ParseRequestBody(body)
	if err != nil {
		logger.Info("malformed payload, acking", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}

	eventType := githubclient.DetectEventType(r.Header.Get("X-GitHub-Event"), rawEvent)
	logger = logger.With("event_type", eventType)
	if eventType != githubclient.EventPullRequest {
		logger.Info("non-pull_request event, acking")
		w.WriteHeader(http.StatusOK)
		return
	}

	prEvent, err := githubclient.ParsePullRequestEvent(rawEvent)
	if err != nil {
		logger.Info("unparseable pull_request payload, acking", "error", err)
		w.WriteHeader(http.StatusOK)
		return
	}
	logger = logger.With("pr", prEvent.PRRef())

	rc := agent.ReviewContextFromEvent(prEvent)
	ag := s.deps.BuildAgent(rc, false /* dryRun */, logger)
	outcome, err := ag.Run(r.Context())
	if err != nil {
		logger.Error("review failed", "outcome", outcome.String(), "error", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	logger.Info("review completed", "outcome", outcome.String(), "dry_run", false)
	w.WriteHeader(http.StatusOK)
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled
// (e.g. by SIGTERM) or the server stops. On ctx cancellation it drains
// in-flight reviews for up to the shutdown timeout, then returns.
func (s *Server) ListenAndServe(ctx context.Context) error {
	srv := &http.Server{
		Addr:              s.addr,
		Handler:           s.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	s.httpServer = srv

	logger := s.logger
	logger.Info("http server starting", "addr", s.addr)

	errCh := make(chan error, 1)
	go func() {
		err := srv.ListenAndServe()
		if err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
			return
		}
		errCh <- nil
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received; draining", "timeout", s.shutdownTimeout)
		shutdownCtx, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()
		if err := srv.Shutdown(shutdownCtx); err != nil {
			logger.Warn("graceful shutdown did not complete; in-flight reviews abandoned", "error", err)
			return fmt.Errorf("shutdown: %w", err)
		}
		logger.Info("shutdown complete")
		return nil
	case err := <-errCh:
		return err
	}
}

// HTTPServer returns the underlying *http.Server once listening has started
// (nil before ListenAndServe). Primarily for tests.
func (s *Server) HTTPServer() *http.Server {
	return s.httpServer
}
