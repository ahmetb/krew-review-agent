# Engineering Specification: Agent CLI Harness (`cmd/agent`)

This document is the implementation specification for the `cmd/agent` binary.
It is a companion to [`HIGH_LEVEL_DESIGN.md`](./HIGH_LEVEL_DESIGN.md), which
describes the agent architecture, tool definitions, and orchestration loop at
a conceptual level.

> **Note on superseded interface:** `HIGH_LEVEL_DESIGN.md` §3.1 specifies that
> the program reads the GitHub payload from `os.Stdin`. **This document
> supersedes that interface.** In production the binary runs as a long-lived
> HTTP server receiving Pub/Sub push deliveries on Cloud Run. The `stdin`
> interface is no longer used.

---

## 1. Overview & Scope

The `cmd/agent` binary is the entrypoint of the `krew-review-agent`. It is a
single Go binary that operates in one of two modes, selected by CLI flags:

1. **Production Mode** — a long-lived HTTP server that receives Pub/Sub push
   deliveries (Cloud Run invocation model) and runs the agent orchestration loop
   per request.
2. **Test Mode** — a run-to-completion CLI that reads a raw GitHub event payload
   from a file (`--test-payload`) and executes the agent loop in **dry-run**
   mode, making no write calls to GitHub.

Both modes share the same agent orchestration loop, tool implementations, LLM
client, and system prompt. The only difference is the input source (HTTP vs.
file) and whether write-side tools (`submit_review_comment`) are intercepted.

The binary is designed to be stateless and ephemeral: each request (production)
or invocation (test) runs one complete review and then either returns an HTTP
response or exits.

---

## 2. Operating Modes

### 2.1 Production Mode (HTTP Server on `$PORT`)

When run without `--test-payload`, the binary starts an HTTP server listening on
`$PORT` (Cloud Run contract; default `8080`).

- Each `POST /pubsub` request is parsed into a GitHub event and dispatched to
  the agent orchestration loop on a dedicated goroutine.
- The server stays up and serves concurrent requests until killed by Cloud Run
  (SIGTERM).
