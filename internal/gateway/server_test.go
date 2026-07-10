package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/ahmetb/krew-review-agent/internal/log"
)

// fakePublisher is a test double for the Publisher interface. It records the
// last Publish call and can be configured to return an error.
type fakePublisher struct {
	mu     sync.Mutex
	calls  []publishCall
	failOn int // 1-based call number that should fail; 0 = never
	err    error
}

type publishCall struct {
	Data       []byte
	Attributes map[string]string
}

func (f *fakePublisher) Publish(_ context.Context, data []byte, attrs map[string]string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, publishCall{Data: data, Attributes: attrs})
	if f.failOn > 0 && len(f.calls) == f.failOn {
		return "", f.err
	}
	return "msg-id-fake", nil
}

func (f *fakePublisher) Close() error { return nil }

func (f *fakePublisher) lastCall() publishCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return publishCall{}
	}
	return f.calls[len(f.calls)-1]
}

func (f *fakePublisher) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func testDeps(t *testing.T, pub *fakePublisher, disableVerification bool) Deps {
	t.Helper()
	return Deps{
		Secret:              []byte("test-secret"),
		DisableVerification: disableVerification,
		AllowedRepo:         AllowedRepository,
		Publisher:           pub,
		Logger:              log.New(slog.LevelInfo, io.Discard),
	}
}

// validPROpenedBody is a minimal valid pull_request:opened payload from the
// allowed repository.
const validPROpenedBody = `{
	"action": "opened",
	"repository": {"full_name": "kubernetes-sigs/krew-index"},
	"pull_request": {"number": 7}
}`

func signBody(secret, body string) string {
	return validSignature([]byte(secret), []byte(body))
}

func doRequest(t *testing.T, s *Server, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func TestValidPROpenedPublishedReturns200(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	headers := map[string]string{
		"X-GitHub-Event":    "pull_request",
		"X-GitHub-Delivery": "del-123",
		"X-Hub-Signature-256": signBody("test-secret", validPROpenedBody),
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody, headers)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if pub.callCount() != 1 {
		t.Errorf("publish calls=%d want 1", pub.callCount())
	}
}

func TestPublishReceivesCorrectDataAndAttributes(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	headers := map[string]string{
		"X-GitHub-Event":    "pull_request",
		"X-GitHub-Delivery": "del-456",
		"X-Hub-Signature-256": signBody("test-secret", validPROpenedBody),
	}
	doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody, headers)

	call := pub.lastCall()
	if string(call.Data) != validPROpenedBody {
		t.Errorf("published data does not match raw body")
	}
	if call.Attributes["X-GitHub-Event"] != "pull_request" {
		t.Errorf("attr X-GitHub-Event=%q want pull_request", call.Attributes["X-GitHub-Event"])
	}
	if call.Attributes["X-GitHub-Delivery"] != "del-456" {
		t.Errorf("attr X-GitHub-Delivery=%q want del-456", call.Attributes["X-GitHub-Delivery"])
	}
	if call.Attributes["github-action"] != "opened" {
		t.Errorf("attr github-action=%q want opened", call.Attributes["github-action"])
	}
}

func TestNonPullRequestEventReturns202(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	body := `{"action":"opened","repository":{"full_name":"kubernetes-sigs/krew-index"}}`
	headers := map[string]string{
		"X-GitHub-Event":      "push",
		"X-Hub-Signature-256": signBody("test-secret", body),
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", body, headers)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", rr.Code)
	}
	if pub.callCount() != 0 {
		t.Errorf("should not publish for non-pull_request event; calls=%d", pub.callCount())
	}
}

func TestNonOpenedActionReturns202(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	body := `{
		"action": "closed",
		"repository": {"full_name": "kubernetes-sigs/krew-index"},
		"pull_request": {"number": 1}
	}`
	headers := map[string]string{
		"X-GitHub-Event":      "pull_request",
		"X-Hub-Signature-256": signBody("test-secret", body),
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", body, headers)
	if rr.Code != http.StatusAccepted {
		t.Fatalf("status=%d want 202", rr.Code)
	}
	if pub.callCount() != 0 {
		t.Errorf("should not publish for non-opened action; calls=%d", pub.callCount())
	}
}

func TestMissingSignatureReturns401(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	headers := map[string]string{
		"X-GitHub-Event": "pull_request",
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody, headers)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rr.Code)
	}
	if pub.callCount() != 0 {
		t.Errorf("should not publish when unauthorized; calls=%d", pub.callCount())
	}
}

func TestWrongSignatureReturns401(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	headers := map[string]string{
		"X-GitHub-Event":      "pull_request",
		"X-Hub-Signature-256": "sha256=deadbeef",
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody, headers)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status=%d want 401", rr.Code)
	}
}

