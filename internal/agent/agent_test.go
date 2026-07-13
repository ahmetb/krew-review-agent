package agent

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/ahmetb/krew-review-agent/internal/githubclient"
	"github.com/ahmetb/krew-review-agent/internal/llm"
	"github.com/ahmetb/krew-review-agent/internal/tools"
)

func prEventForTest() githubclient.PREvent {
	return githubclient.PREvent{
		Action: "opened",
		Number: 123,
		PullRequest: githubclient.PullRequest{
			Number: 123, Title: "Add foo plugin", Body: "b",
			Head: githubclient.Head{SHA: "abc123"}, User: githubclient.User{Login: "alice"},
		},
		Repository: githubclient.Repository{Name: "repo", Owner: githubclient.Owner{Login: "owner"}},
	}
}

// mockLLM returns scripted responses in order.
type mockLLM struct {
	responses []llm.Response
	errs      []error
	calls     int
	msgsAt    [][]llm.Message // messages seen per call
}

func (m *mockLLM) Complete(ctx context.Context, msgs []llm.Message) (llm.Response, error) {
	i := m.calls
	m.calls++
	// snapshot the messages seen (for assertions)
	cp := append([]llm.Message(nil), msgs...)
	m.msgsAt = append(m.msgsAt, cp)
	if i < len(m.errs) && m.errs[i] != nil {
		return llm.Response{}, m.errs[i]
	}
	if i < len(m.responses) {
		return m.responses[i], nil
	}
	return llm.Response{}, fmt.Errorf("mockLLM: no response scheduled at index %d", i)
}

// fakeTool is a configurable tool for loop tests.
type fakeTool struct {
	name     string
	terminal bool
	result   string
	err      error
	calls    int
	lastArgs string
	lastDry  bool
}

func (f *fakeTool) Name() string    { return f.name }
func (f *fakeTool) Terminal() bool  { return f.terminal }
func (f *fakeTool) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	f.calls++
	f.lastArgs = args
	f.lastDry = dryRun
	return f.result, f.err
}

func baseCfg(m llm.Client, registry *tools.Registry) Config {
	return Config{
		LLM:           m,
		Tools:         registry,
		SystemPrompt:  "SYS",
		MaxIterations: 5,
		RC:            tools.ReviewContext{Owner: "o", Repo: "r", PRNumber: 1, Title: "T", Author: "a", Action: "opened", Body: "B"},
		DryRun:        false,
	}
}

func toolCall(id, name, args string) llm.ToolCall {
	return llm.ToolCall{ID: id, Name: name, Arguments: args}
}

func TestHistory(t *testing.T) {
	h := NewMessageHistory()
	h.AddSystem("s")
	h.AddUser("u")
	h.AddAssistantText("a")
	h.AddAssistantToolCalls("", []llm.ToolCall{toolCall("1", "fetch_pr_diff", "{}")})
	h.AddTool("1", "result")
	if h.Len() != 5 {
		t.Fatalf("len=%d", h.Len())
	}
	msgs := h.Messages()
	if msgs[0].Role != llm.RoleSystem || msgs[0].Content != "s" {
		t.Errorf("msg0=%+v", msgs[0])
	}
	if msgs[3].Role != llm.RoleAssistant || len(msgs[3].ToolCalls) != 1 {
		t.Errorf("msg3=%+v", msgs[3])
	}
	if msgs[4].Role != llm.RoleTool || msgs[4].ToolCallID != "1" || msgs[4].Content != "result" {
		t.Errorf("msg4=%+v", msgs[4])
	}
}

func TestRunTerminalSubmitReview(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "posted"}
	r := tools.NewRegistry(submit)
	m := &mockLLM{responses: []llm.Response{{
		ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolSubmitReviewComment, `{"body":"hi"}`)},
		FinishReason: "tool_calls",
	}}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err != nil || outcome != OutcomeReviewSubmitted {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if submit.calls != 1 {
		t.Errorf("submit called %d", submit.calls)
	}
}

func TestRunNoop(t *testing.T) {
	noop := &fakeTool{name: llm.ToolNoop, terminal: true, result: "noop"}
	r := tools.NewRegistry(noop)
	m := &mockLLM{responses: []llm.Response{{
		ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolNoop, `{"reason":"x"}`)},
	}}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err != nil || outcome != OutcomeNoop {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
}

func TestRunNonTerminalThenTerminal(t *testing.T) {
	fetch := &fakeTool{name: llm.ToolFetchPRDiff, result: "DIFF"}
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "posted"}
	r := tools.NewRegistry(fetch, submit)
	m := &mockLLM{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolFetchPRDiff, "{}")}},
		{ToolCalls: []llm.ToolCall{toolCall("c2", llm.ToolSubmitReviewComment, `{"body":"ok"}`)}},
	}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err != nil || outcome != OutcomeReviewSubmitted {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if fetch.calls != 1 || submit.calls != 1 {
		t.Errorf("fetch=%d submit=%d", fetch.calls, submit.calls)
	}
	// The tool result should have been fed back to the LLM on the 2nd call.
	if len(m.msgsAt[1]) < 4 {
		t.Errorf("expected tool result in history; got %d msgs", len(m.msgsAt[1]))
	}
	last := m.msgsAt[1][len(m.msgsAt[1])-1]
	if last.Role != llm.RoleTool || last.Content != "DIFF" {
		t.Errorf("last msg of 2nd call=%+v", last)
	}
}

