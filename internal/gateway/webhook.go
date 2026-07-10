package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
)

// signaturePrefix is the expected prefix of the X-Hub-Signature-256 header.
const signaturePrefix = "sha256="

// VerifySignature validates the X-Hub-Signature-256 header against the raw
// request body using HMAC-SHA256. Comparison is constant-time via hmac.Equal
// to prevent timing attacks (EVENT_GATEWAY.md §4 Step 2, §9).
//
// Returns false if the header is missing or malformed.
func VerifySignature(secret, body []byte, signatureHeader string) bool {
	if !strings.HasPrefix(signatureHeader, signaturePrefix) {
		return false
	}
	expectedMAC, err := hex.DecodeString(signatureHeader[len(signaturePrefix):])
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return hmac.Equal(mac.Sum(nil), expectedMAC)
}

// webhookPayload is the minimal subset of a GitHub pull_request webhook payload
// extracted by the gateway (EVENT_GATEWAY.md §4 Step 4).
type webhookPayload struct {
	Action      string        `json:"action"`
	Repository  repoPayload   `json:"repository"`
	PullRequest prPayload     `json:"pull_request"`
}

type repoPayload struct {
	FullName string `json:"full_name"`
}

type prPayload struct {
	Number int `json:"number"`
}

// parseWebhookPayload unmarshals the raw GitHub webhook body into a
// webhookPayload. Unknown fields are ignored.
func parseWebhookPayload(body []byte) (webhookPayload, error) {
	var p webhookPayload
	if err := json.Unmarshal(body, &p); err != nil {
		return webhookPayload{}, fmt.Errorf("parsing webhook payload: %w", err)
	}
	return p, nil
}
