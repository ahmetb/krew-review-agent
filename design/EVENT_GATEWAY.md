# Engineering Specification: Event Gateway (Fast-ACK Receiver)

## 1. Overview & Motivation

The **Event Gateway** is a thin, stateless HTTP service that sits at the front of the `krew-review-agent` pipeline. Its sole purpose is to receive GitHub webhook deliveries, perform minimal validation, and fast-ACK them into a GCP Pub/Sub topic for asynchronous processing by the agent worker.

By decoupling webhook intake from the expensive LLM review logic, the gateway ensures GitHub's ~10s webhook delivery timeout is never at risk and provides a durable buffer between event ingestion and review execution. This service implements the "Fast-ACK Receiver" component referenced in `HIGH_LEVEL_DESIGN.md` §2.

## 2. Architectural Context

The end-to-end pipeline is:

```
GitHub Webhook ──▶ Event Gateway (Cloud Run) ──▶ Pub/Sub Topic ──▶ [Subscription] ──▶ Agent Worker (Cloud Run)
```

**In scope for this document:**
- The Event Gateway service (HTTP intake → signature verification → filtering → publish).
- The Pub/Sub topic topology and message contract.

**Out of scope for this document:**
- The Pub/Sub subscription and Cloud Run push invocation wiring that delivers messages to the agent worker. This will be covered in a separate design document.

The gateway is deployed as a Cloud Run service running a Go binary. It scales to zero and autoscales on HTTP request volume.

## 3. Service Interface

### 3.1 HTTP Endpoints

* `POST /webhook` — receives GitHub webhook deliveries.

### 3.2 Environment Variables

* `PORT` (Default: `8080`) — the port the HTTP server listens on.
* `GITHUB_WEBHOOK_SECRET` — the HMAC-SHA256 secret used to verify the `X-Hub-Signature-256` header.
* `DISABLE_WEBHOOK_VERIFICATION` (Default: `false`) — when set to `true`, skips HMAC signature verification. Intended for local development only.
* `GCP_PROJECT_ID` — the GCP project ID hosting the Pub/Sub topic.
* `PUBSUB_TOPIC` — the target Pub/Sub topic name (e.g. `github-pr-events`).

### 3.3 Hardcoded Configuration

* **Allowed Repository:** `kubernetes-sigs/krew-index`. The gateway only accepts webhooks originating from this repository. Any payload whose `repository.full_name` does not match is rejected with `403`.

## 4. Request Processing Flow

The `POST /webhook` handler executes the following ordered steps:

### Step 1: Read Raw Body

Read the complete raw request body into memory. The raw bytes are required for HMAC verification before any JSON parsing occurs (re-serializing parsed JSON would produce a different byte sequence and fail signature validation).

### Step 2: Signature Verification

Unless `DISABLE_WEBHOOK_VERIFICATION` is `true`:

1. Extract the `X-Hub-Signature-256` header (format: `sha256=<hex>`).
2. Compute `HMAC-SHA256` of the raw body using `GITHUB_WEBHOOK_SECRET`.
3. Compare the computed digest against the header value using `hmac.Equal` (constant-time comparison to prevent timing attacks).
4. On mismatch or missing header: respond `401`, log the outcome, and return.

### Step 3: Event Type Filter

Inspect the `X-GitHub-Event` request header. If it is not `pull_request`, respond `202`, log the outcome, and return. A `202` (not an error) is used because GitHub may deliver other event types if the webhook configuration is broadened; these are intentionally filtered, not failed.

### Step 4: Parse Payload

Unmarshal the raw body as JSON. Extract:
* `action` (string)
* `repository.full_name` (string)
* `pull_request.number` (int)

On parse failure: respond `400`, log the outcome, and return.

### Step 5: Action Filter

If `action` is not `opened`, respond `202`, log the outcome, and return.

### Step 6: Repository Validation

Compare `repository.full_name` against the hardcoded allowed repository (`kubernetes-sigs/krew-index`). On mismatch: respond `403`, log the outcome, and return.

### Step 7: Publish to Pub/Sub

Publish a single message to the target topic:
* **Data:** the raw GitHub webhook JSON body (unmodified passthrough).
* **Attributes:**
  * `X-GitHub-Event` — the event type (e.g. `pull_request`).
  * `X-GitHub-Delivery` — the GUID from the `X-GitHub-Delivery` request header. This ID is stable across manual redeliveries of the same logical event.
  * `github-action` — the action value (e.g. `opened`).

On success: respond `200`, log the outcome, and return.
On failure (transient error, timeout): respond `500`, log the outcome, and return. Returning a non-2xx status causes GitHub to retry the webhook delivery per its own backoff schedule.

