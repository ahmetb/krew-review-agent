package llm

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// chatCompletionResponse builds a minimal OpenAI-compatible chat completion
// JSON response. content and toolCalls customize the first choice's message.
func chatCompletionResponse(content string, toolCalls []map[string]any) []byte {
	msg := map[string]any{
		"role":    "assistant",
		"content": content,
	}
	if len(toolCalls) > 0 {
		msg["tool_calls"] = toolCalls
	}
	finish := "stop"
	if len(toolCalls) > 0 {
		finish = "tool_calls"
	}
	resp := map[string]any{
		"id":      "chatcmpl-test",
		"object":  "chat.completion",
		"created": 1,
		"model":   "test-model",
		"choices": []map[string]any{
			{
				"index":         0,
				"finish_reason": finish,
				"message":       msg,
				"logprobs":      map[string]any{"content": []any{}, "refusal": []any{}},
			},
		},
	}
	b, _ := json.Marshal(resp)
	return b
}

func startLLMServer(t *testing.T, fn func(w http.ResponseWriter, r *http.Request)) *httptest.Server {
	t.Helper()
	wrapped := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fn(w, r)
	}
	srv := httptest.NewServer(http.HandlerFunc(wrapped))
	t.Cleanup(srv.Close)
	return srv
}

func TestCompleteParsesToolCall(t *testing.T) {
	srv := startLLMServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("path=%q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write(chatCompletionResponse("", []map[string]any{
			{"id": "call_1", "type": "function", "function": map[string]any{"name": "fetch_pr_diff", "arguments": "{}"}},
		}))
	})
	c := NewClient(Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.FinishReason != "tool_calls" {
		t.Errorf("finish=%q", resp.FinishReason)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "fetch_pr_diff" || resp.ToolCalls[0].ID != "call_1" {
		t.Errorf("toolcalls=%+v", resp.ToolCalls)
	}
}

func TestCompleteParsesContent(t *testing.T) {
	srv := startLLMServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write(chatCompletionResponse("hello there", nil))
	})
	c := NewClient(Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	resp, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if resp.Content != "hello there" {
		t.Errorf("content=%q", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %+v", resp.ToolCalls)
	}
}

func TestCompleteSendsMessagesAndTools(t *testing.T) {
	var gotBody map[string]any
	srv := startLLMServer(t, func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(b, &gotBody)
		w.Write(chatCompletionResponse("ok", nil))
	})
	c := NewClient(Config{APIKey: "k", BaseURL: srv.URL, Model: "my-model"})
	_, err := c.Complete(context.Background(), []Message{
		{Role: RoleSystem, Content: "sys"},
		{Role: RoleUser, Content: "u"},
		{Role: RoleAssistant, ToolCalls: []ToolCall{{ID: "c1", Name: "fetch_pr_diff", Arguments: "{}"}}},
		{Role: RoleTool, Content: "result", ToolCallID: "c1"},
	})
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if gotBody["model"] != "my-model" {
		t.Errorf("model=%v", gotBody["model"])
	}
	msgs, ok := gotBody["messages"].([]any)
	if !ok || len(msgs) != 4 {
		t.Fatalf("messages=%v", gotBody["messages"])
	}
	roles := []string{}
	for _, m := range msgs {
		mm := m.(map[string]any)
		roles = append(roles, mm["role"].(string))
	}
	wantRoles := []string{"system", "user", "assistant", "tool"}
	for i, r := range roles {
		if r != wantRoles[i] {
			t.Errorf("msg[%d].role=%q want %q", i, r, wantRoles[i])
		}
	}
	// assistant message should carry tool_calls
	asst := msgs[2].(map[string]any)
	tcs, ok := asst["tool_calls"].([]any)
	if !ok || len(tcs) != 1 {
		t.Errorf("assistant tool_calls=%v", asst["tool_calls"])
	}
	// tool message should carry tool_call_id
	tool := msgs[3].(map[string]any)
	if tool["tool_call_id"] != "c1" {
		t.Errorf("tool_call_id=%v", tool["tool_call_id"])
	}
	// tools list should be sent
	tools, ok := gotBody["tools"].([]any)
	if !ok || len(tools) != 5 {
		t.Errorf("tools count=%d want 5", len(tools))
	}
}

func TestCompleteNoChoices(t *testing.T) {
	srv := startLLMServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"id":"x","object":"chat.completion","created":1,"model":"m","choices":[]}`))
	})
	c := NewClient(Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	_, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for no choices")
	}
}

func TestCompleteHTTPError(t *testing.T) {
	srv := startLLMServer(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"bad key"}`))
	})
	c := NewClient(Config{APIKey: "k", BaseURL: srv.URL, Model: "m"})
	_, err := c.Complete(context.Background(), []Message{{Role: RoleUser, Content: "hi"}})
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestToolParamsHasAllTools(t *testing.T) {
	tools := ToolParams()
	want := map[string]bool{
		ToolFetchPRDiff:         false,
		ToolFetchPluginManifest: false,
		ToolGetAllPlugins:       false,
		ToolSubmitReviewComment: false,
		ToolNoop:                false,
	}
	for _, tp := range tools {
		name := string(tp.Function.Name)
		if _, ok := want[name]; !ok {
			t.Errorf("unexpected tool %q", name)
		}
		want[name] = true
	}
	for name, seen := range want {
		if !seen {
			t.Errorf("missing tool %q", name)
		}
	}
}

func TestStringParamSchema(t *testing.T) {
	s := StringParam("name", "the name")
	if s["type"] != "object" {
		t.Errorf("type=%v", s["type"])
	}
	reqs, ok := s["required"].([]string)
	if !ok || len(reqs) != 1 || reqs[0] != "name" {
		t.Errorf("required=%v", s["required"])
	}
}