func TestRunConversationalTextTriggersWarning(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "posted"}
	r := tools.NewRegistry(submit)
	m := &mockLLM{responses: []llm.Response{
		{Content: "Let me think about this."}, // no tool call
		{ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolSubmitReviewComment, `{"body":"x"}`)}},
	}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err != nil || outcome != OutcomeReviewSubmitted {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	// 2nd LLM call's history must contain the warning user message.
	found := false
	for _, msg := range m.msgsAt[1] {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "must use a tool") {
			found = true
		}
	}
	if !found {
		t.Error("expected warning user message in 2nd call history")
	}
}

func TestRunUnknownToolRecordedAndContinues(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "posted"}
	r := tools.NewRegistry(submit)
	m := &mockLLM{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{toolCall("c1", "nonexistent_tool", "{}")}},
		{ToolCalls: []llm.ToolCall{toolCall("c2", llm.ToolSubmitReviewComment, `{"body":"x"}`)}},
	}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err != nil || outcome != OutcomeReviewSubmitted {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	// The unknown tool result (error) should appear in 2nd call history.
	last := m.msgsAt[1][len(m.msgsAt[1])-1]
	if !strings.Contains(last.Content, "unknown tool") {
		t.Errorf("expected unknown-tool error in history, got %+v", last)
	}
}

func TestRunToolErrorAborts(t *testing.T) {
	fetch := &fakeTool{name: llm.ToolFetchPRDiff, err: errors.New("github down")}
	r := tools.NewRegistry(fetch)
	m := &mockLLM{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolFetchPRDiff, "{}")}},
	}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err == nil || outcome != OutcomeError {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if !strings.Contains(err.Error(), "github down") {
		t.Errorf("err=%v", err)
	}
}

func TestRunLLMErrorAborts(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true}
	r := tools.NewRegistry(submit)
	m := &mockLLM{errs: []error{errors.New("llm unavailable")}}
	ag := New(baseCfg(m, r))
	outcome, err := ag.Run(context.Background())
	if err == nil || outcome != OutcomeError {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
}

func TestRunCircuitBreakerThenTerminal(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "posted"}
	// Set MaxIterations=1 so the first iteration runs, then the circuit breaker
	// fires.
	fetch := &fakeTool{name: llm.ToolFetchPRDiff, result: "d"}
	r := tools.NewRegistry(fetch, submit)
	m := &mockLLM{responses: []llm.Response{
		{ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolFetchPRDiff, "{}")}},                            // iter 1
		{ToolCalls: []llm.ToolCall{toolCall("c2", llm.ToolSubmitReviewComment, `{"body":"final"}`)}}, // circuit breaker
	}}
	cfg := baseCfg(m, r)
	cfg.MaxIterations = 1
	ag := New(cfg)
	outcome, err := ag.Run(context.Background())
	if err != nil || outcome != OutcomeReviewSubmitted {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	// Circuit breaker prompt must be in the 2nd call's history.
	found := false
	for _, msg := range m.msgsAt[1] {
		if msg.Role == llm.RoleUser && strings.Contains(msg.Content, "CIRCUIT BREAKER") {
			found = true
		}
	}
	if !found {
		t.Error("expected circuit breaker prompt in 2nd call history")
	}
}

func TestRunCircuitBreakerFallbackDryRun(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "intercepted"}
	r := tools.NewRegistry(submit)
	cfg := baseCfg(&mockLLM{}, r)
	cfg.MaxIterations = 1
	cfg.DryRun = true
	m := &mockLLM{responses: []llm.Response{
		{Content: "I can't decide."}, // iter 1: conversational text
		{Content: "still no tool"},   // circuit breaker: no terminal tool
	}}
	cfg.LLM = m
	ag := New(cfg)
	outcome, err := ag.Run(context.Background())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if outcome != OutcomeFallback {
		t.Fatalf("outcome=%v want OutcomeFallback", outcome)
	}
	// Fallback uses submit_review_comment with the fallback body.
	if submit.calls != 1 {
		t.Errorf("submit called %d, want 1 (fallback)", submit.calls)
	}
	var fb struct {
		Body              string `json:"body"`
		NeedsHumanReview bool  `json:"needs_human_review"`
	}
	if err := json.Unmarshal([]byte(submit.lastArgs), &fb); err != nil {
		t.Fatalf("fallback args not json: %v", err)
	}
	if !strings.Contains(fb.Body, "internal error") || !strings.Contains(fb.Body, "/hold") {
		t.Errorf("fallback body=%q", fb.Body)
	}
	if !fb.NeedsHumanReview {
		t.Errorf("fallback needs_human_review should be true; args=%s", submit.lastArgs)
	}
	if !submit.lastDry {
		t.Errorf("dryRun not propagated to fallback")
	}
}

