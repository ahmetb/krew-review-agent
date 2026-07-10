# Deployment Specification: Event Gateway on Cloud Run

This document describes the production deployment topology and configuration
for the `cmd/gateway` binary (the Event Gateway / Fast-ACK Receiver) on Google
Cloud Run. It is a companion to
[`EVENT_GATEWAY.md`](./EVENT_GATEWAY.md) (binary specification) and
[`AGENT_CLI_DEPLOYMENT.md`](./AGENT_CLI_DEPLOYMENT.md) (agent worker
deployment).

---

## 1. High-Level Overview

The krew-review-agent pipeline consists of two Cloud Run services and a
Pub/Sub topic that connects them:

```
 GitHub (kubernetes-sigs/krew-index)
   │
   │ POST /webhook  (X-Hub-Signature-256, X-GitHub-Event, raw JSON body)
   ▼
 ┌──────────────────────────────┐
 │  Event Gateway               │  Cloud Run (public ingress)
 │  krew-review-event-gateway   │  - verifies HMAC-SHA256 signature
 │  POST /webhook               │  - filters: pull_request + opened + repo
 │                               │  - publishes raw body to Pub/Sub
 └──────────┬───────────────────┘
            │
            │ Publish (data = raw webhook JSON, attrs = event/delivery/action)
            ▼
 ┌──────────────────────────────┐
 │  Pub/Sub Topic               │  krew-index-github-events
 │  (provisioned by agent dep)  │  durable buffer; decouples intake from review
 └──────────┬───────────────────┘
            │
            │ Authenticated push (OIDC token from krew-review-agent-invoker SA)
            ▼
 ┌──────────────────────────────┐
 │  Agent Worker                │  Cloud Run (internal ingress)
 │  krew-review-agent           │  POST /pubsub
 │  (see AGENT_CLI_DEPLOYMENT)  │  runs the LLM review orchestration loop
 └──────────────────────────────┘
```

### Why two services?

GitHub enforces a ~10-second webhook delivery timeout. The agent worker's
review loop takes minutes (LLM inference, git clone, GitHub API calls). If
the webhook delivery and the review ran in the same service, GitHub would
time out and retry before the review even started. The Event Gateway
fast-ACKs each webhook into Pub/Sub in milliseconds, then returns `200`
well within the timeout. The Pub/Sub topic acts as a durable buffer, and
the push subscription delivers each message to the agent worker
asynchronously with its own retry semantics.

### What the gateway does (and doesn't do)

| Does | Doesn't do |
|---|---|
| Verify the HMAC-SHA256 webhook signature | Run any LLM calls |
| Filter for `pull_request:opened` from `kubernetes-sigs/krew-index` | Clone any git repos |
| Publish the raw webhook JSON to Pub/Sub | Deduplicate events |
| Return `200` within ~10s of receipt | Retry Pub/Sub publish failures (relies on GitHub retry via non-2xx) |

### Shared infrastructure

The Pub/Sub topic `krew-index-github-events` is provisioned by
`AGENT_CLI_DEPLOYMENT.md` §6.6. The gateway expects it to already exist
(`EVENT_GATEWAY.md` §5.1). The gateway only needs publish permission on
this topic.

---

## 2. Deployment Topology

```
                         GCP Project: ahmet-personal-api
                         Region: us-central1

  ┌─────────────────────┐     ┌─────────────────────────────────────┐
  │  GitHub             │     │  Pub/Sub Topic                       │
  │  kubernetes-sigs/   │     │  krew-index-github-events            │
  │  krew-index         │     │  (provisioned by agent deployment)   │
  │  webhook ───────────┼────▶│                                      │
  │  (Payload URL =     │     │  Subscription:                       │
  │   gateway URL +     │     │  krew-index-github-events-to-agent   │────▶ Agent Worker
  │   /webhook)         │     │  (provisioned by agent deployment)   │      (krew-review-agent)
  └─────────────────────┘     └─────────────────────────────────────┘
          │                            ▲
          │ POST /webhook               │ roles/pubsub.publisher
          │ (HMAC-signed)               │ on krew-index-github-events
          ▼                            │
  ┌──────────────────────────────────────────────────┐
  │  Event Gateway (Cloud Run)                        │
  │  Service: krew-review-event-gateway               │
  │  URL:    https://krew-review-event-gateway-       │
  │          1075231961184.us-central1.run.app        │
  │  Image:  …/krew-review-agent/gateway              │
  │  Ingress: all (public — GitHub must reach it)     │
  │  Auth: allow-unauthenticated (HMAC is the auth)   │
  │  SA: krew-review-event-gateway@…                  │
  └──────────────────────────────────────────────────┘
```

