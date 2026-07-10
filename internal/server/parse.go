package server

import (
	"encoding/json"
	"fmt"

	"github.com/ahmetb/krew-review-agent/internal/pubsub"
)

// ParseRequestBody auto-detects whether body is a wrapped Pub/Sub push
// envelope or a raw GitHub webhook event, and returns the raw GitHub event
// JSON bytes (see AGENT_CLI.md §4.2).
//
// Detection: a top-level "message" field indicates a wrapped Pub/Sub envelope;
// its message.data is base64-decoded automatically when unmarshaling into a
// []byte field. Otherwise the body is treated as the raw GitHub event.
func ParseRequestBody(body []byte) ([]byte, error) {
	if len(body) == 0 {
		return nil, fmt.Errorf("empty request body")
	}

	var probe struct {
		Message *json.RawMessage `json:"message"`
	}
	if err := json.Unmarshal(body, &probe); err != nil {
		return nil, fmt.Errorf("parsing request body: %w", err)
	}
	if probe.Message == nil {
		// Raw GitHub webhook event (or unwrapped Pub/Sub / Cloud Tasks body).
		return body, nil
	}

	// Wrapped Pub/Sub push envelope.
	var env pubsub.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("parsing pub/sub envelope: %w", err)
	}
	if len(env.Message.Data) == 0 {
		return nil, fmt.Errorf("pub/sub envelope has empty message.data")
	}
	return env.Message.Data, nil
}
