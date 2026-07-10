package pubsub

import (
	"encoding/base64"
	"encoding/json"
	"testing"
)

func TestEnvelopeBase64AutoDecode(t *testing.T) {
	raw := `{"hello":"world"}`
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	body := `{"message":{"data":"` + encoded + `","messageId":"msg-1"},"subscription":"projects/p/subscriptions/s"}`

	var env Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Subscription != "projects/p/subscriptions/s" {
		t.Errorf("subscription=%q", env.Subscription)
	}
	if env.Message.MessageID != "msg-1" {
		t.Errorf("messageId=%q", env.Message.MessageID)
	}
	if string(env.Message.Data) != raw {
		t.Errorf("data=%q want %q", env.Message.Data, raw)
	}
}

func TestEnvelopeEmptyData(t *testing.T) {
	var env Envelope
	if err := json.Unmarshal([]byte(`{"message":{"data":""}}`), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if len(env.Message.Data) != 0 {
		t.Errorf("expected empty data, got %q", env.Message.Data)
	}
}

func TestEnvelopeAttributes(t *testing.T) {
	body := `{"message":{"data":"aGk=","attributes":{"event":"pull_request"}}}`
	var env Envelope
	if err := json.Unmarshal([]byte(body), &env); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if env.Message.Attributes["event"] != "pull_request" {
		t.Errorf("attributes=%v", env.Message.Attributes)
	}
}
