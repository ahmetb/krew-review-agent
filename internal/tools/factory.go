package tools

import (
	"io"
	"log/slog"
)

// Deps bundles the shared dependencies used to build a per-review tool
// registry. The ReviewContext (which varies per PR) is supplied separately to
// BuildRegistry.
type Deps struct {
	GH     GitHubClient
	Clone  *Clone
	Stdout io.Writer
	Logger *slog.Logger
}

// BuildRegistry constructs the standard 5-tool registry for a given review.
// Tools that need the PR context (fetch_pr_diff, submit_review_comment) are
// bound to rc; the manifest/listing tools share the krew-index clone.
func (d Deps) BuildRegistry(rc ReviewContext) *Registry {
	return NewRegistry(
		NewFetchPRDiff(d.GH, rc),
		NewFetchPluginManifest(d.Clone),
		NewGetAllPlugins(d.Clone),
		NewSubmitReview(d.GH, rc, d.Stdout, d.Logger),
		NewNoop(d.Logger),
	)
}