func TestRunCircuitBreakerFallbackProduction(t *testing.T) {
	// Use the real submit_review tool with a stub GitHub client so the fallback
	// actually "posts".
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "posted"}
	r := tools.NewRegistry(submit)
	cfg := baseCfg(&mockLLM{}, r)
	cfg.MaxIterations = 1
	cfg.DryRun = false
	m := &mockLLM{responses: []llm.Response{
		{Content: "hmm"},
		{Content: "no tool"},
	}}
	cfg.LLM = m
	ag := New(cfg)
	outcome, err := ag.Run(context.Background())
	if err != nil {
		t.Fatalf("err=%v", err)
	}
	if outcome != OutcomeFallback {
		t.Fatalf("outcome=%v want OutcomeFallback", outcome)
	}
}

func TestRunCircuitBreakerFallbackPostFails(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, err: errors.New("post failed")}
	r := tools.NewRegistry(submit)
	cfg := baseCfg(&mockLLM{}, r)
	cfg.MaxIterations = 1
	m := &mockLLM{responses: []llm.Response{
		{Content: "hmm"},
		{Content: "no tool"},
	}}
	cfg.LLM = m
	ag := New(cfg)
	outcome, err := ag.Run(context.Background())
	if err == nil || outcome != OutcomeError {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
	if !strings.Contains(err.Error(), "fallback") {
		t.Errorf("err=%v", err)
	}
}

func TestRunCircuitBreakerLLMError(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true}
	r := tools.NewRegistry(submit)
	cfg := baseCfg(&mockLLM{}, r)
	cfg.MaxIterations = 1
	m := &mockLLM{
		responses: []llm.Response{{Content: "hmm"}},
		errs:      []error{nil, errors.New("llm died")},
	}
	cfg.LLM = m
	ag := New(cfg)
	outcome, err := ag.Run(context.Background())
	if err == nil || outcome != OutcomeError {
		t.Fatalf("outcome=%v err=%v", outcome, err)
	}
}

func TestRunDryRunPropagatedToTools(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "ok"}
	r := tools.NewRegistry(submit)
	cfg := baseCfg(&mockLLM{}, r)
	cfg.DryRun = true
	m := &mockLLM{responses: []llm.Response{{
		ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolSubmitReviewComment, `{"body":"x"}`)},
	}}}
	cfg.LLM = m
	ag := New(cfg)
	if _, err := ag.Run(context.Background()); err != nil {
		t.Fatalf("err=%v", err)
	}
	if !submit.lastDry {
		t.Error("dryRun not propagated to tool")
	}
}

func TestRunSystemAndUserPromptSeeded(t *testing.T) {
	submit := &fakeTool{name: llm.ToolSubmitReviewComment, terminal: true, result: "ok"}
	r := tools.NewRegistry(submit)
	m := &mockLLM{responses: []llm.Response{{
		ToolCalls: []llm.ToolCall{toolCall("c1", llm.ToolSubmitReviewComment, `{"body":"x"}`)},
	}}}
	ag := New(baseCfg(m, r))
	if _, err := ag.Run(context.Background()); err != nil {
		t.Fatalf("err=%v", err)
	}
	first := m.msgsAt[0]
	if first[0].Role != llm.RoleSystem || first[0].Content != "SYS" {
		t.Errorf("system prompt not first: %+v", first[0])
	}
	if first[1].Role != llm.RoleUser || !strings.Contains(first[1].Content, "#1") || !strings.Contains(first[1].Content, "o/r") {
		t.Errorf("user prompt missing PR context: %+v", first[1])
	}
}

func TestOutcomeString(t *testing.T) {
	cases := map[Outcome]string{
		OutcomeReviewSubmitted: "review_submitted",
		OutcomeNoop:            "noop",
		OutcomeFallback:        "fallback",
		OutcomeError:           "error",
	}
	for o, want := range cases {
		if o.String() != want {
			t.Errorf("%d.String()=%q want %q", o, o.String(), want)
		}
	}
}

func TestReviewContextFromEvent(t *testing.T) {
	rc := ReviewContextFromEvent(prEventForTest())
	if rc.Owner != "owner" || rc.Repo != "repo" || rc.PRNumber != 123 {
		t.Errorf("rc=%+v", rc)
	}
	if rc.Title != "Add foo plugin" || rc.Author != "alice" || rc.HeadSHA != "abc123" || rc.Action != "opened" {
		t.Errorf("rc=%+v", rc)
	}
}

func TestBuildUserPromptEmptyBody(t *testing.T) {
	cfg := baseCfg(&mockLLM{}, tools.NewRegistry(&fakeTool{name: llm.ToolNoop, terminal: true}))
	cfg.RC.Body = "   "
	ag := New(cfg)
	got := ag.buildUserPrompt()
	if !strings.Contains(got, "(no description provided)") {
		t.Errorf("expected placeholder for empty body: %q", got)
	}
}
