package gateway

import "context"

// Publisher is the abstraction over the Pub/Sub publish operation. The gateway
// handler depends on this interface rather than a concrete GCP client so that
// tests can substitute a fake without touching real infrastructure.
type Publisher interface {
	// Publish sends a single message with the given data bytes and attributes
	// to the target Pub/Sub topic. It blocks until the publish completes and
	// returns the server-assigned message ID on success.
	Publish(ctx context.Context, data []byte, attributes map[string]string) (string, error)

	// Close releases any resources associated with the publisher (e.g. the
	// underlying Pub/Sub client and bundled publish goroutines).
	Close() error
}
