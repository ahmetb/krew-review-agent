// Package pubsub defines the wrapped Pub/Sub push envelope types.
//
// When a Pub/Sub subscription is configured with default (non-unwrapping) push
// delivery, the request body posted to the endpoint has the shape described by
// Envelope. The Message.Data field is base64-encoded; unmarshaling into a
// []byte field performs the decode automatically, so no explicit base64 step is
// required by callers.
package pubsub

// Envelope is the top-level body of a wrapped Pub/Sub push delivery.
type Envelope struct {
	Message      Message `json:"message"`
	Subscription string  `json:"subscription,omitempty"`
}

// Message is the inner Pub/Sub message.
type Message struct {
	// Data holds the base64-encoded payload. encoding/json decodes base64
	// automatically when unmarshaling into a []byte field.
	Data []byte `json:"data"`

	// MessageID is the Pub/Sub-assigned identifier.
	MessageID string `json:"messageId,omitempty"`

	// PublishTime is the RFC3339 timestamp the message was published.
	PublishTime string `json:"publishTime,omitempty"`

	// Attributes carries optional message attributes (e.g. the GitHub event
	// type when forwarded by the fast-ACK receiver).
	Attributes map[string]string `json:"attributes,omitempty"`
}
