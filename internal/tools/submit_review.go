package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
)

// DryRunCommentStart and DryRunCommentEnd delimit the intercepted comment body
// written to stdout in dry-run mode (see AGENT_CLI.md §5.3).
const (
	DryRunCommentStart = "--- review comment (dry-run, not posted) ---"
	DryRunCommentEnd   = "--- end review comment ---"
)

// SubmitReview implements the submit_review_comment terminal tool.
type SubmitReview struct {
	gh     GitHubClient
	rc     ReviewContext
	out    io.Writer
	logger *slog.Logger
}

// NewSubmitReview builds a submit_review_comment tool. out is where the
// intercepted comment body is written in dry-run mode (typically os.Stdout);
// logger records the intercepted body as a structured log field.
func NewSubmitReview(gh GitHubClient, rc ReviewContext, out io.Writer, logger *slog.Logger) *SubmitReview {
	if logger == nil {
		logger = slog.New(slog.NewJSONHandler(io.Discard, nil))
	}
	return &SubmitReview{gh: gh, rc: rc, out: out, logger: logger}
}

func (t *SubmitReview) Name() string    { return "submit_review_comment" }
func (t *SubmitReview) Terminal() bool  { return true }

// Run posts the comment (production) or intercepts and prints it (dry-run).
//
// TODO(#14.2): v1 does not deduplicate against existing PR comments, so Pub/Sub
// at-least-once redelivery may post the same review twice. Candidate fix:
// search the PR for an existing comment with a hidden marker and skip. See
// design/AGENT_CLI.md §14.
func (t *SubmitReview) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	var p struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal([]byte(args), &p); err != nil {
		return "", fmt.Errorf("parsing submit_review_comment args: %w", err)
	}
	if p.Body == "" {
		return "", fmt.Errorf("submit_review_comment requires a non-empty body")
	}

	if dryRun {
		fmt.Fprintf(t.out, "%s\n%s\n%s\n", DryRunCommentStart, p.Body, DryRunCommentEnd)
		t.logger.Info("submit_review_comment intercepted (dry-run)",
			"tool", t.Name(),
			"body", p.Body,
			"dry_run", true,
			"pr", fmt.Sprintf("%s/%s#%d", t.rc.Owner, t.rc.Repo, t.rc.PRNumber),
		)
		return "dry-run: comment not posted", nil
	}

	if err := t.gh.PostComment(ctx, t.rc.Owner, t.rc.Repo, t.rc.PRNumber, p.Body); err != nil {
		return "", err
	}
	t.logger.Info("submit_review_comment posted",
		"tool", t.Name(),
		"pr", fmt.Sprintf("%s/%s#%d", t.rc.Owner, t.rc.Repo, t.rc.PRNumber),
		"dry_run", false,
	)
	return "review comment posted", nil
}
