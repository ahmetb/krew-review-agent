package log

import (
	"bytes"
	"encoding/json"
	"log/slog"
	"strings"
	"testing"
)

func TestParseLevel(t *testing.T) {
	cases := []struct {
		in   string
		want slog.Level
		err  bool
	}{
		{"", slog.LevelInfo, false},
		{"info", slog.LevelInfo, false},
		{"INFO", slog.LevelInfo, false},
		{"debug", slog.LevelDebug, false},
		{"DEBUG", slog.LevelDebug, false},
		{"warn", slog.LevelWarn, false},
		{"WARN", slog.LevelWarn, false},
		{"warning", slog.LevelWarn, false},
		{"error", slog.LevelError, false},
		{"ERROR", slog.LevelError, false},
		{"  WARN  ", slog.LevelWarn, false},
		{"bogus", slog.LevelInfo, true},
	}
	for _, c := range cases {
		got, err := ParseLevel(c.in)
		if (err != nil) != c.err {
			t.Errorf("ParseLevel(%q) err=%v want err=%v", c.in, err, c.err)
		}
		if got != c.want {
			t.Errorf("ParseLevel(%q)=%v want %v", c.in, got, c.want)
		}
	}
}

func TestNewLoggerJSON(t *testing.T) {
	var buf bytes.Buffer
	l := New(slog.LevelDebug, &buf)
	l.Info("hello", "key", "value")
	var rec map[string]any
	if err := json.Unmarshal(buf.Bytes(), &rec); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, buf.String())
	}
	if rec["msg"] != "hello" {
		t.Errorf("msg=%v want hello", rec["msg"])
	}
	if rec["level"] != "INFO" {
		t.Errorf("level=%v want INFO", rec["level"])
	}
	if rec["key"] != "value" {
		t.Errorf("key=%v want value", rec["key"])
	}
}

func TestNewLoggerLevelFiltering(t *testing.T) {
	var buf bytes.Buffer
	l := New(slog.LevelWarn, &buf)
	l.Info("should-be-filtered")
	l.Warn("should-appear")
	if strings.Contains(buf.String(), "should-be-filtered") {
		t.Errorf("INFO logged at WARN level: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "should-appear") {
		t.Errorf("WARN not logged: %s", buf.String())
	}
}

func TestNewTraceIDUnique(t *testing.T) {
	ids := make(map[string]struct{}, 100)
	for i := 0; i < 100; i++ {
		id := NewTraceID()
		if id == "" {
			t.Fatal("empty trace id")
		}
		if _, dup := ids[id]; dup {
			t.Fatalf("duplicate trace id %q", id)
		}
		ids[id] = struct{}{}
	}
}

func TestWithTraceID(t *testing.T) {
	var buf bytes.Buffer
	base := New(slog.LevelInfo, &buf)
	l := WithTraceID(base, "trace-123")
	l.Info("with-trace")
	if !strings.Contains(buf.String(), `"trace_id":"trace-123"`) {
		t.Errorf("trace_id not attached: %s", buf.String())
	}
}

func TestWithTraceIDNilLogger(t *testing.T) {
	// Should not panic when given a nil logger.
	l := WithTraceID(nil, "x")
	if l == nil {
		t.Fatal("expected non-nil logger")
	}
	l.Info("no-op")
}