func TestMalformedPayloadReturns400(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	body := `{not valid json`
	headers := map[string]string{
		"X-GitHub-Event":      "pull_request",
		"X-Hub-Signature-256": signBody("test-secret", body),
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", body, headers)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status=%d want 400", rr.Code)
	}
	if pub.callCount() != 0 {
		t.Errorf("should not publish for malformed payload; calls=%d", pub.callCount())
	}
}

func TestWrongRepositoryReturns403(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	body := `{
		"action": "opened",
		"repository": {"full_name": "some-other/repo"},
		"pull_request": {"number": 1}
	}`
	headers := map[string]string{
		"X-GitHub-Event":      "pull_request",
		"X-Hub-Signature-256": signBody("test-secret", body),
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", body, headers)
	if rr.Code != http.StatusForbidden {
		t.Fatalf("status=%d want 403", rr.Code)
	}
	if pub.callCount() != 0 {
		t.Errorf("should not publish for wrong repo; calls=%d", pub.callCount())
	}
}

func TestPublishFailureReturns500(t *testing.T) {
	pub := &fakePublisher{failOn: 1, err: errors.New("transient error")}
	s := New(testDeps(t, pub, false), ":0")

	headers := map[string]string{
		"X-GitHub-Event":      "pull_request",
		"X-Hub-Signature-256": signBody("test-secret", validPROpenedBody),
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody, headers)
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500", rr.Code)
	}
	if pub.callCount() != 1 {
		t.Errorf("publish should have been attempted; calls=%d", pub.callCount())
	}
}

func TestNonPOSTReturns405(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, false), ":0")

	rr := doRequest(t, s, http.MethodGet, "/webhook", "", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", rr.Code)
	}
}

func TestVerificationDisabledSkipsSignature(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, true), ":0")

	headers := map[string]string{
		"X-GitHub-Event": "pull_request",
		// No X-Hub-Signature-256 header.
	}
	rr := doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody, headers)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (verification disabled)", rr.Code)
	}
	if pub.callCount() != 1 {
		t.Errorf("publish calls=%d want 1", pub.callCount())
	}
}

func TestWrongPathReturns404(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, true), ":0")

	rr := doRequest(t, s, http.MethodPost, "/other", validPROpenedBody, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr.Code)
	}
}

func TestNilLoggerDoesNotPanic(t *testing.T) {
	pub := &fakePublisher{}
	deps := Deps{
		Secret:              []byte("s"),
		DisableVerification: true,
		AllowedRepo:         AllowedRepository,
		Publisher:           pub,
		Logger:              nil,
	}
	s := New(deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody,
		map[string]string{"X-GitHub-Event": "pull_request"})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
}

// TestOutcomeLogged verifies that the structured log output contains the
// expected outcome field for a published event.
func TestOutcomeLogged(t *testing.T) {
	var buf strings.Builder
	pub := &fakePublisher{}
	deps := Deps{
		Secret:              []byte("s"),
		DisableVerification: true,
		AllowedRepo:         AllowedRepository,
		Publisher:           pub,
		Logger:              log.New(slog.LevelInfo, &buf),
	}
	s := New(deps, ":0")
	doRequest(t, s, http.MethodPost, "/webhook", validPROpenedBody,
		map[string]string{"X-GitHub-Event": "pull_request", "X-GitHub-Delivery": "d-1"})

	var rec map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &rec); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["outcome"] != string(outcomePublished) {
		t.Errorf("outcome=%v want %q", rec["outcome"], outcomePublished)
	}
	if rec["delivery_id"] != "d-1" {
		t.Errorf("delivery_id=%v want d-1", rec["delivery_id"])
	}
	if rec["event_type"] != "pull_request" {
		t.Errorf("event_type=%v want pull_request", rec["event_type"])
	}
	if rec["repo"] != AllowedRepository {
		t.Errorf("repo=%v want %q", rec["repo"], AllowedRepository)
	}
	if rec["pr_number"] != float64(7) {
		t.Errorf("pr_number=%v want 7", rec["pr_number"])
	}
	if _, ok := rec["publish_latency_ms"]; !ok {
		t.Errorf("publish_latency_ms missing from log: %v", rec)
	}
}

func TestListenAndServeShutdownOnCtxCancel(t *testing.T) {
	pub := &fakePublisher{}
	s := New(testDeps(t, pub, true), "127.0.0.1:0", WithShutdownTimeout(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx) }()

	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("ListenAndServe returned error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("ListenAndServe did not return after ctx cancel")
	}
}
