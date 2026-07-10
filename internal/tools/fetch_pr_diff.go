package tools

import (
	"context"
	"encoding/json"
	"fmt"
)

// FetchPRDiff implements the fetch_pr_diff tool.
type FetchPRDiff struct {
	gh GitHubClient
	rc ReviewContext
}

// GitHubClient is the subset of the GitHub API client used by tools. It is kept
// narrow so tools can be unit-tested with a stub. *githubclient.Client satisfies
// this interface.
type GitHubClient interface {
	FetchPRDiff(ctx context.Context, owner, repo string, prNumber int) (string, error)
	PostComment(ctx context.Context, owner, repo string, prNumber int, body string) error
}

// NewFetchPRDiff builds a fetch_pr_diff tool for the given PR.
func NewFetchPRDiff(gh GitHubClient, rc ReviewContext) *FetchPRDiff {
	return &FetchPRDiff{gh: gh, rc: rc}
}

func (t *FetchPRDiff) Name() string    { return "fetch_pr_diff" }
func (t *FetchPRDiff) Terminal() bool  { return false }

// Run fetches the raw PR diff. dryRun is irrelevant (read-only).
func (t *FetchPRDiff) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	// fetch_pr_diff takes no parameters; tolerate any (including empty) args
	// but reject non-empty non-object args defensively.
	if args != "" && args != "null" && args != "{}" {
		var v map[string]any
		if err := json.Unmarshal([]byte(args), &v); err != nil {
			return "", fmt.Errorf("fetch_pr_diff expects no parameters: %w", err)
		}
	}
	diff, err := t.gh.FetchPRDiff(ctx, t.rc.Owner, t.rc.Repo, t.rc.PRNumber)
	if err != nil {
		return "", err
	}
	if diff == "" {
		return "(empty diff)", nil
	}
	return diff, nil
}
