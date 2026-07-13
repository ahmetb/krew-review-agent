package agent

// Circuit-breaker prompts and the fallback comment body used when the LLM
// fails to terminate within MAX_ITERATIONS (see AGENT_CLI.md §7.3).

// CircuitBreakerPrompt is appended as a final user message when the iteration
// count reaches MAX_ITERATIONS, nudging the LLM to finish immediately.
const CircuitBreakerPrompt = "CIRCUIT BREAKER: You have reached the maximum " +
	"allowed tool executions. You must immediately output your findings using " +
	"the submit_review_comment tool."

// WarningNoToolCall is appended as a user message when the LLM returns
// conversational text without a tool call, forcing it back onto the tool path.
const WarningNoToolCall = "You must use a tool to gather data or use " +
	"submit_review_comment to finish your task."

// FallbackCommentBody is the generic comment posted when the circuit breaker
// fires and the LLM still fails to call a terminal tool. It marks the PR for
// human review. The needs-human-review label is added by the
// submit_review_comment tool (invoked with needs_human_review=true by the
// circuit breaker); /hold is kept in the body to block accidental auto-merge.
const FallbackCommentBody = "The krew-review-agent encountered an internal " +
	"error while reviewing this PR and could not complete the review " +
	"automatically. A human reviewer will need to review this PR manually.\n\n" +
	"/hold"
