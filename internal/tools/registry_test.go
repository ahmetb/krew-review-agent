package tools

import (
	"context"
	"strings"
	"testing"
)

func TestRegistryGetAndNames(t *testing.T) {
	gh := &stubGH{}
	c := newCloneWithFake(t, nil)
	r := Deps{GH: gh, Clone: c, Stdout: &strings.Builder{}}.BuildRegistry(sampleRC())

	for _, name := range []string{
		"fetch_pr_diff", "fetch_plugin_manifest",
		"get_all_existing_plugins", "submit_review_comment", "noop",
	} {
		if _, ok := r.Get(name); !ok {
			t.Errorf("missing tool %q", name)
		}
	}
	if _, ok := r.Get("does-not-exist"); ok {
		t.Error("expected miss for unknown tool")
	}
	if len(r.Names()) != 5 {
		t.Errorf("names count=%d want 5", len(r.Names()))
	}
}

func TestRegistryDuplicatePanics(t *testing.T) {
	gh := &stubGH{}
	tool := NewFetchPRDiff(gh, sampleRC())
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for duplicate tool")
		}
	}()
	NewRegistry(tool, tool)
}

func TestRegistryGetConcurrentSafe(t *testing.T) {
	gh := &stubGH{}
	r := NewRegistry(NewNoop(nil), NewFetchPRDiff(gh, sampleRC()))
	done := make(chan struct{})
	go func() {
		for i := 0; i < 100; i++ {
			_, _ = r.Get("noop")
		}
		close(done)
	}()
	for i := 0; i < 100; i++ {
		_ = r.Names()
	}
	<-done
}

// fakeTool is a configurable Tool for agent-loop tests.
type fakeTool struct {
	name     string
	terminal bool
	result   string
	err      error
	called   int
}

func (f *fakeTool) Name() string { return f.name }
func (f *fakeTool) Terminal() bool { return f.terminal }
func (f *fakeTool) Run(ctx context.Context, args string, dryRun bool) (string, error) {
	f.called++
	return f.result, f.err
}
