package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// DefaultShutdownTimeout is the maximum time to wait for in-flight requests to
// drain during graceful shutdown.
const DefaultShutdownTimeout = 30 * time.Second

// AllowedRepository is re-exported here for convenience; the canonical
// definition lives in config.go.

// outcome values for the structured log "outcome" field (EVENT_GATEWAY.md §8).
type outcome string

const (
	outcomePublished     outcome = "published"
	outcomeFiltered      outcome = "filtered"
	outcomeUnauthorized  outcome = "unauthorized"
	outcomeForbidden     outcome = "forbidden"
	outcomeBadRequest    outcome = "bad_request"
	outcomePublishFailed outcome = "publish_failed"
)

// Deps bundles the dependencies required to construct the gateway Server.
type Deps struct {
	// Secret is the HMAC-SHA256 key used for signature verification. Ignored
	// when DisableVerification is true.
	Secret []byte

	// DisableVerification skips HMAC signature verification. Intended for
	// local development only (EVENT_GATEWAY.md §3.2).
	DisableVerification bool

	// AllowedRepo is the single repository allowed by the gateway. Payloads
	// whose repository.full_name does not match are rejected with 403.
	AllowedRepo string

	// Publisher publishes filtered webhook payloads to Pub/Sub.
	Publisher Publisher

	// Logger receives structured log lines. If nil, a discard logger is used.
	Logger *slog.Logger
}

// Option configures a Server.
type Option func(*Server)

// WithShutdownTimeout overrides the graceful-shutdown drain timeout.
func WithShutdownTimeout(d time.Duration) Option {
	return func(s *Server) { s.shutdownTimeout = d }
}

// Server is the Event Gateway HTTP server.
type Server struct {
	deps            Deps
	addr            string
	shutdownTimeout time.Duration
	logger          *slog.Logger
	httpServer      *http.Server
}

// New creates a gateway server bound to addr with the given dependencies.
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
	mux.HandleFunc("/webhook", s.handleWebhook)
	return mux
}

// handleWebhook processes a single GitHub webhook delivery following the
// 7-step flow described in EVENT_GATEWAY.md §4.
func (s *Server) handleWebhook(w http.ResponseWriter, r *http.Request) {
	deliveryID := r.Header.Get("X-GitHub-Delivery")
	eventType := r.Header.Get("X-GitHub-Event")
	logger := s.logger.With("delivery_id", deliveryID, "event_type", eventType)

	if r.Method != http.MethodPost {
		logger.Info("rejecting non-POST request", "method", r.Method)
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}

	// Step 1: Read raw body. The raw bytes are required for HMAC verification
	// before any JSON parsing (EVENT_GATEWAY.md §4 Step 1).
	body, err := io.ReadAll(r.Body)
	if err != nil {
		logger.Info("failed to read request body",
			"outcome", string(outcomeBadRequest), "error", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	// Step 2: Signature verification (EVENT_GATEWAY.md §4 Step 2).
	if !s.deps.DisableVerification {
		sig := r.Header.Get("X-Hub-Signature-256")
		if !VerifySignature(s.deps.Secret, body, sig) {
			logger.Info("signature verification failed",
				"outcome", string(outcomeUnauthorized))
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
	}

	// Step 3: Event type filter (EVENT_GATEWAY.md §4 Step 3).
	if eventType != "pull_request" {
		logger.Info("filtered: non-pull_request event",
			"outcome", string(outcomeFiltered))
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Step 4: Parse payload (EVENT_GATEWAY.md §4 Step 4).
	payload, err := parseWebhookPayload(body)
	if err != nil {
		logger.Info("payload parse failure",
			"outcome", string(outcomeBadRequest), "error", err.Error())
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	logger = logger.With(
		"action", payload.Action,
		"repo", payload.Repository.FullName,
		"pr_number", payload.PullRequest.Number,
	)

	// Step 5: Action filter (EVENT_GATEWAY.md §4 Step 5).
	if payload.Action != "opened" {
		logger.Info("filtered: action is not opened",
			"outcome", string(outcomeFiltered))
		w.WriteHeader(http.StatusAccepted)
		return
	}

	// Step 6: Repository validation (EVENT_GATEWAY.md §4 Step 6).
	if payload.Repository.FullName != s.deps.AllowedRepo {
		logger.Info("forbidden: repository not allowed",
			"outcome", string(outcomeForbidden))
		w.WriteHeader(http.StatusForbidden)
		return
	}

	// Step 7: Publish to Pub/Sub (EVENT_GATEWAY.md §4 Step 7).
	attributes := map[string]string{
		"X-GitHub-Event":    eventType,
		"X-GitHub-Delivery": deliveryID,
		"github-action":     payload.Action,
	}

	start := time.Now()
	msgID, err := s.deps.Publisher.Publish(r.Context(), body, attributes)
	latencyMs := float64(time.Since(start).Microseconds()) / 1000.0

	if err != nil {
		logger.Error("publish failed",
			"outcome", string(outcomePublishFailed),
			"publish_latency_ms", latencyMs,
			"error", err.Error())
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	logger.Info("event published",
		"outcome", string(outcomePublished),
		"publish_latency_ms", latencyMs,
		"message_id", msgID)
	w.WriteHeader(http.StatusOK)
}

// ListenAndServe starts the HTTP server and blocks until ctx is cancelled
// (e.g. by SIGTERM) or the server stops. On ctx cancellation it drains
// in-flight requests for up to the shutdown timeout, then returns.
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
			logger.Warn("graceful shutdown did not complete", "error", err)
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
