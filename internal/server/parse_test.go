package server

import (
	"encoding/base64"
	"testing"
)

const rawPREvent = `{"action":"opened","number":1,"pull_request":{"number":1,"title":"t","body":"b","head":{"sha":"s"},"user":{"login":"u"}},"repository":{"name":"r","owner":{"login":"o"}}}`

func TestParseRequestBodyRaw(t *testing.T) {
	got, err := ParseRequestBody([]byte(rawPREvent))
	if err != nil {
		t.Fatalf("ParseRequestBody: %v", err)
	}
	if string(got) != rawPREvent {
		t.Errorf("got=%q", got)
	}
}

func TestParseRequestBodyWrappedPubSub(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString([]byte(rawPREvent))
	body := `{"message":{"data":"` + encoded + `","messageId":"m1"},"subscription":"sub"}`
	got, err := ParseRequestBody([]byte(body))
	if err != nil {
		t.Fatalf("ParseRequestBody: %v", err)
	}
	if string(got) != rawPREvent {
		t.Errorf("got=%q want %q", got, rawPREvent)
	}
}

func TestParseRequestBodyEmpty(t *testing.T) {
	if _, err := ParseRequestBody(nil); err == nil {
		t.Fatal("expected error for empty body")
	}
}

func TestParseRequestBodyInvalidJSON(t *testing.T) {
	if _, err := ParseRequestBody([]byte(`{not json`)); err == nil {
		t.Fatal("expected error for invalid json")
	}
}

func TestParseRequestBodyWrappedEmptyData(t *testing.T) {
	body := `{"message":{"data":""},"subscription":"s"}`
	if _, err := ParseRequestBody([]byte(body)); err == nil {
		t.Fatal("expected error for empty message.data")
	}
}

func TestParseRequestBodyWrappedInvalidEnvelope(t *testing.T) {
	// Has a "message" field but it's not a valid envelope object.
	body := `{"message":not-an-object}`
	if _, err := ParseRequestBody([]byte(body)); err == nil {
		t.Fatal("expected error for invalid envelope")
	}
}
