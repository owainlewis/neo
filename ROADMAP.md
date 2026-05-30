# Neo Roadmap

A small, readable Go coding agent — built to be understood. The goal is a
rock-solid core with capabilities layered on top as independent, feature-flagged
modules, so the codebase doubles as a teaching reference for how a coding agent
actually works.

## Architecture

Three layers, each in its own package, each replaceable:

- **Core agent loop** (`internal/agent`) — provider-agnostic turn loop. Sends
  messages, dispatches tool calls, preserves the `tool_use`/`tool_result`
  transcript invariant. Knows nothing about coding, files, or skills.
- **Capabilities** (`internal/tools`, and the modules below) — everything the
  core can be given: tools, a system prompt, project context, skills. Each is
  opt-in.
- **Surfaces** (`internal/tui`, `cmd/neo`) — interactive chat (Bubble Tea) and
  the headless `flow`/`step` CLI. Both drive the same core.

Workflows (`internal/workflow`, `internal/phase`) are a thin orchestration layer
that reuses the core: ordered Markdown steps with templated prompts, retry-from
semantics, and structured pass/fail verdicts.

## Done

- [x] **Agent core loop** — turn loop with strict transcript invariants; tool
      errors fed back to the model rather than aborting.
- [x] **Tool registry** — `Bash`, `ReadFile`, `WriteFile`, `EditFile`. Atomic
      writes, strict single-match edits, per-step tool whitelisting.
- [x] **Anthropic provider** — `Provider` interface with one implementation.
      Exponential backoff + jitter on 429/5xx, respects `Retry-After` (cap 30s).
- [x] **Workflow engine** — multi-step flows, retry-from a named step, max-rounds
      cap, cross-step template context (`.Task` / `.Round` / `.Prev` / `.Steps`).
- [x] **Verdict detection** — structured ```` ```neo-result ```` JSON block,
      with a prose-marker fallback for older prompts.
- [x] **Config** — `neo.yaml` → `~/.neo/config.yaml` → embedded default;
      Markdown step prompts with optional YAML frontmatter (tool/model override).
- [x] **TUI** — Bubble Tea v2 chat (blocking + spinner, no streaming by design),
      workflow status grid, splash screen.
- [x] **Artifact store** — per-run, per-step outputs written under `.agent/runs`.

## Next: capability modules (feature-flagged)

Each lands as its own package behind a config flag, wired in `cmd/neo`. The point
is that a reader can turn one off and see exactly what it contributed.

- [ ] **Feature flags** — a `features` block in `neo.yaml` and a `Features`
      struct. Every capability below checks its flag. Core is always on.
- [ ] **AGENTS.md loading** — discover `AGENTS.md` (project, walking up to repo
      root, plus `~/.neo/AGENTS.md`) and inject as a dynamic system-prompt
      section. Flag: `features.agents_file`.
- [ ] **Skill loading** — Codex-style skills. Discover `SKILL.md` files (name +
      description frontmatter) from `.neo/skills/` and `~/.neo/skills/`. Reference
      a skill in input with `$skill-name` to expand its body into the turn.
      Available skills are advertised to the model. Flag: `features.skills`.

## Then: core robustness

- [ ] **Grep tool** — content search with context lines. The most-used tool in
      any coding agent; today the model shells out to `bash grep`.
- [ ] **Glob tool** — fast file pattern matching (`**/*.go`). Avoids `bash find`.
- [ ] **Git context** — snapshot `git status`, branch, and recent log into the
      prompt at session start.
- [ ] **Cancellation** — Ctrl+C / Esc cancels the current turn gracefully through
      tool execution, not just at the TUI layer.
- [ ] **Context compaction** — token-aware summarization at a threshold, cutting
      only at valid points (never mid-tool-result).

## Later

- [ ] **Prompt caching** — `cache_control` on the static system prompt; keep the
      dynamic sections (git, project context) separate to maximize cache hits.
- [ ] **Second provider** — a non-Anthropic `Provider` to prove the seam holds.
- [ ] **Model catalog** — context-window sizes and pricing to drive compaction
      thresholds and cost display.

## Design principles

- **Core is policy-free.** The loop knows nothing about coding, files, skills, or
  approval. Capabilities are injected; the core just runs turns.
- **One strategy, not three.** Prefer a single general approach over several
  specialized ones.
- **Readable over clever.** This codebase is a teaching artifact. Optimize for the
  next person reading it, not for line count.
- **Capabilities are opt-in and isolated.** Each lives in its own package behind a
  flag, so its contribution is legible in isolation.
- **Tokens are the bottleneck.** Evaluate features by their token efficiency.
- **Errors are data.** Tool failures flow back to the model as results, not panics.
