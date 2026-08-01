# Architecture

Neo is a small Go coding agent. The core agent loop is policy-free: it owns message state, calls an LLM provider, emits events, and runs injected tools. Product behavior is layered around that loop.

## Main Modules

| Path | Responsibility |
| --- | --- |
| `cmd/neo/` | Process lifecycle, stream-injected command dispatch, and chat session startup. |
| `internal/agent/` | Core agent loop, transcript state, event model, tool-use continuation. |
| `internal/approval/` | Literal matcher for optional interactive tool confirmations. |
| `internal/auth/` | OpenAI ChatGPT/Codex device-code login, token refresh, and stored subscription credentials. |
| `internal/compact/` | Compaction interface, summarizing compactor, and safe split helpers. |
| `internal/config/` | Config discovery, defaults, and feature flags. |
| `internal/factory/` | Subagent runtime for the chat `agent` tool: supervisor budgets, node tree, and event stream. |
| `internal/llm/` | Provider-neutral request/response types and system prompt blocks. |
| `internal/llm/anthropic/` | Anthropic provider adapter. |
| `internal/llm/openai/` | OpenAI provider adapters for API-key Responses API calls and ChatGPT/Codex subscription calls. |
| `internal/llm/chatcompletions/` | Reusable OpenAI-compatible Chat Completions adapter. |
| `internal/llm/openrouter/` | OpenRouter provider setup and defaults. |
| `internal/llm/google/` | Google Gemini adapter. |
| `internal/projectctx/` | AGENTS.md discovery and prompt augmentation. |
| `internal/session/` | File-backed session metadata and transcripts. |
| `internal/skills/` | Skill discovery, catalog rendering, and $name or /name expansion. |
| `internal/tools/` | Built-in tools exposed to the model. |
| `internal/tui/` | Bubble Tea terminal UI and transcript rendering. |
| `internal/workspace/` | Workspace helpers shared by project-context features. |

## Chat Startup Flow

1. `cmd/neo` loads config.
2. Provider construction selects Anthropic, OpenAI, OpenRouter, or Google Gemini. OpenAI defaults to API-key auth; `openai_auth: subscription` builds the Codex subscription provider from stored device-code credentials.
3. The CLI creates or loads a session from `internal/session`.
4. Skills and AGENTS.md are discovered when enabled.
5. `chatSystem` builds both flattened and segmented system prompts.
6. `agent.New` receives the provider, tools, optional interactive approval matcher, system prompt, and restored messages.
7. `tui.Run` owns user interaction and saves the transcript after each send.

## Execution Boundary

Neo assumes its VM or sandbox controls filesystem, process, network, credential,
and external-service access. Neo does not classify commands or enforce a
security policy. Interactive `tool_approvals` add optional confirmation before
selected coordinator calls; they are not passed to headless or child agents.

Inspect children are constrained by capability selection: their registry
contains only `read_file`, `grep`, and `glob`.

## Agent Loop Contract

The agent appends one user text message per `Send` call. Each provider response becomes an assistant message. If the assistant requests tools, Neo runs them, caps oversized output before it enters user `tool_result` blocks, and continues the provider loop until the assistant ends the turn or max turns is reached.
