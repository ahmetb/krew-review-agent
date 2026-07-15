# Deployment Specification: Agent Worker on Cloud Run

This document describes the production deployment topology and configuration
for the `cmd/agent` binary on Google Cloud Run. It is a companion to
[`AGENT_CLI.md`](./AGENT_CLI.md) (binary specification) and
[`EVENT_GATEWAY.md`](./EVENT_GATEWAY.md) (event intake).

---

## 1. Deployment Topology

```
                         GCP Project: ahmet-personal-api
                         Region: us-central1

  ┌─────────────────┐     ┌──────────────────────┐     ┌─────────────────────────────────────┐
  │  Event Gateway   │     │  Pub/Sub Topic        │     │  Agent Worker (Cloud Run)           │
  │  (Cloud Run)     │────▶│  krew-index-github-   │────▶│  Service: krew-review-agent         │
  │  (separate doc)  │     │  events               │     │  Image: …/krew-review-agent/agent   │
  │                  │     │                      │     │  Endpoint: POST /pubsub             │
  └─────────────────┘     │  Subscription:        │     │  Ingress: internal                  │
   GitHub webhook          │  krew-index-github-   │     │  Auth: IAM Invoker (no allow-       │
   → publish message       │  events-to-agent      │     │        unauthenticated)            │
                           │  (authenticated push) │     │                                     │
                           └──────────────────────┘     └─────────────────────────────────────┘
                                  │                                        ▲
                                  │  OIDC token from                        │  roles/run.invoker
                                  │  krew-review-agent-invoker SA            │  on krew-review-agent
                                  └──────────────────────────────────────────┘
```

### Components

| Component | Type | Name | Status |
|---|---|---|---|
| Artifact Registry repository | Artifact Registry (Docker) | `krew-review-agent` (us-central1) | Provisioned |
| Container image | Artifact Registry | `us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/agent:latest` | Built & pushed |
| Agent service | Cloud Run (managed) | `krew-review-agent` | Deployed |
| Pub/Sub topic | Pub/Sub | `krew-index-github-events` | Provisioned |
| Push subscription | Pub/Sub | `krew-index-github-events-to-agent` | Provisioned |
| Invoker service account | IAM | `krew-review-agent-invoker@ahmet-personal-api.iam.gserviceaccount.com` | Provisioned |
| IAM binding | IAM | `roles/run.invoker` on `krew-review-agent` for the invoker SA | Applied |
| Event Gateway | Cloud Run (managed) | (separate — see `EVENT_GATEWAY.md`) | Separate effort |

### Message flow

1. The **Event Gateway** receives GitHub webhooks, verifies HMAC signatures,
   filters for `pull_request` events from `kubernetes-sigs/krew-index`, and
   publishes the raw webhook JSON to the `krew-index-github-events` topic (see
   `EVENT_GATEWAY.md` §4–§5).
2. The **push subscription** `krew-index-github-events-to-agent` delivers each
   message as an authenticated HTTPS POST to the agent's `/pubsub` endpoint.
   Pub/Sub attaches an OIDC identity token issued for the
   `krew-review-agent-invoker` service account.
3. The **Agent Worker** auto-detects the Pub/Sub envelope (or raw body),
   extracts the GitHub event, and runs the review orchestration loop. See
   `AGENT_CLI.md` §4.2 for the body-parsing logic.

---

## 2. Cloud Run Service Configuration

### 2.1 Service settings

| Setting | Value | Rationale |
|---|---|---|
| Service name | `krew-review-agent` | |
| Region | `us-central1` | Matches existing project services |
| Service URLs | `https://krew-review-agent-1075231961184.us-central1.run.app` (primary), `https://krew-review-agent-pwfuv4g72q-uc.a.run.app` (alias) | Both routes are internal-only. |
| Ingress | `internal` | §4.7: v1 has no request-auth verification. Internal ingress restricts access to same-project Google services (Pub/Sub push is explicitly allowed per Cloud Run ingress docs). |
| Authentication | `--no-allow-unauthenticated` | Only the Pub/Sub push SA (granted `roles/run.invoker`) may invoke. |
| Container port | `8080` (default `$PORT`) | Cloud Run contract; binary reads `$PORT`. |
| CPU | `1000m` | krew-index shallow clone + YAML parsing. |
| Memory | `512Mi` | Headroom for clone + concurrent request goroutines. |
| Concurrency | `10` | Reviews are long, I/O-bound (LLM waits). Shared read-only clone is concurrency-safe (§4.5). |
| Min instances | `0` | Scale to zero; Pub/Sub retries on cold start. |
| Max instances | `10` | Cap runaway cost on PR bursts. |
| CPU boost | enabled | Faster cold starts. |
| Timeout | `600` (10 min) | Covers multi-iteration reviews + 2-min graceful drain (§4.6/§14.3). Aligned with Pub/Sub ack-deadline. |

