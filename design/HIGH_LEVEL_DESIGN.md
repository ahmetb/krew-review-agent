# Engineering Specification: Krew-Review-Agent Harness

## 1. Overview & Motivation

The `krew-review-agent` is a specialized, LLM-backed autonomous agent designed to review pull requests. The core motivation for this program is to eliminate the manual toil of reviewing highly structured, mechanical PRs (like Kubernetes Krew plugin submissions) by utilizing a Large Language Model (LLM) as the reasoning engine.

Unlike traditional procedural bots, this program implements an **Agentic Orchestration Loop**. It does not execute a rigid, predefined set of steps. Instead, it provides the LLM with a set of "tools" and a system prompt, allowing the LLM to autonomously gather context, read files, and decide when it has enough information to submit a final review.

## 2. Architectural Context

This Go program is designed to be a **stateless, ephemeral, short-lived worker**.

Architecturally, it sits behind an asynchronous message queue. The standard deployment model is:

1. GitHub Webhook -> Fast-ACK Receiver -> Pub/Sub or Cloud Tasks.

2. The queue invokes a Cloud Run container running this Go binary.

3. The queue passes the raw GitHub Webhook event payload to the program via **`stdin`**.

4. The program communicates with an LLM API (e.g., GLM5.2, Kimi) routed through an AI Gateway (like Cloudflare AI Gateway) for persistent prompt/response observability.

5. The program exits successfully (0) once the review is submitted, spinning down the container.

## 3. Program Interface

### 3.1 Inputs

The program is designed as a UNIX-style CLI tool to maximize portability (it can be run locally for debugging or wrapped in a Cloud Run container).

* **Payload:** Reads the raw GitHub Webhook JSON event (e.g., `pull_request` event) directly from `os.Stdin`.

* **Environment Variables:**

  * `GITHUB_TOKEN`: For fetching diffs and submitting comments.

  * `LLM_API_KEY`: Authentication for the LLM provider.

  * `LLM_BASE_URL`: Overridden to point to the AI Gateway.

  * `MAX_ITERATIONS`: (Default: 7) The circuit breaker limit for the agent loop.

### 3.2 Outputs

* **Stdout/Stderr:** Emits structured JSON logs (`log/slog`) containing trace IDs, iteration counts, and tool execution statuses.

* **GitHub API:** Submits exactly one HTTP POST request to the GitHub Issues API to leave the final review comment (including slash commands like `/lgtm`).

## 4. Tool Definitions (The "Fat Tool" Pattern)

To ensure the LLM succeeds, we employ a "Fat Tool" pattern—abstracting complex multi-step operations into single, robust Go functions. The LLM is provided with the following strict JSON schema of tools:

1. **`fetch_pr_diff(pr_number: int)`**

   * **Description:** Fetches the raw `.diff` of the Pull Request.

   * **Go Implementation:** Makes an authenticated HTTP GET request to GitHub's pull request diff endpoint.

2. **`fetch_plugin_manifest(file_path: string)`**

   * **Description:** Reads the contents of a specific file modified in the PR.

   * **Go Implementation:** Validates the `file_path` against directory traversal attacks. Reads the file directly from the GitHub API or the local cloned workspace.

3. **`get_all_existing_plugins()`**

   * **Description:** Returns a condensed list of all currently approved plugins (Name and Description) for duplicate comparison.

   * **Go Implementation (Fat Tool):** The LLM just calls this tool. Under the hood, the Go program checks if a `/tmp/krew-index` exists. If not, it executes `git clone --depth 1 <repo> /tmp/krew-index`. It then parses all YAML files in the directory, concatenates the names and descriptions, and returns the compiled string to the LLM.

4. **`submit_review_comment(body: string)` [TERMINAL TOOL]**

   * **Description:** Submits the final review to the PR. Calling this tool ends the execution loop.

   * **Go Implementation:** Makes an HTTP POST to the GitHub API to leave a comment. Sets a flag in the Go loop to cleanly terminate the program.

## 5. Agent Orchestration Loop (The State Machine)

The core of the Go program is the orchestration loop. It acts as the supervisor for the LLM.

### 5.1 Initialization

1. Parse the GitHub payload from `stdin`. Extract PR Number, Repo Name, Title, and Body.

2. Initialize the `MessageHistory` array.

3. Append `Role: System` containing the Review Guidelines (the brain).

4. Append `Role: User` containing the PR Context (e.g., "Please review PR #123 titled 'Add foo plugin'").

### 5.2 The `for` Loop

The program enters a bounded `for` loop, governed by `MAX_ITERATIONS`.

**Step 1: LLM Inference**

* Transmit `MessageHistory` to the LLM API.

* Block and wait for the response.

**Step 2: Response Routing**

* **Case A: The LLM returns a Tool Call (e.g., `get_all_existing_plugins`)**

  * Append the LLM's tool call message to `MessageHistory`.

  * Route the request to the corresponding Go function.

  * Execute the Go function and capture the string result (or error).

  * Append a new message to `MessageHistory` with `Role: Tool` and the content of the result.

  * *Continue to next loop iteration.*

* **Case B: The LLM returns the Terminal Tool (`submit_review_comment`)**

  * Execute the Go function to post to GitHub.

  * Log successful completion.

  * *Break the loop and exit(0).*

* **Case C: The LLM returns conversational text (No Tool Call)**

  * Append the LLM's text to `MessageHistory`.

  * Append a forced `Role: User` system warning: *"You must use a tool to gather data or use submit_review_comment to finish your task."*

  * *Continue to next loop iteration.*

### 5.3 Circuit Breakers

If the `for` loop index reaches `MAX_ITERATIONS` (e.g., 7), the Go program intervenes to prevent infinite loops (and API cost runaways).

1. The Go program appends a final `Role: User` message: *"CIRCUIT BREAKER: You have reached the maximum allowed tool executions. You must immediately output your findings using the submit_review_comment tool."*

2. It gives the LLM one final inference call.

3. If the LLM still fails to use the terminal tool, the Go program logs a `[FATAL]` error, posts a fallback comment to the PR indicating an internal agent failure, and exits with a non-zero status.

## 6. Security and Sandboxing

Because this agent processes untrusted third-party pull requests:

* **No Arbitrary Execution:** The LLM is NEVER given a raw `bash` or `exec` tool. It can only invoke the 4 explicitly defined Go functions.

* **Path Sanitization:** Any tool accepting a `file_path` (like `fetch_plugin_manifest`) enforces strict path sanitization (`filepath.Clean` in Go) to prevent relative path escapes outside of the designated `/tmp` workspace.

* **Ephemeral State:** By designing the program to ingest `stdin` and exit, we rely on the Cloud Run infrastructure to destroy the container and the `/tmp` disk after every single PR, ensuring zero state leakage between reviews.
