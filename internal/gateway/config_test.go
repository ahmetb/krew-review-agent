package gateway

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
		"GITHUB_WEBHOOK_SECRET": "secret",
		"GCP_PROJECT_ID":        "my-project",
		"PUBSUB_TOPIC":          "github-pr-events",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != DefaultPort {
		t.Errorf("port=%d want %d", cfg.Port, DefaultPort)
	}
	if cfg.DisableWebhookVerification {
		t.Errorf("disable_verification=true, want false by default")
	}
	if cfg.GitHubWebhookSecret != "secret" {
		t.Errorf("secret=%q", cfg.GitHubWebhookSecret)
	}
	if cfg.GCPProjectID != "my-project" {
		t.Errorf("project=%q", cfg.GCPProjectID)
	}
	if cfg.PubSubTopic != "github-pr-events" {
		t.Errorf("topic=%q", cfg.PubSubTopic)
	}
}

func TestLoadOverrides(t *testing.T) {
	cfg, err := Load(getenvFrom(map[string]string{
		"PORT":                       "9000",
		"GITHUB_WEBHOOK_SECRET":      "s",
		"DISABLE_WEBHOOK_VERIFICATION": "true",
		"GCP_PROJECT_ID":             "proj",
		"PUBSUB_TOPIC":               "events",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.Port != 9000 {
		t.Errorf("port=%d want 9000", cfg.Port)
	}
	if !cfg.DisableWebhookVerification {
		t.Errorf("disable_verification=false, want true")
	}
}

func TestLoadMissingRequiredProjectAndTopic(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{
		"GITHUB_WEBHOOK_SECRET": "s",
	}))
	if err == nil {
		t.Fatal("expected error for missing GCP_PROJECT_ID and PUBSUB_TOPIC")
	}
	for _, want := range []string{"GCP_PROJECT_ID", "PUBSUB_TOPIC"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q should mention %q", err.Error(), want)
		}
	}
}

func TestLoadSecretRequiredWhenVerificationEnabled(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{
		"GCP_PROJECT_ID": "p",
		"PUBSUB_TOPIC":   "t",
	}))
	if err == nil {
		t.Fatal("expected error for missing GITHUB_WEBHOOK_SECRET")
	}
	if !strings.Contains(err.Error(), "GITHUB_WEBHOOK_SECRET") {
		t.Errorf("error should mention GITHUB_WEBHOOK_SECRET: %q", err.Error())
	}
}

func TestLoadSecretNotRequiredWhenVerificationDisabled(t *testing.T) {
	cfg, err := Load(getenvFrom(map[string]string{
		"DISABLE_WEBHOOK_VERIFICATION": "true",
		"GCP_PROJECT_ID":               "p",
		"PUBSUB_TOPIC":                 "t",
	}))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.GitHubWebhookSecret != "" {
		t.Errorf("secret=%q, want empty", cfg.GitHubWebhookSecret)
	}
}

func TestLoadEmptyStringTreatedAsMissing(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{
		"GITHUB_WEBHOOK_SECRET": "",
		"GCP_PROJECT_ID":        "p",
		"PUBSUB_TOPIC":          "t",
	}))
	if err == nil {
		t.Fatal("expected error for empty GITHUB_WEBHOOK_SECRET")
	}
}

func TestLoadInvalidPort(t *testing.T) {
	cases := []string{"0", "-1", "abc", "99999"}
	for _, c := range cases {
		_, err := Load(getenvFrom(map[string]string{
			"GITHUB_WEBHOOK_SECRET": "s",
			"GCP_PROJECT_ID":        "p",
			"PUBSUB_TOPIC":          "t",
			"PORT":                  c,
		}))
		if err == nil {
			t.Errorf("PORT=%q expected error", c)
		}
	}
}

func TestLoadInvalidDisableVerification(t *testing.T) {
	_, err := Load(getenvFrom(map[string]string{
		"DISABLE_WEBHOOK_VERIFICATION": "maybe",
		"GCP_PROJECT_ID":               "p",
		"PUBSUB_TOPIC":                 "t",
	}))
	if err == nil {
		t.Fatal("expected error for invalid boolean")
	}
}
