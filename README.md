# krew-review-agent

An LLM-backed autonomous agent that reviews pull requests to the
[`kubernetes-sigs/krew-index`](https://github.com/kubernetes-sigs/krew-index)
repository — the curated index of plugins for [Krew](https://krew.sigs.k8s.io/),
the `kubectl` plugin manager.

> **Note:** This is a personal project, built and run by me to help with my own
> maintenance work on the Krew index. It's shared publicly so others can see how
> it works and borrow ideas, but it isn't packaged for general use — there are
> no installation or deployment instructions here, and you probably don't need
> to run it yourself.

## What it does

Maintaining a curated plugin index means reviewing a steady stream of highly
structured, mechanical pull requests: plugin version bumps, new plugin
submissions, manifest tweaks. Most of these follow the same patterns and the
same rules over and over. That's a lot of repetitive reviewer toil — and with
the rise of AI coding tools, the submission rate has only gone up, putting more
strain on a small volunteer reviewer community.

`krew-review-agent` automates the first pass. When a pull request is opened
against the Krew index, the agent:

- **Approves straightforward version bumps** automatically (e.g. a PR that only
  changes `uri`, `sha256`, and `version`), posting a `/lgtm` `/approve` to let
  it auto-merge.
- **Closes PRs that clearly violate the rules** — for example, version numbers
  going backwards, pre-release tags, or bot-submitted new plugins — with a
  friendly explanation of *why* the rule exists and how to fix it.
- **Flags anything that needs a human** — new plugin submissions, plugin
  renames, changes to a plugin's origin repository — by leaving review notes and
  adding a `needs-human-review` label, so a maintainer can make the final call.

It also checks new submissions against the whole existing index for
naming-guideline compliance (kebab-case, no `kube-`/`kubectl-` prefixes, no
overly generic names, and so on) and for functional overlap with plugins that
are already published.

The goal is not to replace human judgment on what belongs in a curated index,
but to handle the mechanical checks and clear-cut cases so a maintainer only
spends attention where it's genuinely needed.

## How it works

The heart of the project is an **agentic orchestration loop**, not a rigid
script. Rather than executing a fixed sequence of checks, the program hands a
Large Language Model a set of tools and a system prompt (the review guidelines)
and lets it decide, step by step, what context it needs and when it has seen
enough to render a verdict.

### The agent loop

The program feeds the LLM the pull request's context and then runs a bounded
loop:

1. The LLM is asked what to do next.
2. If it calls one of the data-gathering tools, the Go program runs that tool
   and feeds the result back into the conversation.
3. This repeats until the LLM decides it's done and calls a **terminal tool** to
   either submit its review or record that no review was needed.

A circuit breaker caps the number of iterations so a confused or looping model
can never run up unbounded API cost — if it hits the limit, it's forced to
either finish or post a fallback message.

### The tools

The LLM never gets a raw shell or arbitrary file access. It can only invoke a
small, fixed set of purpose-built Go functions (a "fat tool" pattern, where each
tool wraps a complete, robust operation):

| Tool | Purpose |
|---|---|
| `fetch_pr_diff` | Get the raw diff of the pull request. |
| `fetch_plugin_manifest` | Read a plugin's current manifest by name. |
| `get_all_existing_plugins` | List every approved plugin (with descriptions) for duplicate detection. |
| `submit_review_comment` | **Terminal.** Post the final review, optionally flagging for human review. |
| `noop` | **Terminal.** Record that the PR needed no review (e.g. it doesn't touch `plugins/`). |

Because the review rules live in a system prompt rather than in code, tuning how
the agent reviews is mostly a matter of editing prose — see
[`SYSTEM_PROMPT.md`](./SYSTEM_PROMPT.md).

### The pipeline

The agent is a stateless, short-lived worker that sits behind an asynchronous
queue, so the review logic is fully decoupled from webhook intake:

```
GitHub webhook ──▶ Event Gateway ──▶ Pub/Sub ──▶ Agent Worker ──▶ review comment on the PR
```

- The **Event Gateway** is a thin HTTP service that receives GitHub webhooks,
  verifies their signatures, filters out anything that isn't a relevant
  pull-request event, and quickly hands the rest off to a message queue. This
  keeps GitHub's short webhook-delivery deadline safe no matter how slow the
  actual review turns out to be.
- The **Agent Worker** picks up a queued event, runs the agent loop against it,
  posts its review, and exits. Each review runs in isolation and leaves nothing
  behind.

The LLM calls are routed through an AI gateway, which provides observability
into every prompt and response. The design is provider-agnostic — any
OpenAI-compatible model can be plugged in.

## Design & safety

Because the agent processes untrusted, third-party pull requests, safety is a
first-class concern:

- **No arbitrary execution.** The model can only call the handful of defined
  tools — never a shell or `exec`.
- **Path sanitization.** Tools that read files take a plugin *name*, not a path,
  so the model can't traverse the filesystem or escape its workspace.
- **Ephemeral state.** Each review runs in a throwaway environment that's
  destroyed afterward, so nothing leaks between reviews.

The full design is documented under [`design/`](./design):

- [`HIGH_LEVEL_DESIGN.md`](./design/HIGH_LEVEL_DESIGN.md) — the agent harness,
  tools, and orchestration loop.
- [`AGENT_CLI.md`](./design/AGENT_CLI.md) — the agent worker implementation.
- [`EVENT_GATEWAY.md`](./design/EVENT_GATEWAY.md) — the webhook receiver.
- Companion deployment specifications for each component.

## License

Apache License 2.0 — see [`LICENSE`](./LICENSE).
