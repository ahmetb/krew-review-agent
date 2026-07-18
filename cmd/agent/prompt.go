package main

import _ "embed"

// systemPrompt is the reviewer's system prompt, embedded into the binary at
// build time. It lives in this package (rather than the module root) so that
// edits to it are covered by the cmd/agent/** CI path filter, and a
// prompt-only change still triggers a rebuild and deploy.
//
//go:embed SYSTEM_PROMPT.md
var systemPrompt string