- On successful review completion, the HTTP response is `200`; on failure, `500`
  (see [§4.4](#44-http-status-code-semantics)).

### 2.2 Test Mode (`--test-payload`, run-to-completion, always dry-run)

When invoked with `--test-payload=FILE`, the binary:

1. Reads the file (a raw GitHub webhook event JSON, **not** a Pub/Sub envelope).
2. Runs the agent orchestration loop in **dry-run** mode.
3. Exits with code `0` on success, non-zero on failure.

In dry-run mode:
- Data-gathering tools (`fetch_pr_diff`, `fetch_plugin_manifest`,
  `get_all_existing_plugins`) make **real** network calls (to GitHub, git clone).
- Write-side tools (`submit_review_comment`, and the circuit-breaker fallback
  comment) are **intercepted**: the would-be comment body is printed to `stdout`
  and logged via `slog`, but no HTTP POST to GitHub is made.
- `noop` is logged as usual.

There is no way to post a live comment from test mode; `--test-payload` always
implies dry-run. This is intentional to make local iteration safe.

---

## 3. Command-Line Interface

### 3.1 Flags

| Flag | Default | Description |
|---|---|---|
| `--test-payload=FILE` | (unset) | If set, run in test/dry-run mode using the given raw GitHub event file. If unset, run as HTTP server. |
| `--port=N` | `$PORT` or `8080` | HTTP listen port (production mode only). |

### 3.2 Environment Variables

| Variable | Required | Default | Description |
|---|---|---|---|
| `GITHUB_TOKEN` | yes (prod) | — | GitHub API token for fetching diffs and posting comments. In dry-run, used only for read calls (diff/manifest). |
| `LLM_API_KEY` | yes | — | API key for the LLM provider (OpenAI-compatible). |
| `LLM_BASE_URL` | yes | — | Base URL for the LLM API (e.g. AI Gateway endpoint). |
| `LLM_MODEL` | no | `glm-5.2` | Model name passed to the LLM API. |
| `MAX_ITERATIONS` | no | `7` | Circuit-breaker limit for the agent loop. |
| `LOG_LEVEL` | no | `INFO` | slog level (`DEBUG`, `INFO`, `WARN`, `ERROR`). |
| `PORT` | no | `8080` | HTTP listen port (Cloud Run contract). Overridden by `--port` if set. |

> **TODO:** Ingress request authentication (verifying the Pub/Sub push caller)
> is not implemented in v1; see [§4.7](#47-request-authentication-todo).

---

## 4. HTTP Server Design

### 4.1 Endpoint: `POST /pubsub`

The server handles a single endpoint:

```
POST /pubsub
```

- **Method:** `POST` only. Any other method (e.g. `GET`) returns `405 Method
  Not Allowed`. There is no health-check endpoint; Cloud Run detects container
  start via the port being open.
- **Path:** `/pubsub`. All other paths return `404 Not Found`.

### 4.2 Request Body Parsing (auto-detect)

The HTTP request body may be either a Pub/Sub push envelope (wrapped) or a raw
GitHub webhook event, depending on the subscription's payload-unwrapping
setting. The parser auto-detects:

1. Read the full request body.
2. Attempt to unmarshal into a probe struct with a top-level `message` field.
3. If a `message` field is present, treat the body as a **wrapped Pub/Sub
   envelope** and extract `message.data`. Per the Pub/Sub push specification,
   `message.data` is **base64-encoded**. In Go, unmarshaling into a `[]byte`
   field automatically base64-decodes it, so no explicit decode step is needed.
   The decoded bytes are the raw GitHub webhook event JSON.
4. If no `message` field is present, treat the body as the **raw GitHub webhook
   event** directly.

This single code path handles:
- Wrapped Pub/Sub push (default subscription setting).
- Unwrapped Pub/Sub push (subscription with payload-unwrapping enabled).
- Cloud Tasks deliveries (raw body).
- Local `--test-payload` files (raw GitHub event, bypassing the HTTP parser
  entirely).

Relevant Go struct (illustrative):

```go
// pubsubEnvelope is the wrapped Pub/Sub push body.
type pubsubEnvelope struct {
    Message struct {
        Data []byte `json:"data"` // base64-decoded automatically by encoding/json
    } `json:"message"`
    Subscription string `json:"subscription"`
}
```

### 4.3 Event Filtering

After parsing the raw GitHub event, the server checks the `X-GitHub-Event` HTTP
header (set by GitHub webhooks; for Pub/Sub-delivered events this header is
absent, so the event type is determined from the JSON payload's top-level
fields). The agent loop is only run for `pull_request` events.

- **`pull_request` events (any action):** proceed to the agent loop.
- **All other event types** (e.g. `push`, `issue_comment`, `ping`): log the
  event type and return `200` immediately without invoking the LLM. This avoids
  spending LLM calls on irrelevant events.

### 4.4 HTTP Status Code Semantics

Pub/Sub acknowledges a message on any `2xx` response and retries on non-`2xx`.
The status code is chosen to avoid poison-message retry loops while still
retrying transient failures:

| Outcome | HTTP Status | Pub/Sub Behavior |
|---|---|---|
| Review submitted successfully | `200` | ACK (drop) |
| `noop` terminal tool called | `200` | ACK (drop) |
| Non-`pull_request` event | `200` | ACK (drop) |
| Malformed/unparseable payload | `200` | ACK (drop) — avoid retry loop on bad input |
| Transient GitHub API error | `500` | Retry |
| Transient LLM API error | `500` | Retry |
| Circuit-breaker failure **and** fallback comment also failed to post | `500` | Retry |
| Circuit-breaker failure but fallback comment posted successfully | `200` | ACK (drop) |

The principle: return `500` only when a retry might succeed (transient
infra failures) or when the PR is left in a bad state (no comment at all).
Return `200` for anything that is a final, non-retryable outcome.

### 4.5 Concurrency Model

The server processes multiple reviews concurrently — one goroutine per request.
This is safe because:

- The agent loop and message history are per-request (no shared mutable state
  between reviews).
- The krew-index clone (`get_all_existing_plugins`) is guarded by a `sync.Once`
  + mutex: the first call clones the repo into a temp directory; concurrent and
  subsequent calls block until the clone completes, then reuse the existing
  directory. The clone is a shallow `git clone --depth 1` and completes quickly.
  After the clone, all reads are concurrent-safe (read-only filesystem access).

Cloud Run's instance concurrency setting controls how many requests reach a
single instance; the binary itself does not impose a concurrency cap beyond
goroutine-per-request.

### 4.6 Graceful Shutdown (SIGTERM)

Cloud Run sends `SIGTERM` before `SIGKILL` (with a configurable grace period).
The server handles `SIGTERM`/`SIGINT`:

1. Stop accepting new requests (call `http.Server.Shutdown`).
2. Wait up to **2 minutes** for in-flight reviews to complete.
3. If the timeout expires, log a warning and force-exit (in-flight reviews are
   abandoned; Pub/Sub will redeliver those messages).

> **Deployment note:** The Cloud Run service's termination grace period (set via
> `--timeout` or the container's `stopTimeout`) must be configured to **>= 2
> minutes** to allow the drain to complete. Otherwise Cloud Run will SIGKILL
> before the drain finishes.

### 4.7 Request Authentication (TODO)

**v1 does not verify incoming request authenticity.** This is acceptable as an
interim measure if Cloud Run ingress is restricted (e.g. internal-only or IAM
Invoker) so that only the intended Pub/Sub subscription can reach the endpoint.

This must be addressed before any public-facing deployment. Candidate
approaches (to be evaluated later):
- Verify the Google-issued OIDC token that Pub/Sub attaches to authenticated
  push requests (see Pub/Sub "Authenticate push requests" docs).
- Verify a shared-secret bearer token from an env var.

The design doc and code will carry a `TODO` marker for this.

---

## 5. Test / Dry-Run Mode

### 5.1 `--test-payload` Semantics

```
cmd/agent --test-payload=path/to/event.json
```

- The file must contain a **raw GitHub webhook event** JSON (not a Pub/Sub
  envelope). This matches what GitHub sends and what you'd capture from a
  webhook delivery.
- The program parses the event, runs the agent loop, and exits.
- Dry-run is **always on** in this mode; there is no flag to disable it.

### 5.2 Dry-Run Tool Behavior

Tools are invoked with a `dryRun bool` parameter (set by the orchestration loop
based on mode). Behavior:

| Tool | Dry-run behavior |
|---|---|
| `fetch_pr_diff` | Real GitHub API call (read-only). |
| `fetch_plugin_manifest` | Real filesystem read from the clone (read-only). |
| `get_all_existing_plugins` | Real `git clone` / filesystem read (read-only). |
| `submit_review_comment` | **Intercepted:** no GitHub POST. Comment body printed to stdout and logged. |
| `noop` | Logged as usual (no side effects either way). |
| Circuit-breaker fallback comment | **Intercepted:** same as `submit_review_comment` — printed + logged, not posted. |

### 5.3 Output Surfacing

When `submit_review_comment` (or the fallback) is intercepted in dry-run:

1. The full comment body is written to **stdout**, delimited by a clear marker
   so it can be extracted/eyeballed:
   ```
   --- review comment (dry-run, not posted) ---
   <body>
   --- end review comment ---
   ```
2. The body is also emitted via `slog` as a structured log field for
   consistency with production logging.

All other logging (iteration counts, tool calls, LLM request/response metadata)
goes to stderr via `slog`, same as production.

---

## 6. Package Layout

Module path: `github.com/ahmetb/krew-review-agent`
Go version: **1.26**

```
cmd/agent/
    main.go                  # entrypoint: flag parsing, mode selection, wiring

internal/
    server/
        server.go            # HTTP server, /pubsub handler, graceful shutdown
        parse.go             # request body parsing (Pub/Sub envelope vs raw)

    agent/
        agent.go             # orchestration loop (init, for-loop, routing)
        history.go           # message history management
        circuit.go           # circuit-breaker logic

    llm/
        client.go            # openai-go SDK wrapper (chat completions + tools)
        tools.go             # tool/function JSON schema definitions

    tools/
        tools.go             # Tool interface + registry
        fetch_pr_diff.go     # fetch_pr_diff implementation
        fetch_manifest.go    # fetch_plugin_manifest implementation
        all_plugins.go       # get_all_existing_plugins implementation (lazy clone)
        submit_review.go     # submit_review_comment implementation (dry-run aware)
        noop.go              # noop implementation
        clone.go             # shared krew-index clone (sync.Once guarded)

    githubclient/
        client.go            # GitHub API client (diff fetch, comment post)

    pubsub/
        envelope.go          # Pub/Sub wrapped envelope types

    log/
        log.go               # slog setup (JSON handler, level, trace IDs)
```

`cmd/agent/main.go` is thin: it parses flags, constructs dependencies (LLM
client, GitHub client, tool registry), and either starts the HTTP server
(production) or runs a single review (test mode).

---

## 7. Agent Orchestration Loop (Implementation)

The loop implements the state machine described in `HIGH_LEVEL_DESIGN.md` §5.
This section specifies the implementation-level details not covered there.

### 7.1 Initialization

1. Parse the raw GitHub event JSON. Extract: PR number, repo owner/name, PR
   title, PR body, author, action, and the head SHA (for diff fetching).
2. Initialize `MessageHistory` (slice of messages).
3. Append `Role: system` containing the embedded `SYSTEM_PROMPT.md` content.
4. Append `Role: user` containing the PR context (e.g. "Review PR #123 in
   owner/repo titled 'Add foo plugin' by author. PR body: ...").

### 7.2 The Loop

Bounded by `MAX_ITERATIONS`. Each iteration:

1. **LLM inference:** transmit `MessageHistory` to the LLM via the `openai-go`
   client (with tool definitions). Block for response.
2. **Response routing:**
   - **Tool call (non-terminal):** append the assistant message (with tool call)
     to history; dispatch to the Go tool implementation; append the result as a
     `Role: tool` message; continue.
   - **Terminal tool (`submit_review_comment` or `noop`):** execute the tool
     (honoring dry-run); break the loop; return success.
   - **Conversational text (no tool call):** append the text to history; append
     a forced `Role: user` warning ("You must use a tool to gather data or use
     submit_review_comment to finish your task."); continue.

### 7.3 Circuit Breaker

When the iteration count reaches `MAX_ITERATIONS`:

1. Append a final `Role: user` message: *"CIRCUIT BREAKER: You have reached the
   maximum allowed tool executions. You must immediately output your findings
   using the submit_review_comment tool."*
2. Make one final LLM inference call.
3. If the LLM calls `submit_review_comment` (or `noop`): execute it (dry-run
   aware) and return success.
4. If the LLM still fails to use a terminal tool: attempt to post a **fallback
   comment** to the PR (a generic message indicating an internal agent failure).
   - In dry-run: the fallback is intercepted and printed (same as any
     `submit_review_comment`), and the program exits non-zero.
   - In production: if the fallback POST succeeds, return `200`; if it fails,
     return `500` (so Pub/Sub retries).

---

## 8. Tool Implementations

All tools implement a common interface. Each tool receives a `dryRun bool` from
the orchestration loop (true in test mode, false in production). The LLM-facing
JSON schema for these tools is defined in `internal/llm/tools.go` and matches
the descriptions in `HIGH_LEVEL_DESIGN.md` §4 and `SYSTEM_PROMPT.md`.

### 8.1 `fetch_pr_diff()`

- **LLM signature:** no parameters.
- **Implementation:** authenticated `GET` to GitHub's PR diff endpoint
  (`GET /repos/{owner}/{repo}/pulls/{pr_number}`, `Accept: application/vnd.github.v3.diff`).
  Returns the raw diff string.
- **Dry-run:** same (read-only).

### 8.2 `fetch_plugin_manifest(name: string)`

- **LLM signature:** `name` (string) — the plugin name, **not** a file path.
- **Implementation:** sanitizes `name` (validates kebab-case, rejects path
  separators / traversal), joins with the krew-index clone path as
  `plugins/<name>.yaml`, and reads the file from the **master clone**.
  - For **existing-plugin updates**: returns the original (pre-PR) manifest. The
    LLM combines this with `fetch_pr_diff()` to understand the changes.
  - For **new-plugin submissions**: the file does not exist in master. Returns a
    clear message: *"Plugin '<name>' does not exist in the master krew-index
    (this appears to be a new submission). The full manifest content is
    available in the PR diff."* The LLM already has the full new file from the
    diff (added lines), so no PR-head fetch is needed.
- **Dry-run:** same (read-only).
- **Security:** `name` is validated; arbitrary paths are rejected. See
  [§13](#13-security--sandboxing).

### 8.3 `get_all_existing_plugins()`

- **LLM signature:** no parameters.
- **Implementation (fat tool):**
  1. Check if the krew-index clone exists at
     `os.TempDir()/krew-index` (see [§8.3.1](#831-clone-management)).
  2. If not, run `git clone --depth 1 https://github.com/kubernetes-sigs/krew-index <path>`.
  3. Read all `plugins/*.yaml` files.
  4. Parse each YAML, extract `name` and `shortDescription`.
  5. Concatenate into a single string: `name: shortDescription` per line.
  6. Return the compiled string.
- **Dry-run:** same (read-only).

#### 8.3.1 Clone Management

- **Path:** `os.TempDir()/krew-index` (not hardcoded `/tmp/krew-index`, to
  respect the OS temp directory).
- **Concurrency:** guarded by a `sync.Once` + mutex. The first caller clones;
  all concurrent and subsequent callers block until the clone finishes, then
  reuse the directory. No re-clone within a process lifetime.
- **Shallow:** `git clone --depth 1` for speed.
- **No cleanup:** the clone is left in the temp dir; on Cloud Run the container
  filesystem is destroyed after the instance scales down. For local re-runs,
  the existing clone is reused (fast iteration).

### 8.4 `submit_review_comment(body: string)` [TERMINAL]

- **LLM signature:** `body` (string) — the Markdown review comment.
- **Implementation:** authenticated `POST` to the GitHub Issues API
  (`POST /repos/{owner}/{repo}/issues/{pr_number}/comments`) with the body.
  Sets the terminal flag to end the loop.
- **Dry-run:** **intercepted.** No POST. The body is printed to stdout (with
  delimiters, see [§5.3](#53-output-surfacing)) and logged via `slog`.

### 8.5 `noop(reason: string)` [TERMINAL]

- **LLM signature:** `reason` (string).
- **Implementation:** logs the reason. Sets the terminal flag to end the loop.
  No GitHub API call.
- **Dry-run:** same (already side-effect-free).

---

## 9. LLM Client

### 9.1 SDK

Uses the official [`openai-go`](https://github.com/openai/openai-go) SDK, which
supports OpenAI-compatible `/chat/completions` endpoints with `tool_calls`
(function calling). The LLM providers in use (GLM 5.2 via an AI gateway,
Kimi, etc.) are OpenAI-API-compatible, so a single client works across
providers by varying `LLM_BASE_URL` and `LLM_MODEL`.

### 9.2 Configuration

| Setting | Source | Default |
|---|---|---|
| API key | `LLM_API_KEY` | (required) |
| Base URL | `LLM_BASE_URL` | (required) |
| Model | `LLM_MODEL` | `glm-5.2` |
| Timeout | hardcoded | reasonable default (e.g. 120s per request) |

The base URL points to the AI Gateway (e.g. Cloudflare AI Gateway or OpenCode
Zen), which provides request/response observability and caching. The binary
does not implement custom logging of LLM requests/responses beyond what the SDK
and gateway provide.

### 9.3 Tool Schema

The tool/function JSON schema is defined once in `internal/llm/tools.go` and
passed to the SDK on every chat completion call. It describes the 5 tools with
strict parameter types (matching [§8](#8-tool-implementations)). The SDK handles
the `tools` / `tool_choice` fields in the API request and parses `tool_calls`
in the response.

---

## 10. System Prompt Loading

`SYSTEM_PROMPT.md` is embedded into the binary at build time via `go:embed`:

```go
//go:embed SYSTEM_PROMPT.md
var systemPrompt string
```

The embedded file is located at the module root. The system prompt is **not**
overridable at runtime; iterating on the prompt requires a rebuild. This keeps
the production binary self-contained (no external file dependency).

---

## 11. Logging

- Uses the standard library `log/slog` package with a JSON handler.
- Level controlled by `LOG_LEVEL` env (default `INFO`).
- Logs go to **stderr** (stdout is reserved for dry-run comment output in test
  mode).
- Structured fields include:
  - `trace_id` — a per-request UUID for correlating all logs within one review.
  - `iteration` — the current loop iteration number.
  - `tool` — the tool name being executed.
  - `tool_args` — the tool arguments (for non-sensitive tools).
  - `tool_status` — `ok` / `error`.
  - `pr` — `{owner}/{repo}#{number}`.
  - `event_type` — the GitHub event type.
  - `dry_run` — boolean, whether the review is in dry-run mode.

---

## 12. Container & Runtime Dependencies

### 12.1 OS-Level Dependencies

The container runtime image must include:

- **`git`** — required by `get_all_existing_plugins` for the shallow clone of
  the krew-index repo.
- **`ca-certificates`** — required for HTTPS verification of outbound calls to
  GitHub and the LLM API. (Go's `net/http` uses the system CA bundle.)

No other OS-level tools are required. Go uses its own crypto/network stack, so
`openssl` (the CLI/binary) is **not** needed. The binary does not shell out to
any program other than `git`.

### 12.2 Container Image

Suggested multi-stage build:

- **Build stage:** `golang:1.26` (compile the binary).
- **Runtime stage:** a minimal base (e.g. `alpine` or a distroless static image)
  with `git` and `ca-certificates` installed. If using distroless, use the
  `:debug` variant or a custom base that includes `git`.

### 12.3 Cloud Run Contract

- Listens on `$PORT` (Cloud Run sets this).
- Handles `SIGTERM` for graceful shutdown (see [§4.6](#46-graceful-shutdown)).
- Termination grace period must be **>= 2 minutes** to allow drain.

---

## 13. Security & Sandboxing

This section implements `HIGH_LEVEL_DESIGN.md` §6 for the CLI harness:

- **No arbitrary execution:** The LLM is never given a `bash`/`exec` tool. It
  can only invoke the 5 explicitly defined Go functions. The only subprocess
  the binary spawns is `git clone` (hardcoded command, no LLM-controlled
  arguments beyond the clone URL, which is hardcoded).
- **Path sanitization:** `fetch_plugin_manifest` accepts only a plugin `name`,
  not a file path. The `name` is validated (kebab-case, no separators, no
  traversal) and joined with the clone path internally. The LLM cannot supply
  arbitrary paths or escape the designated workspace.
- **Ephemeral state:** The binary is stateless across requests. The krew-index
  clone lives in `os.TempDir()` and is read-only after creation. On Cloud Run,
  the container filesystem is destroyed when the instance scales down, ensuring
  no state leakage between reviews. (Within a single long-lived instance, the
  clone is intentionally reused across requests for performance — it is
  read-only and contains only public data.)
- **Secrets:** `GITHUB_TOKEN` and `LLM_API_KEY` are read from env vars and
  never logged. Tool arguments that might contain sensitive data are scrubbed
  from logs.

---

## 14. Open Items / TODOs

These are known gaps acknowledged for v1, to be addressed before broader
deployment:

1. **Ingress request authentication ([§4.7](#47-request-authentication-todo)):**
   v1 does not verify the authenticity of incoming Pub/Sub push requests. Cloud
   Run ingress must be restricted (internal-only or IAM Invoker) as an interim
   measure. Candidate fix: verify Google-issued OIDC tokens from Pub/Sub, or a
   shared-secret bearer token.

2. **Pub/Sub at-least-once redelivery / duplicate comments:** Pub/Sub may
   redeliver the same event, causing the same review comment to be posted
   twice. v1 accepts this risk. Candidate fix: before posting, search the PR
   for an existing comment with a hidden marker (e.g.
   `<!-- krew-review-agent -->`) and skip if found.

3. **Cloud Run shutdown timeout:** The deployment must set a termination grace
   period >= 2 minutes to allow the graceful drain ([§4.6](#46-graceful-shutdown))
   to complete. This is a deployment-config concern, not a code change, but
   must not be overlooked.
