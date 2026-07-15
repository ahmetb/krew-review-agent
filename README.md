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

- **Approves straightforward version bumps** automatically, so they can
  auto-merge.
- **Closes PRs that clearly violate the rules**, with a friendly explanation of
  *why* the rule exists and how to fix it.
- **Flags anything that needs a human** — new plugin submissions and other
  judgment calls — so a maintainer can make the final decision.

The goal is not to replace human judgment on what belongs in a curated index,
but to handle the mechanical checks and clear-cut cases so a maintainer only
spends attention where it's genuinely needed.

## How it works

The heart of the project is a **limited-turn agent loop**, not a rigid script.
The program hands a Large Language Model a system prompt (the review guidelines)
and a small, fixed set of tools — reading the PR diff, reading a plugin
manifest, listing existing plugins, and posting the final review — and lets it
decide, step by step, what context it needs before rendering a verdict. It never
gets a shell or arbitrary file access, and a per-review turn limit caps how long
it can run.

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
