package main

import (
	"strings"
	"testing"
)

func TestSystemPromptEmbedded(t *testing.T) {
	if systemPrompt == "" {
		t.Fatal("embedded system prompt is empty")
	}
	if !strings.Contains(systemPrompt, "krew-review-agent") {
		t.Errorf("system prompt does not mention krew-review-agent: %q", systemPrompt[:min(80, len(systemPrompt))])
	}
	for _, want := range []string{
		"fetch_pr_diff",
		"fetch_plugin_manifest",
		"get_all_existing_plugins",
		"submit_review_comment",
		"noop",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("system prompt missing tool %q", want)
		}
	}
}

// TestSystemPromptProwCommands guards the Prow slash commands the agent is
// expected to emit; dropping one silently changes merge/labeling behavior in
// krew-index.
func TestSystemPromptProwCommands(t *testing.T) {
	for _, want := range []string{
		"/lgtm",
		"/approve",
		"/kind plugin-update",
		"/close",
		"/hold",
	} {
		if !strings.Contains(systemPrompt, want) {
			t.Errorf("system prompt missing Prow command %q", want)
		}
	}
}
