package githubclient

import (
	"encoding/json"
	"fmt"
	"strings"
)

// EventType constants for GitHub webhook events the agent recognizes.
const (
	EventPullRequest = "pull_request"
	EventUnknown     = "unknown"
)

// PREvent is the subset of a GitHub pull_request webhook payload used by the
// agent. Unknown fields are ignored during unmarshaling.
type PREvent struct {
	Action      string      `json:"action"`
	Number      int         `json:"number"`
	PullRequest PullRequest `json:"pull_request"`
	Repository  Repository  `json:"repository"`
}

// PullRequest is the relevant subset of the pull_request object.
type PullRequest struct {
	Number  int    `json:"number"`
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Head    Head   `json:"head"`
	User    User   `json:"user"`
}

// Head identifies the PR head commit.
type Head struct {
	SHA string `json:"sha"`
}

// User is the PR author.
type User struct {
	Login string `json:"login"`
}

// Repository is the relevant subset of the repository object.
type Repository struct {
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Owner    Owner  `json:"owner"`
}

// Owner is the repository owner login.
type Owner struct {
	Login string `json:"login"`
}

// PRRef returns the canonical "{owner}/{repo}#{number}" reference for logging.
func (e PREvent) PRRef() string {
	return fmt.Sprintf("%s/%s#%d", e.Repository.Owner.Login, e.Repository.Name, e.Number)
}

// DetectEventType determines the GitHub event type for a payload.
//
// The X-GitHub-Event header is authoritative when present (GitHub webhooks set
// it). When absent (Pub/Sub-delivered events), the type is inferred from the
// payload's top-level fields: the presence of a "pull_request" object indicates
// a pull_request event; otherwise the type is "unknown".
func DetectEventType(header string, payload []byte) string {
	if h := strings.TrimSpace(header); h != "" {
		return h
	}
	var probe struct {
		PullRequest *json.RawMessage `json:"pull_request"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return EventUnknown
	}
	if probe.PullRequest != nil {
		return EventPullRequest
	}
	return EventUnknown
}

// ParsePullRequestEvent unmarshals a raw GitHub pull_request webhook payload.
func ParsePullRequestEvent(payload []byte) (PREvent, error) {
	var e PREvent
	if err := json.Unmarshal(payload, &e); err != nil {
		return PREvent{}, fmt.Errorf("parsing pull_request event: %w", err)
	}
	if e.Number == 0 && e.PullRequest.Number != 0 {
		e.Number = e.PullRequest.Number
	}
	if e.Number == 0 {
		return PREvent{}, fmt.Errorf("pull_request event missing PR number")
	}
	if e.Repository.Owner.Login == "" || e.Repository.Name == "" {
		return PREvent{}, fmt.Errorf("pull_request event missing repository owner/name")
	}
	return e, nil
}
