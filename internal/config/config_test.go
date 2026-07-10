package config

import (
	"strings"
	"testing"
)

func getenvFrom(m map[string]string) func(string) (string, bool) {
	return func(k string) (string, bool) {
		v, ok := m[k]
		return v, ok
	}
}

func TestLoadDefaults(t *testing.T) {
	cfg, err := Load(getenvFrom(map[string]string{
		"GITHUB_TOKEN":  "ghp_x",
		"LLM_API_KEY":   "k",
		"LLM_BASE_URL":  "https://gw",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLMModel != DefaultLLMModel {
		t.Errorf("model=%q want %q", cfg.LLMModel, DefaultLLMModel)
	}
	if cfg.MaxIterations != DefaultMaxIter {
		t.Errorf("max_iter=%d want %d", cfg.MaxIterations, DefaultMaxIter)
	}
	if cfg.LogLevel != DefaultLogLevel {
		t.Errorf("log_level=%q want %q", cfg.LogLevel, DefaultLogLevel)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("port=%d want %d", cfg.Port, DefaultPort)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(getenvFrom(map[string]string{
		"GITHUB_TOKEN":   "ghp_x",
		"LLM_API_KEY":    "k",
		"LLM_BASE_URL":   "https://gw",
		"LLM_MODEL":      "kimi",
		"MAX_ITERATIONS": "12",
		"LOG_LEVEL":      "debug",
		"PORT":           "9000",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.LLMModel != "kimi" {
		t.Errorf("model=%q", cfg.LLMModel)
	}
	if cfg.MaxIterations != 12 {
		t.Errorf("max_iter=%d", cfg.MaxIterations)
	}
	if cfg.LogLevel != "debug" {
		t.Errorf("log_level=%q", cfg.LogLevel)
	}
	if cfg.Port != 9000 {
		t.Errorf("port=%d", cfg.Port)
	}
}

func TestLoadMissingRequired(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{}))
	if err == nil {
		t.Fatal("expected error for missing required vars")
	}
	for _, want := range []string{"GITHUB_TOKEN", "LLM_API_KEY", "LLM_BASE_URL"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestLoadEmptyStringTreatedAsMissing(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{
		"GITHUB_TOKEN": "",
		"LLM_API_KEY":  "k",
		"LLM_BASE_URL": "u",
	}))
	if err == nil {
		t.Fatal("expected error for empty GITHUB_TOKEN")
	}
}

func TestLoadInvalidMaxIterations(t *testing.T) {
	cases := []string{"0", "-1", "abc", "1.5"}
	for _, c := range cases {
		_, err := Load(getenvFrom(map[string]string{
			"GITHUB_TOKEN":   "t",
			"LLM_API_KEY":    "k",
			"LLM_BASE_URL":   "u",
			"MAX_ITERATIONS": c,
		}))
		if err == nil {
			t.Errorf("MAX_ITERATIONS=%q expected error", c)
		}
	}
}

func TestLoadInvalidPort(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{
		"GITHUB_TOKEN": "t",
		"LLM_API_KEY":  "k",
		"LLM_BASE_URL": "u",
		"PORT":         "99999",
	}))
	if err == nil {
		t.Errorf("invalid PORT expected error")
	}
}
