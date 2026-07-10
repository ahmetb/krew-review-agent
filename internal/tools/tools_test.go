package tools

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// stubGH is a test double for GitHubClient.
type stubGH struct {
	diff  string
	diffErr error
	posted string
	postErr error
}

func (s *stubGH) FetchPRDiff(ctx context.Context, owner, repo string, pr int) (string, error) {
	return s.diff, s.diffErr
}

func (s *stubGH) PostComment(ctx context.Context, owner, repo string, pr int, body string) error {
	s.posted = body
	return s.postErr
}

func sampleRC() ReviewContext {
	return ReviewContext{Owner: "owner", Repo: "repo", PRNumber: 42, Title: "t", Author: "a", Action: "opened"}
}

func TestFetchPRDiff(t *testing.T) {
	gh := &stubGH{diff: "DIFF-CONTENT"}
	tool := NewFetchPRDiff(gh, sampleRC())
	got, err := tool.Run(context.Background(), "{}", true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "DIFF-CONTENT" {
		t.Errorf("got=%q", got)
	}
}

func TestFetchPRDiffEmpty(t *testing.T) {
	gh := &stubGH{diff: ""}
	tool := NewFetchPRDiff(gh, sampleRC())
	got, _ := tool.Run(context.Background(), "", false)
	if got != "(empty diff)" {
		t.Errorf("got=%q", got)
	}
}

func TestFetchPRDiffError(t *testing.T) {
	gh := &stubGH{diffErr: fmt.Errorf("boom")}
	tool := NewFetchPRDiff(gh, sampleRC())
	if _, err := tool.Run(context.Background(), "", false); err == nil {
		t.Fatal("expected error")
	}
}

func TestFetchPluginManifestExisting(t *testing.T) {
	c := newCloneWithFake(t, map[string]string{
		"whoami": "name: whoami\nshortDescription: Show identity\n",
	})
	tool := NewFetchPluginManifest(c)
	got, err := tool.Run(context.Background(), `{"name":"whoami"}`, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got, "name: whoami") {
		t.Errorf("got=%q", got)
	}
}

func TestFetchPluginManifestNewSubmission(t *testing.T) {
	c := newCloneWithFake(t, nil)
	tool := NewFetchPluginManifest(c)
	got, err := tool.Run(context.Background(), `{"name":"newplugin"}`, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got, "does not exist") || !strings.Contains(got, "new submission") {
		t.Errorf("got=%q", got)
	}
}

func TestFetchPluginManifestInvalidName(t *testing.T) {
	c := newCloneWithFake(t, nil)
	tool := NewFetchPluginManifest(c)
	for _, name := range []string{`{"name":"../etc"}`, `{"name":"FOO"}`, `{"name":""}`} {
		if _, err := tool.Run(context.Background(), name, false); err == nil {
			t.Errorf("expected error for args %q", name)
		}
	}
}

func TestFetchPluginManifestBadArgs(t *testing.T) {
	c := newCloneWithFake(t, nil)
	tool := NewFetchPluginManifest(c)
	if _, err := tool.Run(context.Background(), `not json`, false); err == nil {
		t.Fatal("expected error for bad args")
	}
}

func TestFetchPluginManifestEnsureFails(t *testing.T) {
	c := CloneForTest(t.TempDir(), "u", func(ctx context.Context, url, dir string) error {
		return fmt.Errorf("clone failed")
	})
	tool := NewFetchPluginManifest(c)
	if _, err := tool.Run(context.Background(), `{"name":"whoami"}`, false); err == nil {
		t.Fatal("expected clone error")
	}
}

func TestGetAllPlugins(t *testing.T) {
	c := newCloneWithFake(t, map[string]string{
		"whoami":  "name: whoami\nshortDescription: Show identity\n",
		"rbac-lookup": "name: rbac-lookup\nshortDescription: RBAC lookup\n",
	})
	tool := NewGetAllPlugins(c)
	got, err := tool.Run(context.Background(), "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got, "whoami: Show identity") {
		t.Errorf("missing whoami: %q", got)
	}
	if !strings.Contains(got, "rbac-lookup: RBAC lookup") {
		t.Errorf("missing rbac-lookup: %q", got)
	}
}

func TestGetAllPluginsSkipsInvalidYAML(t *testing.T) {
	c := newCloneWithFake(t, map[string]string{
		"good": "name: good\nshortDescription: ok\n",
		"bad":  "::: not valid yaml :::\n  - broken",
	})
	// Materialize the clone so the plugins directory exists, then add a
	// non-.yaml file that must be skipped by the listing.
	if err := c.Ensure(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.PluginsDir(), "README.txt"), []byte("ignore me"), 0o644); err != nil {
		t.Fatal(err)
	}
	tool := NewGetAllPlugins(c)
	got, err := tool.Run(context.Background(), "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(got, "good: ok") {
		t.Errorf("missing good: %q", got)
	}
	if strings.Contains(got, "bad:") {
		t.Errorf("invalid yaml should be skipped: %q", got)
	}
}

