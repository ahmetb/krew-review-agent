package githubclient

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) (*Client, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := New("tok", WithBaseURL(srv.URL), WithHTTPClient(srv.Client()))
	return c, srv
}

func TestFetchPRDiff(t *testing.T) {
	var gotPath, gotAccept, gotAuth string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAccept = r.Header.Get("Accept")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/plain")
		io.WriteString(w, "diff --git a/foo b/foo\n+added\n")
	})
	diff, err := c.FetchPRDiff(context.Background(), "owner", "repo", 42)
	if err != nil {
		t.Fatalf("FetchPRDiff: %v", err)
	}
	if !strings.Contains(diff, "+added") {
		t.Errorf("diff=%q", diff)
	}
	if gotPath != "/repos/owner/repo/pulls/42" {
		t.Errorf("path=%q", gotPath)
	}
	if gotAccept != "application/vnd.github.v3.diff" {
		t.Errorf("accept=%q", gotAccept)
	}
	if gotAuth != "Bearer tok" {
		t.Errorf("auth=%q", gotAuth)
	}
}

func TestPostComment(t *testing.T) {
	var gotPath, gotMethod string
	var body map[string]string
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		_ = json.NewDecoder(r.Body).Decode(&body)
		w.WriteHeader(http.StatusCreated)
	})
	if err := c.PostComment(context.Background(), "o", "r", 7, "hi"); err != nil {
		t.Fatalf("PostComment: %v", err)
	}
	if gotPath != "/repos/o/r/issues/7/comments" {
		t.Errorf("path=%q", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method=%q", gotMethod)
	}
	if body["body"] != "hi" {
		t.Errorf("body=%v", body)
	}
}

func TestAPIErrorOnNon2xx(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		io.WriteString(w, `{"message":"Not Found"}`)
	})
	_, err := c.FetchPRDiff(context.Background(), "o", "r", 1)
	if err == nil {
		t.Fatal("expected error for 404")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T", err)
	}
	if apiErr.StatusCode != http.StatusNotFound {
		t.Errorf("status=%d", apiErr.StatusCode)
	}
	if !strings.Contains(apiErr.Error(), "status 404") {
		t.Errorf("error message=%q", apiErr.Error())
	}
}

func TestPostCommentFailure(t *testing.T) {
	c, _ := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	if err := c.PostComment(context.Background(), "o", "r", 1, "x"); err == nil {
		t.Fatal("expected error for 500")
	}
}
