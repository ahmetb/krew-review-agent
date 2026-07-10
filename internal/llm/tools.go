package llm

import (
	"github.com/openai/openai-go"
	"github.com/openai/openai-go/shared"
)

// Tool names exposed to the LLM. These match the names implemented in
// internal/tools and the descriptions in SYSTEM_PROMPT.md.
const (
	ToolFetchPRDiff         = "fetch_pr_diff"
	ToolFetchPluginManifest = "fetch_plugin_manifest"
	ToolGetAllPlugins       = "get_all_existing_plugins"
	ToolSubmitReviewComment = "submit_review_comment"
	ToolNoop                = "noop"
)

// NoParams is the JSON schema for a tool that takes no parameters.
var NoParams = shared.FunctionParameters{
	"type":       "object",
	"properties": map[string]any{},
}

// StringParam describes a single required string property for a tool schema.
func StringParam(name, description string) shared.FunctionParameters {
	return shared.FunctionParameters{
		"type": "object",
		"properties": map[string]any{
			name: map[string]any{
				"type":        "string",
				"description": description,
			},
		},
		"required": []string{name},
	}
}

// ToolParams returns the OpenAI-compatible tool/function definitions passed to
// the LLM on every chat completion call. The set and shapes match the tool
// implementations in internal/tools.
func ToolParams() []openai.ChatCompletionToolParam {
	return []openai.ChatCompletionToolParam{
		{
			Function: shared.FunctionDefinitionParam{
				Name:        ToolFetchPRDiff,
				Description: openai.String("Fetches the raw git diff of the pull request being reviewed. Takes no parameters."),
				Parameters:  NoParams,
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        ToolFetchPluginManifest,
				Description: openai.String("Fetches the full YAML manifest of an existing plugin from the master krew-index by plugin name (not a file path)."),
				Parameters:  StringParam("name", "The plugin name (kebab-case), e.g. 'whoami'."),
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        ToolGetAllPlugins,
				Description: openai.String("Returns a list of all currently approved plugins in the krew-index as 'name: shortDescription' lines. Use to detect duplicate functionality."),
				Parameters:  NoParams,
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        ToolSubmitReviewComment,
				Description: openai.String("Submits the final Markdown review comment to the pull request. This is a TERMINAL tool: calling it ends the review loop."),
				Parameters:  StringParam("body", "The Markdown body of the review comment."),
			},
		},
		{
			Function: shared.FunctionDefinitionParam{
				Name:        ToolNoop,
				Description: openai.String("Records that the review did not result in any comment (e.g. the PR does not touch plugins/). This is a TERMINAL tool: calling it ends the review loop."),
				Parameters:  StringParam("reason", "Why no review comment is being posted."),
			},
		},
	}
}
