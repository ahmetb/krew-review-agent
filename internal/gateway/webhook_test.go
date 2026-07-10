package gateway

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func validSignature(secret, body []byte) string {
	mac := hmac.New(sha256.New, secret)
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

func TestVerifySignatureValid(t *testing.T) {
	secret := []byte("my-secret")
	body := []byte(`{"action":"opened"}`)
	sig := validSignature(secret, body)
	if !VerifySignature(secret, body, sig) {
		t.Errorf("expected valid signature to pass")
	}
}

func TestVerifySignatureWrongSecret(t *testing.T) {
	body := []byte(`{"action":"opened"}`)
	sig := validSignature([]byte("correct-secret"), body)
	if VerifySignature([]byte("wrong-secret"), body, sig) {
		t.Errorf("expected signature with wrong secret to fail")
	}
}

func TestVerifySignatureTamperedBody(t *testing.T) {
	secret := []byte("my-secret")
	sig := validSignature(secret, []byte(`{"action":"opened"}`))
	if VerifySignature(secret, []byte(`{"action":"closed"}`), sig) {
		t.Errorf("expected signature with tampered body to fail")
	}
}

func TestVerifySignatureMissingHeader(t *testing.T) {
	if VerifySignature([]byte("s"), []byte("body"), "") {
		t.Errorf("expected empty header to fail")
	}
}

func TestVerifySignatureMalformedHeader(t *testing.T) {
	cases := []string{
		"sha256=nothex!!",
		"sha1=abc123",
		"sha256=",
		"sha256=abc",
	}
	for _, c := range cases {
		if VerifySignature([]byte("s"), []byte("body"), c) {
			t.Errorf("expected malformed header %q to fail", c)
		}
	}
}

func TestVerifySignatureEmptyBody(t *testing.T) {
	secret := []byte("s")
	sig := validSignature(secret, nil)
	if !VerifySignature(secret, nil, sig) {
		t.Errorf("expected valid signature for empty body to pass")
	}
}

func TestParseWebhookPayloadValid(t *testing.T) {
	body := []byte(`{
		"action": "opened",
		"repository": {"full_name": "kubernetes-sigs/krew-index"},
		"pull_request": {"number": 42}
	}`)
	p, err := parseWebhookPayload(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Action != "opened" {
		t.Errorf("action=%q want opened", p.Action)
	}
	if p.Repository.FullName != "kubernetes-sigs/krew-index" {
		t.Errorf("full_name=%q", p.Repository.FullName)
	}
	if p.PullRequest.Number != 42 {
		t.Errorf("pr_number=%d want 42", p.PullRequest.Number)
	}
}

func TestParseWebhookPayloadIgnoresUnknownFields(t *testing.T) {
	body := []byte(`{
		"action": "opened",
		"zen": "keep it awesome",
		"repository": {"full_name": "o/r", "extra": true},
		"pull_request": {"number": 1, "title": "t"}
	}`)
	p, err := parseWebhookPayload(body)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if p.Action != "opened" {
		t.Errorf("action=%q", p.Action)
	}
	if p.PullRequest.Number != 1 {
		t.Errorf("pr_number=%d", p.PullRequest.Number)
	}
}

func TestParseWebhookPayloadInvalidJSON(t *testing.T) {
	if _, err := parseWebhookPayload([]byte(`{not json`)); err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseWebhookPayloadEmptyBody(t *testing.T) {
	if _, err := parseWebhookPayload(nil); err == nil {
		t.Fatal("expected error for empty body")
	}
}
