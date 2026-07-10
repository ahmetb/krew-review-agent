// Package tools defines the agent's tool interface, registry, and the
// concrete implementations of each LLM-callable tool.
//
// Every tool implements the Tool interface. The orchestration loop invokes
// Run with the raw JSON arguments from the LLM and a dryRun flag (true in test
// mode) that controls side-effecting behavior. Terminal tools (those that end
// the review loop) report Terminal() == true.
package tools

import (
	"context"
	"fmt"
	"sync"
)

// ReviewContext carries the per-PR fields that side-effecting and
// data-gathering tools need (owner/repo/number for API calls).
type ReviewContext struct {
	Owner   string
	Repo    string
	PRNumber int
	Title   string
	Body    string
	Author  string
	HeadSHA string
	Action  string
}

// PRRef returns the canonical "{owner}/{repo}#{number}" reference for logging.
func (r ReviewContext) PRRef() string {
	return fmt.Sprintf("%s/%s#%d", r.Owner, r.Repo, r.PRNumber)
}

// Tool is the contract for an LLM-callable function.
type Tool interface {
	// Name is the tool name as exposed to the LLM. It must match the schema in
	// internal/llm/tools.go.
	Name() string

	// Terminal reports whether calling this tool ends the review loop.
	Terminal() bool

	// Run executes the tool. args is the raw JSON arguments string from the
	// LLM. dryRun is true in test mode; side-effecting tools must intercept
	// their writes when dryRun is true. The returned string is fed back to the
	// LLM as the tool result (or, for terminal tools, a status message).
	Run(ctx context.Context, args string, dryRun bool) (string, error)
}

// Registry maps tool names to Tool implementations.
type Registry struct {
	mu    sync.RWMutex
	tools map[string]Tool
}

// NewRegistry builds a registry containing the given tools. Tool names must be
// unique; a duplicate panics during construction (a programming error).
func NewRegistry(tools ...Tool) *Registry {
	r := &Registry{tools: make(map[string]Tool, len(tools))}
	for _, t := range tools {
		if _, ok := r.tools[t.Name()]; ok {
			panic(fmt.Sprintf("duplicate tool name %q", t.Name()))
		}
		r.tools[t.Name()] = t
	}
	return r
}

// Get returns the tool with the given name and whether it exists.
func (r *Registry) Get(name string) (Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Names returns the names of all registered tools in a stable-ish order.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	out := make([]string, 0, len(r.tools))
	for name := range r.tools {
		out = append(out, name)
	}
	return out
}
