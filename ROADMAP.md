# Neo Roadmap

A small, readable Go coding agent — built to be understood. The goal is a
rock-solid core with capabilities layered on top as independent, feature-flagged
modules, so the codebase doubles as a teaching reference for how a coding agent
actually works.

Planning note: GitHub Issues are the source of truth for live issue status and
backlog planning. This roadmap is a curated product direction snapshot; if it
disagrees with an open issue, trust the issue and update this file.

## Architecture

Three layers, each in its own package, each replaceable:

- **Core agent loop** (`internal/agent`) — provider-agnostic turn loop. Sends
  messages, dispatches tool calls, preserves the `tool_use`/`tool_result`
  transcript invariant. Knows nothing about coding, files, or skills.
- **Capabilities** (`internal/tools`, and the modules below) — everything the
  core can be given: tools, a system prompt, project context, skills. Each is
  opt-in.
- **Surfaces** (`internal/tui`, `cmd/neo`) — the interactive chat (Bubble Tea).

A first pass at multi-step workflows was removed to keep the core focused; it
will be revisited deliberately once the core agent is rock solid (see Later).

## Done

- [x] **Agent core loop** — turn loop with strict transcript invariants; tool
      errors fed back to the model rather than aborting.
- [x] **Tool registry** — `Bash`, `ReadFile`, `WriteFile`, `EditFile`. Atomic
      writes, strict single-match edits, per-step tool whitelisting.
- [x] **Anthropic provider** — `Provider` interface with one implementation.
      Retries honor `Retry-After` headers, HTTP-date hints, caps, jitter, and
      cancellable waits.
- [x] **OpenAI provider** — API-key Responses API adapter plus experimental
      ChatGPT/Codex subscription auth code via `neo login` / `neo logout`;
      GitHub issue #66 remains the source of truth for open verification/status.
- [x] **Config** — `neo.yaml` → `~/.neo/config.yaml` → embedded default, with
      tri-state feature flags for layered capabilities.
- [x] **AGENTS.md loading** — `internal/projectctx` discovers project + global
      `AGENTS.md` and injects it into the chat prompt, behind `features.agents_file`.
- [x] **Skill loading** — `internal/skills` discovers `SKILL.md` files from
      `.neo/skills/` and `~/.neo/skills/`, advertises their name + description in
      the system prompt, and expands a skill's body when the user mentions
      `$name`. Behind `features.skills`. (Model-decided/dynamic triggering is a
      future layer.)
- [x] **TUI** — Bubble Tea v2 chat (blocking + spinner, no streaming by design),
      splash screen.
- [x] **Search tools, first pass** — `grep` and `glob` let the model inspect
      repositories without shelling out for common search operations, returning
      structured JSON with truncation metadata.
- [x] **Permission policy** — `ask`, `trusted`, and `readonly` modes gate tools
      while keeping path-shaped tools inside the workspace.
- [x] **Slash-command observability** — `/help`, `/tools`, `/permissions`,
      `/tokens`, `/model`, `/sessions`, and `/clear` expose session state.
- [x] **Session and model browsers** — `/sessions` resumes current-cwd sessions
      in the TUI, and `/model` switches the active model from a provider-aware
      picker.
- [x] **Permissions picker** — `/permissions` selects `ask`, `trusted`, or
      `readonly` for the current session.
- [x] **Fast TUI references and shell alias** — `@file` path completion and
      `!command` direct shell execution cover common terminal workflows.
- [x] **Teaching guides** — generated `docs/developer/guides/` pages explain the
      core concepts in problem/solution form.
- [x] **Docs freshness checks** — CI runs `go run ./cmd/neo-docs --check` on
      pull requests while the main-branch docs workflow can regenerate output.
- [x] **User docs for robust-core tools** — README documents permission modes,
      approval behavior, search tools, slash commands, sessions, `!`, and
      `@file`.
- [x] **Roadmap and ticket hygiene** — local planning docs now distinguish
      historical work from active next slices and point to GitHub Issues for
      source-of-truth status.

## Next: core robustness

- [ ] **Git context** — snapshot `git status`, branch, and recent log into the
      prompt at session start (GitHub issue #63).
- [ ] **Permission approval UX** — make approval prompts clearer, focused, and
      scrollable for long previews (GitHub issue #94).
- [ ] **TUI tool display polish** — group `/tools` output by capability and
      show how the current permission mode affects each group.
- [ ] **OpenAI subscription-auth verification** — verify GitHub issue #66
      against the live implementation, then implement gaps or update the issue
      tracker.
- [ ] **Session transcript search** — add CLI/API saved-session search as the
      first useful form of episodic memory; the TUI session browser already has
      in-view filtering.
- [ ] **Memory stub** — add an experimental, disabled-by-default `/memory`
      surface before implementing project memory.
- [ ] **Teaching guide polish** — keep improving generated guides from
      `cmd/neo-docs` with practical examples, not hand edits.
- [ ] **Context compaction** — token-aware summarization at a threshold, cutting
      only at valid points (never mid-tool-result; GitHub issue #65).

## Later

- [x] **Prompt caching** — `cache_control` on the static system prompt; keep the
      dynamic sections (git, project context) separate to maximize cache hits.
- [ ] **`neo update`** — self-update the installed binary from GitHub Releases.
      See `TICKETS.md` NEO-1.
- [ ] **Release automation** — replace manual tag pushes with a release PR/tag
      flow so changes are regularly shipped.
- [ ] **Model catalog** — context-window sizes and pricing to drive compaction
      thresholds and cost display.

## Exploration (once the core is rock solid)

- [ ] **Workflows, done properly** — revisit multi-step orchestration with a
      clear design, rather than the half-finished engine that was removed.
- [ ] **Long-lived / always-on mode** — an agent that stays resident and reacts
      to incoming events (a "Hermes"-style listener) rather than one-shot turns.

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
