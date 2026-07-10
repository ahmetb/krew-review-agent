// Package gateway implements the Event Gateway (Fast-ACK Receiver), a thin,
// stateless HTTP service that receives GitHub webhook deliveries, verifies
// their HMAC signatures, filters for pull_request-opened events from the
// allowed repository, and publishes matching payloads to a GCP Pub/Sub topic.
//
// See design/EVENT_GATEWAY.md for the full specification.
package gateway

import (
	"fmt"
	"strconv"
)

// DefaultPort is the HTTP listen port when PORT is unset.
const DefaultPort = 8080

// AllowedRepository is the hardcoded repository allow-list entry. Only webhooks
// from this repository are accepted (EVENT_GATEWAY.md §3.3).
const AllowedRepository = "kubernetes-sigs/krew-index"

// Config holds all runtime settings for the Event Gateway binary.
type Config struct {
	Port                       int
	GitHubWebhookSecret        string
	DisableWebhookVerification bool
	GCPProjectID               string
	PubSubTopic                string
}

// Load reads configuration using getenv (typically os.LookupEnv). Required
// fields (GCP_PROJECT_ID, PUBSUB_TOPIC) produce an error when missing.
// GITHUB_WEBHOOK_SECRET is required when webhook verification is enabled
// (i.e. DISABLE_WEBHOOK_VERIFICATION is not true).
func Load(getenv func(string) (string, bool)) (Config, error) {
	var cfg Config
	var missing []string

	if v, ok := getenv("PORT"); ok && v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 65535 {
			return Config{}, fmt.Errorf("PORT must be a valid TCP port, got %q", v)
		}
		cfg.Port = n
	} else {
		cfg.Port = DefaultPort
	}

	if v, ok := getenv("DISABLE_WEBHOOK_VERIFICATION"); ok && v != "" {
		b, err := strconv.ParseBool(v)
		if err != nil {
			return Config{}, fmt.Errorf("DISABLE_WEBHOOK_VERIFICATION must be a boolean, got %q", v)
		}
		cfg.DisableWebhookVerification = b
	}

	if v, ok := getenv("GITHUB_WEBHOOK_SECRET"); ok && v != "" {
		cfg.GitHubWebhookSecret = v
	} else if !cfg.DisableWebhookVerification {
		missing = append(missing, "GITHUB_WEBHOOK_SECRET")
	}

	if v, ok := getenv("GCP_PROJECT_ID"); ok && v != "" {
		cfg.GCPProjectID = v
	} else {
		missing = append(missing, "GCP_PROJECT_ID")
	}

	if v, ok := getenv("PUBSUB_TOPIC"); ok && v != "" {
		cfg.PubSubTopic = v
	} else {
		missing = append(missing, "PUBSUB_TOPIC")
	}

	if len(missing) > 0 {
		return cfg, fmt.Errorf("required environment variables not set: %v", missing)
	}
	return cfg, nil
}