### 2.2 Environment variables

| Variable | Source | Value / notes |
|---|---|---|
| `GITHUB_TOKEN` | `.envrc` (plaintext) | GitHub API token for diff fetch + comment post. |
| `LLM_API_KEY` | `.envrc` (plaintext) | API key for the LLM provider. |
| `LLM_BASE_URL` | `.envrc` (plaintext) | `https://opencode.ai/zen/v1` (AI Gateway endpoint). |
| `LLM_MODEL` | unset → binary default | `glm-5.2` |
| `MAX_ITERATIONS` | unset → binary default | `10` |
| `LOG_LEVEL` | unset → binary default | `INFO` |
| `PORT` | set by Cloud Run | Cloud Run injects `$PORT` automatically. |

> **Secrets note:** v1 uses plaintext env vars for simplicity (matching the
> project convention). A future iteration should migrate to Secret Manager
> (`--set-secrets`) to avoid exposing secrets in `gcloud run services
> describe` output.

---

## 3. Container Image

### 3.1 `ko` build

Images are built with [`ko`](https://github.com/ko-build/ko) — there are no
Dockerfiles. ko compiles `cmd/agent` with `CGO_ENABLED=0` and layers the
resulting static binary onto a base image in a single step.

Because the agent shells out to `git clone` at runtime
(`internal/tools/clone.go`), its image must ship `git`. ko cannot install
packages, so the agent overrides its base image to
`cgr.dev/chainguard/git:latest`, which bundles both `git` and
`ca-certificates`. This override is declared in `.ko.yaml`:

```yaml
defaultBaseImage: gcr.io/distroless/static-debian12
baseImageOverrides:
  github.com/ahmetb/krew-review-agent/cmd/agent: cgr.dev/chainguard/git:latest
```

### 3.2 Build context

ko compiles the binary from the Go module and layers only the binary onto the
base image — no source tree, secrets, or test data are copied into the image.
`.envrc` (which contains live secrets) therefore can never enter the image.

### 3.3 Artifact Registry repository

| Property | Value |
|---|---|
| Repository name | `krew-review-agent` |
| Format | Docker |
| Location | `us-central1` |
| Full image path | `us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/agent` |

---

## 4. Pub/Sub Wiring

### 4.1 Topic

| Property | Value |
|---|---|
| Topic name | `krew-index-github-events` |
| Project | `ahmet-personal-api` |
| Provisioned by | This deployment (Event Gateway expects it to pre-exist — `EVENT_GATEWAY.md` §5.1) |

### 4.2 Push subscription

| Property | Value |
|---|---|
| Subscription name | `krew-index-github-events-to-agent` |
| Topic | `krew-index-github-events` |
| Push endpoint | `https://krew-review-agent-1075231961184.us-central1.run.app/pubsub` |
| Auth token type | OIDC (implied by `--push-auth-service-account`) |
| Service account | `krew-review-agent-invoker@ahmet-personal-api.iam.gserviceaccount.com` |
| Ack deadline | `600` (10 min) — must be ≥ Cloud Run timeout to prevent mid-review redelivery |

### 4.3 Invoker service account

| Property | Value |
|---|---|
| SA name | `krew-review-agent-invoker` |
| Email | `krew-review-agent-invoker@ahmet-personal-api.iam.gserviceaccount.com` |
| Role on Cloud Run service | `roles/run.invoker` |
| Required IAM | Pub/Sub service agent needs `iam.serviceAccountTokenCreator` on this SA to mint OIDC tokens (auto-granted by `gcloud pubsub subscriptions create --push-auth-service-account`) |

---

## 5. Security Considerations

1. **No request-auth verification (§4.7):** v1 does not verify incoming Pub/Sub
   push authenticity. The interim control is `--ingress=internal` +
   `--no-allow-unauthenticated` + IAM Invoker — only the Pub/Sub push SA in the
   same project can reach the endpoint.

2. **Secrets in plaintext:** `GITHUB_TOKEN` and `LLM_API_KEY` are passed as
   plaintext env vars. They are visible in `gcloud run services describe` and
   the Cloud Run console. Migrate to Secret Manager before any broader access.

3. **At-least-once delivery / duplicate comments (§14.2):** Pub/Sub may
   redeliver the same event. v1 accepts the risk of duplicate review comments.
   The ack-deadline (600s) is set to match the Cloud Run timeout to minimize
   mid-review redeliveries.

4. **No source/secrets in the image:** ko layers only the compiled binary
   onto the base image; the source tree (including `.envrc` with live secrets)
   is never copied into the image, so there is no build context to exclude.

---

## 6. Deployment Commands

### 6.1 Prerequisites

- `gcloud` CLI authenticated with access to project `ahmet-personal-api`.
- [`ko`](https://github.com/ko-build/ko) installed (`go install
  github.com/ko-build/ko@latest`).
- Docker authenticated to Artifact Registry (`gcloud auth configure-docker
  us-central1-docker.pkg.dev`); ko pushes through the same credentials.
- APIs enabled: Cloud Run, Artifact Registry, Pub/Sub (all already enabled in
  this project).
- Environment variables loaded from `.envrc` (e.g. `direnv allow` or
  `source .envrc`) for the deploy step.

### 6.2 Set default project

```bash
gcloud config set project ahmet-personal-api
```

### 6.3 Create Artifact Registry repository

```bash
gcloud artifacts repositories create krew-review-agent \
  --repository-format=docker \
  --location=us-central1
```

### 6.4 Build & push container image

ko compiles `cmd/agent` and pushes the image to Artifact Registry in one step.
`--bare` makes ko use `KO_DOCKER_REPO` verbatim as the image name, and
`--tags=latest` tags it:

```bash
KO_DOCKER_REPO=us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/agent \
  ko build --bare --tags=latest ./cmd/agent
```

The base image (`cgr.dev/chainguard/git:latest`) and build flags are read from
`.ko.yaml` (§3.1).

### 6.5 Deploy Cloud Run service

```bash
gcloud run deploy krew-review-agent \
  --image us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/agent:latest \
  --region us-central1 \
  --ingress internal \
  --no-allow-unauthenticated \
  --set-env-vars "GITHUB_TOKEN=${GITHUB_TOKEN},LLM_API_KEY=${LLM_API_KEY},LLM_BASE_URL=${LLM_BASE_URL}" \
  --timeout 600 \
  --memory 512Mi \
  --cpu 1000m \
  --concurrency 10 \
  --min-instances 0 \
  --max-instances 10 \
  --cpu-boost
```

> Note the service URL from the deploy output — it is needed for the push
> subscription endpoint in §6.8.

### 6.6 Create Pub/Sub topic

```bash
gcloud pubsub topics create krew-index-github-events
```

### 6.7 Create invoker service account + IAM binding

```bash
gcloud iam service-accounts create krew-review-agent-invoker

gcloud run services add-iam-policy-binding krew-review-agent \
  --region us-central1 \
  --member serviceAccount:krew-review-agent-invoker@ahmet-personal-api.iam.gserviceaccount.com \
  --role roles/run.invoker
```

### 6.8 Create authenticated push subscription

Replace `SERVICE_URL` with the URL from §6.5.

```bash
gcloud pubsub subscriptions create krew-index-github-events-to-agent \
  --topic krew-index-github-events \
  --push-endpoint="${SERVICE_URL}/pubsub" \
  --push-auth-service-account=krew-review-agent-invoker@ahmet-personal-api.iam.gserviceaccount.com \
  --ack-deadline=600
```

`gcloud` auto-grants the Pub/Sub service agent
`iam.serviceAccountTokenCreator` on the push service account. If this step
errors with a permission message, grant it manually:

```bash
gcloud projects add-iam-policy-binding ahmet-personal-api \
  --member "serviceAccount:service-1075231961184@gcp-sa-pubsub.iam.gserviceaccount.com" \
  --role roles/iam.serviceAccountTokenCreator
```

---

## 7. Redeploy (update image)

After code changes, rebuild and redeploy:

```bash
KO_DOCKER_REPO=us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/agent \
  ko build --bare --tags=latest ./cmd/agent

gcloud run services update krew-review-agent \
  --region us-central1 \
  --image us-central1-docker.pkg.dev/ahmet-personal-api/krew-review-agent/agent:latest
```

The env vars, ingress, and IAM bindings persist across redeployments.

---

## 8. Open Items

1. **Secret Manager migration:** Move `GITHUB_TOKEN` and `LLM_API_KEY` to
   Secret Manager (`--set-secrets`). Requires enabling the Secret Manager API.

2. **Request authentication (§4.7):** Verify the Google-issued OIDC token from
   Pub/Sub in the agent binary, rather than relying solely on ingress/IAM.

3. **Dead-letter queue:** Configure a Pub/Sub DLQ for poison messages that
   would otherwise be retried indefinitely.

4. **Event Gateway deployment:** The Event Gateway (`EVENT_GATEWAY.md`) is a
   separate service. Once deployed, configure it with
   `GCP_PROJECT_ID=ahmet-personal-api` and
   `PUBSUB_TOPIC=krew-index-github-events`.
