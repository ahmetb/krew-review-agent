// Command gateway is the Event Gateway (Fast-ACK Receiver). It is a thin,
// stateless HTTP service that receives GitHub webhook deliveries, verifies
// their HMAC signatures, filters for pull_request:opened events from the
// allowed repository (kubernetes-sigs/krew-index), and publishes matching
// payloads to a GCP Pub/Sub topic for asynchronous processing by the agent
// worker.
//
// See design/EVENT_GATEWAY.md for the full specification.
package main

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"cloud.google.com/go/pubsub/v2"

	"github.com/ahmetb/krew-review-agent/internal/gateway"
	"github.com/ahmetb/krew-review-agent/internal/log"
	"github.com/ahmetb/krew-review-agent/internal/publisher"
)

// Exit code constants.
const (
	exitOK       = 0
	exitFailure  = 1
	exitConfig   = 2
)

func main() {
	os.Exit(run())
}

func run() int {
	cfg, err := gateway.Load(os.LookupEnv)
	logger := log.New(slog.LevelInfo, os.Stderr)

	if err != nil {
		logger.Error("configuration error", "error", err)
		return exitConfig
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	client, err := pubsub.NewClient(ctx, cfg.GCPProjectID)
	if err != nil {
		logger.Error("creating pubsub client", "error", err, "project", cfg.GCPProjectID)
		return exitFailure
	}

	pub := publisher.NewGCPPublisher(client, cfg.PubSubTopic)
	defer func() {
		if err := pub.Close(); err != nil {
			logger.Error("closing publisher", "error", err)
		}
	}()

	deps := gateway.Deps{
		Secret:              []byte(cfg.GitHubWebhookSecret),
		DisableVerification: cfg.DisableWebhookVerification,
		AllowedRepo:         gateway.AllowedRepository,
		Publisher:           pub,
		Logger:              logger,
	}

	addr := fmt.Sprintf(":%d", cfg.Port)
	srv := gateway.New(deps, addr)

	logger.Info("starting event gateway",
		"addr", addr,
		"topic", cfg.PubSubTopic,
		"project", cfg.GCPProjectID,
		"verify_webhooks", !cfg.DisableWebhookVerification,
	)
	if err := srv.ListenAndServe(ctx); err != nil {
		logger.Error("server exited with error", "error", err)
		return exitFailure
	}
	return exitOK
}