func TestGetAllPluginsEmpty(t *testing.T) {
	c := newCloneWithFake(t, nil)
	tool := NewGetAllPlugins(c)
	got, err := tool.Run(context.Background(), "", false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "(no plugins found)" {
		t.Errorf("got=%q", got)
	}
}

func TestSubmitReviewPostsInProduction(t *testing.T) {
	gh := &stubGH{}
	tool := NewSubmitReview(gh, sampleRC(), &strings.Builder{}, nil)
	got, err := tool.Run(context.Background(), `{"body":"nice PR"}`, false)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.posted != "nice PR" {
		t.Errorf("posted=%q", gh.posted)
	}
	if !strings.Contains(got, "posted") {
		t.Errorf("got=%q", got)
	}
}

func TestSubmitReviewDryRunIntercepts(t *testing.T) {
	gh := &stubGH{}
	var buf strings.Builder
	tool := NewSubmitReview(gh, sampleRC(), &buf, nil)
	got, err := tool.Run(context.Background(), `{"body":"hello world"}`, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gh.posted != "" {
		t.Errorf("dry-run must not post; posted=%q", gh.posted)
	}
	out := buf.String()
	if !strings.Contains(out, DryRunCommentStart) || !strings.Contains(out, "hello world") || !strings.Contains(out, DryRunCommentEnd) {
		t.Errorf("dry-run output missing delimiters/body:\n%s", out)
	}
	if !strings.Contains(got, "dry-run") {
		t.Errorf("got=%q", got)
	}
}

func TestSubmitReviewEmptyBody(t *testing.T) {
	gh := &stubGH{}
	tool := NewSubmitReview(gh, sampleRC(), &strings.Builder{}, nil)
	if _, err := tool.Run(context.Background(), `{"body":""}`, false); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestSubmitReviewPostError(t *testing.T) {
	gh := &stubGH{postErr: fmt.Errorf("github down")}
	tool := NewSubmitReview(gh, sampleRC(), &strings.Builder{}, nil)
	if _, err := tool.Run(context.Background(), `{"body":"x"}`, false); err == nil {
		t.Fatal("expected post error")
	}
}

func TestNoop(t *testing.T) {
	tool := NewNoop(nil)
	got, err := tool.Run(context.Background(), `{"reason":"not a plugins PR"}`, true)
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got != "noop" {
		t.Errorf("got=%q", got)
	}
}

func TestNoopBadArgs(t *testing.T) {
	tool := NewNoop(nil)
	if _, err := tool.Run(context.Background(), `not json`, false); err == nil {
		t.Fatal("expected error for bad args")
	}
}

func TestToolNamesAndTerminal(t *testing.T) {
	c := newCloneWithFake(t, nil)
	gh := &stubGH{}
	cases := []struct {
		tool     Tool
		name     string
		terminal bool
	}{
		{NewFetchPRDiff(gh, sampleRC()), "fetch_pr_diff", false},
		{NewFetchPluginManifest(c), "fetch_plugin_manifest", false},
		{NewGetAllPlugins(c), "get_all_existing_plugins", false},
		{NewSubmitReview(gh, sampleRC(), &strings.Builder{}, nil), "submit_review_comment", true},
		{NewNoop(nil), "noop", true},
	}
	for _, c := range cases {
		if c.tool.Name() != c.name {
			t.Errorf("name=%q want %q", c.tool.Name(), c.name)
		}
		if c.tool.Terminal() != c.terminal {
			t.Errorf("%s terminal=%v want %v", c.name, c.tool.Terminal(), c.terminal)
		}
	}
}