## 5. Pub/Sub Topology

### 5.1 Topic

* **Name:** configurable via `PUBSUB_TOPIC` (e.g. `github-pr-events`).
* **Provisioning:** external to the gateway. The topic is provisioned via Terraform, `gcloud`, or the Google Cloud Console. The gateway does **not** create the topic on startup; it expects the topic to already exist.
* **Project:** the GCP project hosting the topic is specified via `GCP_PROJECT_ID`.

### 5.2 Message Contract

* **Data:** raw GitHub webhook JSON payload (passthrough). The agent worker consumes this directly via `stdin` as specified in `HIGH_LEVEL_DESIGN.md` §2.
* **Attributes:**
  | Attribute | Source | Purpose |
  |---|---|---|
  | `X-GitHub-Event` | `X-GitHub-Event` request header | Event type for downstream routing/filtering |
  | `X-GitHub-Delivery` | `X-GitHub-Delivery` request header | Stable GUID for the logical event; identical across redeliveries |
  | `github-action` | `action` field in payload | The action that triggered the event |

### 5.3 Topology Diagram

```
GCP Project
└── Topic: github-pr-events
    ├── (provisioned externally — Terraform/gcloud)
    └── [Subscription → Agent Worker Cloud Run]  ← covered in a separate design doc
```

### 5.4 Deduplication

The gateway performs **no** deduplication. GitHub redeliveries of the same logical event share the same `X-GitHub-Delivery` GUID, but publishing them to Pub/Sub produces distinct messages with distinct `messageId`s (Pub/Sub does not dedup based on content or custom attributes). The `X-GitHub-Delivery` GUID is attached as a message attribute so that the downstream agent worker *may* use it for idempotent processing if desired.

## 6. Error Handling & HTTP Response Codes

| Outcome | HTTP Status | Published? |
|---|---|---|
| Valid event, published successfully | `200` | Yes |
| Non-matching event type or action (filtered) | `202` | No |
| Bad or missing signature | `401` | No |
| Wrong repository | `403` | No |
| Malformed payload (JSON parse failure) | `400` | No |
| Pub/Sub publish failure | `500` | No (GitHub retries) |

Returning a non-2xx status for publish failures leverages GitHub's built-in webhook retry mechanism. The gateway does not implement its own retry logic for Pub/Sub publish failures.

## 7. Local Development

* Set `DISABLE_WEBHOOK_VERIFICATION=true` to skip the HMAC check (no `GITHUB_WEBHOOK_SECRET` needed).
* Run the Pub/Sub emulator locally:
  ```
  gcloud beta emulators pubsub start --host-port=localhost:8081
  ```
  Set `PUBSUB_EMULATOR_HOST=localhost:8081` and create the topic in the emulator before running the gateway.
* Run the gateway: `go run ./cmd/gateway`.
* Deliver sample payloads via `curl`:
  ```
  curl -X POST http://localhost:8080/webhook \
    -H "X-GitHub-Event: pull_request" \
    -H "X-GitHub-Delivery: <guid>" \
    -H "Content-Type: application/json" \
    -d @sample-pr-opened.json
  ```

## 8. Observability

The gateway emits structured JSON logs using `log/slog`, consistent with the agent worker.

**Log fields:**
* `delivery_id` — the `X-GitHub-Delivery` GUID.
* `event_type` — the `X-GitHub-Event` value.
* `action` — the `action` from the payload.
* `repo` — `repository.full_name` from the payload.
* `pr_number` — the PR number from the payload.
* `outcome` — one of: `published`, `filtered`, `unauthorized`, `forbidden`, `bad_request`, `publish_failed`.
* `publish_latency_ms` — time spent on the Pub/Sub publish call (when applicable).
* `error` — error message (when applicable).

No custom Cloud Monitoring metrics are emitted in v1. These can be added in a future iteration.

## 9. Security

* **HMAC Signature Verification:** prevents spoofed webhook deliveries. Verification uses constant-time comparison (`hmac.Equal`) to prevent timing attacks.
* **Repository Allow-List:** the gateway rejects webhooks from any repository other than `kubernetes-sigs/krew-index`, preventing cross-repo event injection even if the webhook secret is shared.
* **Secret Management:** `GITHUB_WEBHOOK_SECRET` is provided via environment variable and should be sourced from Secret Manager in production. It is never logged.
* **Statelessness:** the gateway persists no data. The raw request body is forwarded to Pub/Sub and discarded. No sensitive information is written to disk.
