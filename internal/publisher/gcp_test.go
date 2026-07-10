package publisher

import (
	"context"
	"testing"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"github.com/ahmetb/krew-review-agent/internal/gateway"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

// newTestClient creates a Pub/Sub client connected to an in-memory pstest
// server. The server and connection are cleaned up via t.Cleanup.
func newTestClient(t *testing.T, projectID string) (*pubsub.Client, *pstest.Server) {
	t.Helper()
	ctx := context.Background()

	srv := pstest.NewServer()
	conn, err := grpc.Dial(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		srv.Close()
		t.Fatalf("dialing pstest: %v", err)
	}

	client, err := pubsub.NewClient(ctx, projectID, option.WithGRPCConn(conn))
	if err != nil {
		conn.Close()
		srv.Close()
		t.Fatalf("creating pubsub client: %v", err)
	}

	t.Cleanup(func() {
		client.Close()
		conn.Close()
		srv.Close()
	})
	return client, srv
}

// createTopic creates a topic in the test server.
func createTopic(t *testing.T, client *pubsub.Client, projectID, topicName string) {
	t.Helper()
	ctx := context.Background()
	fullName := "projects/" + projectID + "/topics/" + topicName
	if _, err := client.TopicAdminClient.CreateTopic(ctx, &pubsubpb.Topic{
		Name: fullName,
	}); err != nil {
		t.Fatalf("creating topic: %v", err)
	}
}

func TestGCPPublisherPublishSuccess(t *testing.T) {
	const (
		projectID = "test-project"
		topicName = "github-pr-events"
	)
	client, srv := newTestClient(t, projectID)
	createTopic(t, client, projectID, topicName)

	pub := NewGCPPublisher(client, topicName)
	t.Cleanup(func() { pub.Close() })

	data := []byte(`{"action":"opened","pull_request":{"number":1}}`)
	attrs := map[string]string{
		"X-GitHub-Event":    "pull_request",
		"X-GitHub-Delivery": "del-1",
		"github-action":     "opened",
	}

	id, err := pub.Publish(context.Background(), data, attrs)
	if err != nil {
		t.Fatalf("Publish failed: %v", err)
	}
	if id == "" {
		t.Errorf("expected non-empty message ID")
	}

	// Verify the message was received by the server.
	msgs := srv.Messages()
	if len(msgs) != 1 {
		t.Fatalf("server received %d messages, want 1", len(msgs))
	}
	if string(msgs[0].Data) != string(data) {
		t.Errorf("message data=%q want %q", msgs[0].Data, data)
	}
	if msgs[0].Attributes["X-GitHub-Event"] != "pull_request" {
		t.Errorf("attr X-GitHub-Event=%q", msgs[0].Attributes["X-GitHub-Event"])
	}
	if msgs[0].Attributes["X-GitHub-Delivery"] != "del-1" {
		t.Errorf("attr X-GitHub-Delivery=%q", msgs[0].Attributes["X-GitHub-Delivery"])
	}
	if msgs[0].Attributes["github-action"] != "opened" {
		t.Errorf("attr github-action=%q", msgs[0].Attributes["github-action"])
	}
}

func TestGCPPublisherMultipleMessages(t *testing.T) {
	const (
		projectID = "test-project"
		topicName = "events"
	)
	client, srv := newTestClient(t, projectID)
	createTopic(t, client, projectID, topicName)

	pub := NewGCPPublisher(client, topicName)
	t.Cleanup(func() { pub.Close() })

	for i := 0; i < 3; i++ {
		_, err := pub.Publish(context.Background(), []byte("msg"), nil)
		if err != nil {
			t.Fatalf("Publish %d failed: %v", i, err)
		}
	}

	if len(srv.Messages()) != 3 {
		t.Errorf("server received %d messages, want 3", len(srv.Messages()))
	}
}

func TestGCPPublisherSatisfiesInterface(t *testing.T) {
	// Compile-time assertion that GCPPublisher implements gateway.Publisher.
	var _ gateway.Publisher = (*GCPPublisher)(nil)
}
