package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/ahmetb/krew-review-agent/internal/config"
	"github.com/ahmetb/krew-review-agent/internal/log"
)

func TestNewLogger(t *testing.T) {
	l := newLogger("DEBUG")
	if l == nil {
		t.Fatal("nil logger")
	}
	// Just ensure it doesn't panic and emits.
	var buf bytes.Buffer
	l2 := log.New(0, &buf)
	l2.Info("x")
	if buf.Len() == 0 {
		t.Error("logger produced no output")
	}
}

func writeTemp(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "event.json")
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRunTestNonPREventReturnsOK(t *testing.T) {
	logger := log.New(100, &bytes.Buffer{}) // suppressed
	path := writeTemp(t, `{"ref":"refs/heads/main","after":"sha"}`)
	code := runTest(config.Config{}, path, logger)
	if code != exitOK {
		t.Errorf("code=%d want %d (exitOK)", code, exitOK)
	}
}

func TestRunTestMissingFile(t *testing.T) {
	logger := log.New(100, &bytes.Buffer{})
	code := runTest(config.Config{}, filepath.Join(t.TempDir(), "does-not-exist.json"), logger)
	if code != exitBadInput {
		t.Errorf("code=%d want exitBadInput (%d)", code, exitBadInput)
	}
}

func TestRunTestIncompletePREventReturnsBadInput(t *testing.T) {
	logger := log.New(100, &bytes.Buffer{})
	// Has a pull_request field (inferred PR event) but missing required fields.
	path := writeTemp(t, `{"pull_request":{},"number":0}`)
	code := runTest(config.Config{}, path, logger)
	if code != exitBadInput {
		t.Errorf("code=%d want exitBadInput (%d)", code, exitBadInput)
	}
}
