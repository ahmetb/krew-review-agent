package githubclient

import "testing"

const prEventJSON = `{
  "action": "opened",
  "number": 123,
  "pull_request": {
    "number": 123,
    "title": "Add foo plugin",
    "body": "This adds foo",
    "html_url": "https://github.com/owner/repo/pull/123",
    "head": {"sha": "abc123"},
    "user": {"login": "alice"}
  },
  "repository": {
    "name": "repo",
    "full_name": "owner/repo",
    "owner": {"login": "owner"}
  }
}`

func TestDetectEventTypeHeader(t *testing.T) {
	if got := DetectEventType("pull_request", []byte("{}")); got != EventPullRequest {
		t.Errorf("got %q", got)
	}
	if got := DetectEventType("push", []byte("{}")); got != "push" {
		t.Errorf("got %q", got)
	}
	if got := DetectEventType("  ", []byte("{}")); got != EventUnknown {
		t.Errorf("got %q", got)
	}
}

func TestDetectEventTypeInferred(t *testing.T) {
	if got := DetectEventType("", []byte(prEventJSON)); got != EventPullRequest {
		t.Errorf("inferred=%q want pull_request", got)
	}
	if got := DetectEventType("", []byte(`{"ref":"refs/heads/main"}`)); got != EventUnknown {
		t.Errorf("inferred=%q want unknown", got)
	}
	if got := DetectEventType("", []byte(`not json`)); got != EventUnknown {
		t.Errorf("inferred=%q want unknown", got)
	}
}

func TestParsePullRequestEvent(t *testing.T) {
	e, err := ParsePullRequestEvent([]byte(prEventJSON))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.Action != "opened" {
		t.Errorf("action=%q", e.Action)
	}
	if e.Number != 123 {
		t.Errorf("number=%d", e.Number)
	}
	if e.PullRequest.Title != "Add foo plugin" {
		t.Errorf("title=%q", e.PullRequest.Title)
	}
	if e.PullRequest.Head.SHA != "abc123" {
		t.Errorf("sha=%q", e.PullRequest.Head.SHA)
	}
	if e.PullRequest.User.Login != "alice" {
		t.Errorf("user=%q", e.PullRequest.User.Login)
	}
	if e.Repository.Owner.Login != "owner" || e.Repository.Name != "repo" {
		t.Errorf("repo=%s/%s", e.Repository.Owner.Login, e.Repository.Name)
	}
	if e.PRRef() != "owner/repo#123" {
		t.Errorf("PRRef=%q", e.PRRef())
	}
}

func TestParsePullRequestEventNumberFromPullRequest(t *testing.T) {
	// Some payloads omit the top-level number; fall back to pull_request.number.
	body := `{"action":"opened","pull_request":{"number":55,"title":"t","user":{"login":"u"},"head":{"sha":"s"}},"repository":{"name":"r","owner":{"login":"o"}}}`
	e, err := ParsePullRequestEvent([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if e.Number != 55 {
		t.Errorf("number=%d want 55", e.Number)
	}
}

func TestParsePullRequestEventErrors(t *testing.T) {
	cases := []string{
		`not json`,
		`{}`,
		`{"number":1,"repository":{"name":"r"}}`,
		`{"number":1,"repository":{"name":"r","owner":{}}}`,
	}
	for _, c := range cases {
		_, err := ParsePullRequestEvent([]byte(c))
		if err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}
