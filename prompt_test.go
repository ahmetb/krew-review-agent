package systemprompt

import (
	"strings"
	"testing"
)

func TestSystemPromptEmbedded(t *testing.T) {
	if Content == "" {
		t.Fatal("embedded system prompt is empty")
	}
	if !strings.Contains(Content, "krew-review-agent") {
		t.Errorf("system prompt does not mention krew-review-agent: %q", Content[:min(80, len(Content))])
	}
	for _, want := range []string{
		"fetch_pr_diff",
		"fetch_plugin_manifest",
		"get_all_existing_plugins",
		"submit_review_comment",
		"noop",
	} {
		if !strings.Contains(Content, want) {
			t.Errorf("system prompt missing tool %q", want)
		}
	}
}
