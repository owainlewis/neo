# Neo Roadmap

Build the best Rust coding agent in the world.

## Done

- [x] **Workspace split** — `neo-core` (pure loop), `neo-coding` (coding bundle), binary (TUI + wiring)
- [x] **Streaming Provider** — SSE parsing for Anthropic, real-time text deltas to UI, errors as terminal events
- [x] **Hooks system** — `Hooks` trait with 5 optional methods, `HookChain` for composition. Plan mode and approval are hooks, not baked into the core.
- [x] **Subagent spawner** — `SubagentSpawner` trait + `DefaultSpawner` + `DispatchTool` for parallel workers
- [x] **Customizable system prompt** — `--system-prompt <path>` flag for pure agent mode (non-coding agents)
- [x] **Eager tool result clearing** — Replaces old tool result content with `[cleared]` before each API call
- [x] **Plan mode** — Read-only exploration mode via `PlanModeHook`
- [x] **Write tool + permission system** — Approval prompts for non-read-only tools
- [x] **Danger guard** — Pattern-based guard hook for dangerous commands
- [x] **PI-style TUI** — Ratatui-based terminal UI with streaming output
- [x] **52 tests** — Unit + integration tests across the workspace

- [x] **Critical fixes** — Async approval (oneshot), subagent usage propagation (`ToolOutput`), subagent timeout, mid-loop tool result preservation

## Phase 1: Core Robustness

- [ ] **Grep tool** — Ripgrep-powered content search with context lines. The most-used tool in any coding agent.
- [ ] **Glob tool** — Fast file pattern matching (`**/*.rs`). Avoids burning tokens on `bash find`.
- [ ] **Real context compaction** — Replace `[cleared]` hack with token-accurate compaction. Use `usage.total_tokens` from the last assistant message, estimate only the tail, find valid cut points (never mid-tool-result), and summarize into a structured checkpoint (Goal / Done / InProgress / Blocked / Next Steps).
- [x] **API retry with backoff** — Exponential backoff + jitter for transient 429/500 errors. Respects Retry-After header (capped at 30s), max 3 attempts.
- [ ] **Cancellation support** — `CancellationToken` threaded through tool execution and the agent loop. Ctrl+C during processing should cancel the current turn gracefully, not kill the process.
- [ ] **Token estimation** — Rough pre-flight estimate (chars/4) to trigger compaction proactively. Trust provider `usage.total_tokens` from the last response, only estimate the tail.

## Phase 2: Tools

- [ ] **File mutation queue** — Serialize concurrent writes/edits behind a lightweight lock. Unlocks safe parallel tool execution without the current "reads concurrent, writes serial" partition.
- [ ] **Tool factories with injectable operations** — Each tool takes `(cwd, Operations)` where `Operations` is a small trait (e.g. `BashOperations::exec`). Enables SSH/sandbox/test backends without touching tool logic.

## Phase 3: Prompt & Config

- [ ] **CLAUDE.md / NEO.md loading** — Load project instructions from `.neo/NEO.md`, `~/.neo/NEO.md`, and `.neo/rules/*.md`. Inject into system prompt as dynamic sections.
- [ ] **Dynamic system prompt sections** — Split prompt into static (cacheable) and dynamic (per-turn: cwd, git status, loaded project instructions) sections. Maximizes prompt cache hits.
- [ ] **Git context injection** — Snapshot `git status`, `git log --oneline -5`, branch name into the prompt at conversation start.
- [ ] **Steering vs follow-up messages** — Two queues: steering injects between turns of an active run, follow-up fires only when the agent would otherwise stop. Handles "user typed while agent was working."

## Phase 4: Provider Layer

- [ ] **Multi-provider support** — Add OpenAI, Google, and local model providers behind the same `Provider` trait. Provider registry with lazy loading.
- [ ] **Prompt caching** — Send `cache_control` headers to Anthropic for the system prompt. Subagents inherit the parent's prompt verbatim to share the cache.
- [ ] **Model catalog** — Known models with context window sizes, pricing, capability tiers. Used for automatic compaction thresholds and cost display.

## Phase 5: Subagent Improvements

- [ ] **Event bubble-up** — Subagent events stream into the parent's `EventBus` tagged with subagent ID. TUI can show a tree view of parallel work.
- [ ] **Subagent cancellation** — Cancel individual or all subagents via `CancellationToken`. Wire into Ctrl+C handling.
- [ ] **Custom tool sets per subagent** — `SubagentSpec` accepts optional tool overrides. An explore-only subagent gets `[read, glob, grep]` instead of the full set.
- [ ] **Prompt cache sharing** — Subagents inherit the parent's system prompt verbatim so they hit the API prompt cache.

## Design Principles

- **One strategy, not three.** Prefer a single general approach over multiple specialized ones.
- **Lossless first, lossy second.** Clear stale tool results before summarizing conversation.
- **Tokens are the bottleneck.** Every feature should be evaluated by its token efficiency.
- **Rust idioms.** Enums over inheritance, traits over traits, zero-cost abstractions.
- **Core is policy-free.** The loop knows nothing about coding, files, or approval. All behavior is injected via hooks and tool sets.
- **Streams never throw.** Errors are data (terminal events). The loop never catches stream exceptions.