### Components

| Component | Type | Name | Provisioned by |
|---|---|---|---|
| Container image | Artifact Registry (Docker) | `us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/gateway` | This doc |
| Gateway service | Cloud Run (managed) | `krew-review-event-gateway` | This doc |
| Runtime service account | IAM | `krew-review-event-gateway@ahmet-personal-api.iam.gserviceaccount.com` | This doc |
| Pub/Sub topic | Pub/Sub | `krew-index-github-events` | Agent deployment (`AGENT_CLI_DEPLOYMENT.md` §6.6) |
| Push subscription | Pub/Sub | `krew-index-github-events-to-agent` | Agent deployment (`AGENT_CLI_DEPLOYMENT.md` §6.8) |
| GitHub webhook | GitHub (kubernetes-sigs/krew-index) | `krew-review-event-gateway` webhook | This doc (manual UI step) |

---

## 3. Cloud Run Service Configuration

### 3.1 Service settings

| Setting | Value | Rationale |
|---|---|---|
| Service name | `krew-review-event-gateway` | |
| Region | `us-central1` | Matches existing project services |
| Ingress | `all` | GitHub sends webhooks from outside GCP; the endpoint must be publicly reachable. HMAC signature verification is the auth layer. |
| Authentication | `--allow-unauthenticated` | The gateway is a public webhook endpoint. Anyone can POST to it, but only valid HMAC-signed payloads from the allowed repo are processed; all others are rejected (401/403/202). |
| Container port | `8080` (default `$PORT`) | Cloud Run contract; binary reads `$PORT`. |
| CPU | `1000m` | Cloud Run requires CPU >= 1 when concurrency > 1. The gateway is sub-second so actual usage is negligible; cost impact is minimal with scale-to-zero. |
| Memory | `128Mi` | No git clone, no LLM response buffering. |
| Concurrency | `10` | Webhook processing is sub-second; high concurrency per instance is fine. |
| Min instances | `0` | Scale to zero between webhook deliveries. |
| Max instances | `2` | krew-index is a low-traffic repo; 2 instances is sufficient headroom for burst redeliveries. |
| CPU boost | enabled | Faster cold starts (important since GitHub's 10s timeout includes cold-start latency). |
| Timeout | `10` (seconds) | Matches GitHub's webhook delivery timeout. The gateway finishes in milliseconds, but this caps the worst case so Cloud Run doesn't hold a request open longer than GitHub will wait. |

### 3.2 Environment variables

| Variable | Source | Value / notes |
|---|---|---|
| `GITHUB_WEBHOOK_SECRET` | `.envrc` (plaintext) | HMAC-SHA256 secret shared with the GitHub webhook configuration. Must match exactly. |
| `GCP_PROJECT_ID` | hardcoded in deploy command | `ahmet-personal-api` |
| `PUBSUB_TOPIC` | hardcoded in deploy command | `krew-index-github-events` |
| `PORT` | set by Cloud Run | Cloud Run injects `$PORT` automatically. |
| `DISABLE_WEBHOOK_VERIFICATION` | unset | Verification is always on in production. |

---

## 4. Container Image

### 4.1 Dockerfile

`Dockerfile.gateway` (multi-stage):
- **Build stage:** `golang:1.26` — compiles `cmd/gateway` with
  `CGO_ENABLED=0`.
- **Runtime stage:** `alpine:3.20` with `ca-certificates` (for TLS to
  GCP Pub/Sub). No `git` needed — the gateway does no cloning.

### 4.2 Artifact Registry repository

The gateway image is pushed to the same Artifact Registry repository as the
agent image (provisioned by `AGENT_CLI_DEPLOYMENT.md` §6.3), under the
`gateway` tag:

| Property | Value |
|---|---|
| Repository name | `krew-review-agent` |
| Full image path | `us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/gateway` |

---

## 5. IAM & Service Account

### 5.1 Runtime service account

The gateway Cloud Run service runs as a dedicated service account with
least-privilege permissions — only the ability to publish to the target
Pub/Sub topic.

| Property | Value |
|---|---|
| SA name | `krew-review-event-gateway` |
| Email | `krew-review-event-gateway@ahmet-personal-api.iam.gserviceaccount.com` |
| Role | `roles/pubsub.publisher` on topic `krew-index-github-events` |

> **Why a dedicated SA?** The gateway's only GCP interaction is publishing
> to one Pub/Sub topic. A dedicated SA with a single role follows
> least-privilege principles and limits blast radius if the service is
> compromised. This is consistent with the agent deployment's use of a
> dedicated `krew-review-agent-invoker` SA.

---

## 6. GitHub Webhook Configuration

This step is performed manually in the GitHub UI for
`kubernetes-sigs/krew-index` (requires maintainer/admin access).

### 6.1 Steps

1. Navigate to **kubernetes-sigs/krew-index → Settings → Webhooks → Add
   webhook**.
2. Configure as follows:

   | Field | Value |
   |---|---|
   | Payload URL | `https://krew-review-event-gateway-1075231961184.us-central1.run.app/webhook` |
   | Content type | `application/json` |
   | Secret | The value of `GITHUB_WEBHOOK_SECRET` from `.envrc` |
   | SSL verification | Enabled (default) |
   | Which events trigger? | **Let me select individual events** → check **Pull requests** |
   | Active | ✓ (checked) |

3. Click **Add webhook**.
4. GitHub will send a `ping` event immediately. The gateway will respond
   `202` (filtered — `ping` is not `pull_request`). Verify the "Recent
   Deliveries" tab shows this delivery with a green checkmark.

### 6.2 Event selection rationale

Only **Pull requests** events are needed. The gateway filters to
`pull_request` event type with `action: opened` — other actions
(`closed`, `synchronize`, etc.) are filtered out with `202` and not
published to Pub/Sub. Subscribing to only the Pull requests event group
minimizes unnecessary webhook deliveries.

### 6.3 Webhook secret

The `GITHUB_WEBHOOK_SECRET` is a shared secret between GitHub and the
gateway. GitHub uses it to compute the `X-Hub-Signature-256` HMAC header
on every delivery. The gateway recomputes the HMAC and compares in
constant time. The secret is generated with `openssl rand -hex 32` and
stored in `.envrc` (local) and as a Cloud Run env var (production). It is
never logged.

---

## 7. Deployment Commands

### 7.1 Prerequisites

- `gcloud` CLI authenticated with access to project `ahmet-personal-api`.
- APIs enabled: Cloud Run, Cloud Build, Artifact Registry, Pub/Sub (all
  already enabled in this project).
- Environment variables loaded from `.envrc` (`direnv allow` or
  `source .envrc`).
- The Pub/Sub topic `krew-index-github-events` already exists (provisioned
  by `AGENT_CLI_DEPLOYMENT.md` §6.6). If not, create it first:

  ```bash
  gcloud pubsub topics create krew-index-github-events
  ```

### 7.2 Set default project

```bash
gcloud config set project ahmet-personal-api
```

### 7.3 Create runtime service account

```bash
gcloud iam service-accounts create krew-review-event-gateway
```

### 7.4 Grant Pub/Sub publisher role on the topic

```bash
gcloud pubsub topics add-iam-policy-binding krew-index-github-events \
  --member="serviceAccount:krew-review-event-gateway@ahmet-personal-api.iam.gserviceaccount.com" \
  --role="roles/pubsub.publisher"
```

### 7.5 Build & push container image

The gateway uses `Dockerfile.gateway` (separate from the agent's
`Dockerfile.agent`). Since `gcloud builds submit --tag` uses the default
`Dockerfile`, the gateway image is built via a Cloud Build config file
(`cloudbuild-gateway.yaml`) that references `Dockerfile.gateway`:

```bash
gcloud builds submit --config cloudbuild-gateway.yaml
```

This builds and pushes the image to Artifact Registry in one step. No local
Docker installation required.

### 7.6 Deploy Cloud Run service

```bash
gcloud run deploy krew-review-event-gateway \
  --image us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/gateway:latest \
  --region us-central1 \
  --ingress all \
  --allow-unauthenticated \
  --service-account krew-review-event-gateway@ahmet-personal-api.iam.gserviceaccount.com \
  --set-env-vars "GITHUB_WEBHOOK_SECRET=${GITHUB_WEBHOOK_SECRET},GCP_PROJECT_ID=ahmet-personal-api,PUBSUB_TOPIC=krew-index-github-events" \
  --timeout 10 \
  --memory 128Mi \
  --cpu 1000m \
  --concurrency 10 \
  --min-instances 0 \
  --max-instances 2 \
  --cpu-boost
```

> Note the service URL from the deploy output — it is needed for the GitHub
> webhook configuration in §6.

### 7.7 Configure the GitHub webhook

Follow the steps in [§6](#6-github-webhook-configuration) to add the
webhook to `kubernetes-sigs/krew-index`, using the service URL from §7.6
as the Payload URL.

---

## 8. Redeploy (update image)

After code changes to the gateway, rebuild and redeploy:

```bash
gcloud builds submit --config cloudbuild-gateway.yaml

gcloud run services update krew-review-event-gateway \
  --region us-central1 \
  --image us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/gateway:latest
```

The env vars, ingress, service account, and IAM bindings persist across
redeployments. If the webhook secret needs rotation, update the env var
and the GitHub webhook secret simultaneously:

```bash
gcloud run services update krew-review-event-gateway \
  --region us-central1 \
  --set-env-vars "GITHUB_WEBHOOK_SECRET=${GITHUB_WEBHOOK_SECRET},GCP_PROJECT_ID=ahmet-personal-api,PUBSUB_TOPIC=krew-index-github-events"
```

---

## 9. End-to-End Verification

After completing all deployment steps, verify the full pipeline:

1. **Check the gateway is serving:**
   ```bash
   curl -s -o /dev/null -w "%{http_code}" \
     -X POST https://krew-review-event-gateway-<hash>-uc.a.run.app/webhook \
     -H "X-GitHub-Event: ping" \
     -d '{}'
   # Expected: 202 (filtered — ping is not pull_request)
   ```

2. **Trigger a real webhook:** Open a test pull request on
   `kubernetes-sigs/krew-index` (or use GitHub's "Redeliver" button on a
   past delivery in the webhook settings).

3. **Check gateway logs:**
   ```bash
   gcloud run services logs read krew-review-event-gateway --region us-central1 --limit=5
   ```
   Look for `outcome: published` with the correct `delivery_id`,
   `event_type`, `action`, `repo`, and `pr_number`.

4. **Check the agent received the message:**
   ```bash
   gcloud run services logs read krew-review-agent --region us-central1 --limit=10
   ```
   Look for the review orchestration log lines (the agent's
   `event_type: pull_request` + `pr: kubernetes-sigs/krew-index#N`).

5. **Verify the review comment was posted** on the test PR by the agent.

---

## 10. Security Considerations

1. **HMAC signature verification:** Every webhook delivery is verified
   using `hmac.Equal` (constant-time comparison) against
   `GITHUB_WEBHOOK_SECRET`. Spoofed deliveries without a valid signature
   are rejected with `401`. This is the primary auth layer for the public
   endpoint.

2. **Repository allow-list:** Even with a valid signature, payloads from
   any repository other than `kubernetes-sigs/krew-index` are rejected
   with `403`. This prevents cross-repo event injection if the webhook
   secret is shared across repos.

3. **Public ingress is intentional:** Unlike the agent worker (which uses
   `--ingress internal`), the gateway must be public for GitHub to reach
   it. The HMAC signature + repo allow-list provide the security layer
   that `--ingress internal` provides for the agent.

4. **Least-privilege service account:** The gateway SA has only
   `roles/pubsub.publisher` on one topic. It cannot read messages, manage
   subscriptions, or access any other GCP resources.

5. **No sensitive data logged:** The raw webhook body (which may contain
   PR content) is forwarded to Pub/Sub and discarded. The webhook secret
   is never logged. Log fields are limited to metadata (delivery ID,
   event type, action, repo, PR number, outcome).

6. **`.dockerignore` prevents secret leakage:** `.envrc` (which contains
   the webhook secret and other secrets) is excluded from the Docker build
   context.
