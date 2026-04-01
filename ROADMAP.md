# Opus Roadmap

Build the best Rust coding agent in the world.

## Phase 1: Quick Wins

- [ ] **Glob tool** — Fast file pattern matching (`**/*.rs`). Avoids burning tokens on `bash find`.
- [ ] **Grep tool** — Ripgrep-powered content search with context lines. The most-used tool in any coding agent.
- [ ] **Token estimation** — Rough pre-flight estimate (4 bytes/token) to know where you stand before sending to the API.
- [ ] **API retry with backoff** — Exponential backoff + jitter for transient 429/500 errors.

## Phase 2: Context Management

- [ ] **Eager tool result clearing** — Before each API call, replace all `ToolResult` content blocks except those in the final user message with `[cleared]`. The model already digested those results into its subsequent reasoning — the raw content is dead weight. Zero-cost, deterministic, no threshold tuning.
- [ ] **Conversation summarization (later, if needed)** — If conversations still blow the context window after clearing, ask the model to summarize the oldest messages into a compact prefix. Only add this when eager clearing proves insufficient.

## Phase 3: Prompt & Config

- [ ] **CLAUDE.md loading** — Load project instructions from `.claude/CLAUDE.md`, `~/.opus/CLAUDE.md`, and `.claude/rules/*.md`. Inject into system prompt as dynamic sections.
- [ ] **Dynamic system prompt** — Split prompt into static (cacheable) and dynamic (per-turn: cwd, git status, loaded CLAUDE.md) sections.
- [ ] **Git context injection** — Snapshot `git status`, `git log --oneline -5`, branch name into the prompt at conversation start.

## Phase 4: Permissions & Safety

- [ ] **Pattern-based permissions** — Allow/deny/ask rules per tool, with glob patterns on arguments (e.g., allow `bash(git *)`, ask for `bash(rm *)`).
- [ ] **Denial tracking** — After N denials of the same tool pattern, offer permanent allow/deny for the session.
- [ ] **Plan mode** — Read-only exploration mode where the agent can only use read/search tools.

## Phase 5: Persistence

- [ ] **Session persistence** — Save transcripts to `~/.opus/sessions/{id}.json`. Support `/resume` to reload.
- [ ] **Session memory** — On exit or compact, distill key facts into `~/.opus/memory/` for future sessions.

## Phase 6: Subagents

- [ ] **Agent tool** — Spawn child agents with isolated message history and a tool subset.
- [ ] **Explore agent** — Search-only subagent (glob, grep, read) for codebase research without polluting the main context.
- [ ] **Prompt cache sharing** — Fork subagents inherit the parent's system prompt verbatim so they hit the API prompt cache.

## Design Principles

- **One strategy, not three.** Prefer a single general approach over multiple specialized ones.
- **Lossless first, lossy second.** Clear stale tool results before summarizing conversation.
- **Tokens are the bottleneck.** Every feature should be evaluated by its token efficiency.
- **Rust idioms.** Enums over inheritance, traits over interfaces, zero-cost abstractions.
