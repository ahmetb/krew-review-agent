// Package publisher provides the GCP Pub/Sub implementation of the
// gateway.Publisher interface. It isolates the heavy cloud.google.com/go/pubsub
// dependency from the gateway package, which depends only on the interface.
package publisher

import (
	"context"
	"fmt"

	"cloud.google.com/go/pubsub/v2"
)

// GCPPublisher publishes messages to a GCP Pub/Sub topic using the official
// Pub/Sub SDK. It implements the gateway.Publisher interface.
type GCPPublisher struct {
	client    *pubsub.Client
	publisher *pubsub.Publisher
}

// NewGCPPublisher wraps an existing Pub/Sub client and targets the named topic
// for publishing. The caller retains ownership of the client and is responsible
// for closing it (or delegate cleanup to GCPPublisher.Close which closes both).
//
// The topic is expected to already exist (EVENT_GATEWAY.md §5.1). The gateway
// does not create topics on startup.
func NewGCPPublisher(client *pubsub.Client, topicName string) *GCPPublisher {
	return &GCPPublisher{
		client:    client,
		publisher: client.Publisher(topicName),
	}
}

// Publish sends a single message to the target Pub/Sub topic and blocks until
// the publish completes. On success it returns the server-assigned message ID.
func (p *GCPPublisher) Publish(ctx context.Context, data []byte, attributes map[string]string) (string, error) {
	result := p.publisher.Publish(ctx, &pubsub.Message{
		Data:       data,
		Attributes: attributes,
	})
	id, err := result.Get(ctx)
	if err != nil {
		return "", fmt.Errorf("publishing to pubsub: %w", err)
	}
	return id, nil
}

// Close stops the publisher's internal bundling goroutines and closes the
// underlying Pub/Sub client. After Close, subsequent Publish calls will fail.
func (p *GCPPublisher) Close() error {
	p.publisher.Stop()
	return p.client.Close()
}
