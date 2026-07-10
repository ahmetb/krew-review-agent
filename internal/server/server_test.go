package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ahmetb/krew-review-agent/internal/githubclient"
	"github.com/ahmetb/krew-review-agent/internal/llm"
	"github.com/ahmetb/krew-review-agent/internal/log"
	"github.com/ahmetb/krew-review-agent/internal/tools"
)

// llmCompletion builds an OpenAI-compatible chat completion that calls a tool.
func llmCompletion(toolName, args string) []byte {
	msg := map[string]any{
		"role":    "assistant",
		"content": "",
		"tool_calls": []map[string]any{
			{"id": "call_1", "type": "function", "function": map[string]any{"name": toolName, "arguments": args}},
		},
	}
	resp := map[string]any{
		"id": "x", "object": "chat.completion", "created": 1, "model": "m",
		"choices": []map[string]any{{
			"index": 0, "finish_reason": "tool_calls", "message": msg,
			"logprobs": map[string]any{"content": []any{}, "refusal": []any{}},
		}},
	}
	b, _ := json.Marshal(resp)
	return b
}

// newTestDeps builds server.Deps wired to in-process httptest servers.
type testDeps struct {
	deps    Deps
	llmSrv  *httptest.Server
	ghSrv   *httptest.Server
	llmHits *int32
	ghHits  *int32
}

func newTestDeps(t *testing.T, llmHandler http.HandlerFunc, ghHandler http.HandlerFunc) *testDeps {
	t.Helper()
	var llmHits, ghHits int32
	llmSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&llmHits, 1)
		w.Header().Set("Content-Type", "application/json")
		if llmHandler != nil {
			llmHandler(w, r)
		}
	}))
	ghSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&ghHits, 1)
		if ghHandler != nil {
			ghHandler(w, r)
		}
	}))
	t.Cleanup(llmSrv.Close)
	t.Cleanup(ghSrv.Close)

	clone := tools.CloneForTest(t.TempDir(), tools.KrewIndexURL, func(ctx context.Context, url, dir string) error {
		return mkdirPlugins(dir)
	})

	deps := Deps{
		LLM:           llm.NewClient(llm.Config{APIKey: "k", BaseURL: llmSrv.URL, Model: "m"}),
		GH:            githubclient.New("tok", githubclient.WithBaseURL(ghSrv.URL), githubclient.WithHTTPClient(ghSrv.Client())),
		Clone:         clone,
		SystemPrompt:  "SYS",
		MaxIterations: 5,
		Logger:        log.New(100, io.Discard), // level 100 suppresses logs
		Stdout:        &bytes.Buffer{},
	}
	return &testDeps{deps: deps, llmSrv: llmSrv, ghSrv: ghSrv, llmHits: &llmHits, ghHits: &ghHits}
}

func doRequest(t *testing.T, s *Server, method, path string, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rr := httptest.NewRecorder()
	s.Handler().ServeHTTP(rr, req)
	return rr
}

func TestHandlerReviewSubmittedReturns200(t *testing.T) {
	td := newTestDeps(t,
		func(w http.ResponseWriter, r *http.Request) { w.Write(llmCompletion("submit_review_comment", `{"body":"approved"}`)) },
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusCreated) },
	)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/pubsub", rawPREvent, map[string]string{
		"X-GitHub-Event": "pull_request",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if atomic.LoadInt32(td.llmHits) != 1 {
		t.Errorf("llm hits=%d want 1", *td.llmHits)
	}
	if atomic.LoadInt32(td.ghHits) != 1 {
		t.Errorf("gh hits=%d want 1 (comment posted)", *td.ghHits)
	}
}

func TestHandlerWrappedPubSubEnvelope(t *testing.T) {
	td := newTestDeps(t,
		func(w http.ResponseWriter, r *http.Request) { w.Write(llmCompletion("noop", `{"reason":"no plugins"}`)) },
		nil,
	)
	s := New(td.deps, ":0")
	encoded := base64Encode(rawPREvent)
	body := `{"message":{"data":"` + encoded + `"},"subscription":"s"}`
	rr := doRequest(t, s, http.MethodPost, "/pubsub", body, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if atomic.LoadInt32(td.llmHits) != 1 {
		t.Errorf("llm hits=%d want 1", *td.llmHits)
	}
	// noop → no GitHub call.
	if atomic.LoadInt32(td.ghHits) != 0 {
		t.Errorf("gh hits=%d want 0", *td.ghHits)
	}
}

func TestHandlerNonPREventReturns200NoLLM(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/pubsub", `{"ref":"refs/heads/main"}`, map[string]string{
		"X-GitHub-Event": "push",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	if atomic.LoadInt32(td.llmHits) != 0 {
		t.Errorf("llm should not be called for non-PR event; hits=%d", *td.llmHits)
	}
}

func TestHandlerInferredNonPREventReturns200(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, ":0")
	// No header; payload has no top-level pull_request → inferred unknown.
	rr := doRequest(t, s, http.MethodPost, "/pubsub", `{"zen":"keep it logically awesome"}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	if atomic.LoadInt32(td.llmHits) != 0 {
		t.Errorf("llm should not be called; hits=%d", *td.llmHits)
	}
}

func TestHandlerMalformedBodyReturns200(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/pubsub", `{not json`, map[string]string{
		"X-GitHub-Event": "pull_request",
	})
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (ack malformed)", rr.Code)
	}
	if atomic.LoadInt32(td.llmHits) != 0 {
		t.Errorf("llm should not be called; hits=%d", *td.llmHits)
	}
}

func TestHandlerUnparseablePRPayloadReturns200(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, ":0")
	// Has pull_request field (inferred as PR event) but missing required fields.
	rr := doRequest(t, s, http.MethodPost, "/pubsub", `{"pull_request":{},"number":0}`, nil)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200 (ack bad PR payload)", rr.Code)
	}
	if atomic.LoadInt32(td.llmHits) != 0 {
		t.Errorf("llm should not be called; hits=%d", *td.llmHits)
	}
}

func TestHandlerNonPOSTReturns405(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodGet, "/pubsub", "", nil)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", rr.Code)
	}
}

func TestHandlerWrongPathReturns404(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/other", rawPREvent, nil)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status=%d want 404", rr.Code)
	}
}

func TestHandlerLLMErrorReturns500(t *testing.T) {
	td := newTestDeps(t,
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusBadGateway) },
		nil,
	)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/pubsub", rawPREvent, map[string]string{
		"X-GitHub-Event": "pull_request",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlerCommentPostFailureReturns500(t *testing.T) {
	td := newTestDeps(t,
		func(w http.ResponseWriter, r *http.Request) { w.Write(llmCompletion("submit_review_comment", `{"body":"x"}`)) },
		func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusServiceUnavailable) },
	)
	s := New(td.deps, ":0")
	rr := doRequest(t, s, http.MethodPost, "/pubsub", rawPREvent, map[string]string{
		"X-GitHub-Event": "pull_request",
	})
	if rr.Code != http.StatusInternalServerError {
		t.Fatalf("status=%d want 500 (transient comment failure)", rr.Code)
	}
}

func TestListenAndServeShutdownOnCtxCancel(t *testing.T) {
	td := newTestDeps(t, nil, nil)
	s := New(td.deps, "127.0.0.1:0", WithShutdownTimeout(time.Second))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.ListenAndServe(ctx) }()

	// Give the server a moment to bind, then cancel.
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
