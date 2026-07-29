# Neo Architecture

## 1. Executive Summary

Neo is a local coding agent distributed as one Go binary. It runs in a
repository, sends conversation state and tool definitions to a configured LLM
provider, executes approved tools on the user's machine, and renders the work in
a terminal UI or as a headless command.

The core architectural rule is that the agent loop is policy-free.
`internal/agent` owns the transcript, provider calls, tool-use continuation,
parallel tool scheduling, steering, compaction, and events. It does not know
about configuration files, sessions, AGENTS.md, skills, the terminal UI, or any
specific model vendor. `cmd/neo` composes those product capabilities around the
loop through interfaces.

Neo is local-first. There is no Neo server, database, daemon, plugin runtime, or
telemetry service. The process talks directly to the selected model provider and
stores configuration, credentials, and resumable sessions on the local
filesystem.

Neo is a Go module licensed under the MIT License.

---

### System Architecture

```text
┌─────────────────────────────────────────────────────────────────────────┐
│                              USER SURFACES                              │
│                                                                         │
│  Interactive terminal          Headless command          Utility CLI    │
│  neo / neo chat                neo run                   doctor, login, │
│  Bubble Tea TUI                text or JSON output       sessions, etc. │
└───────────────┬────────────────────────┬────────────────────────┬────────┘
                │                        │                        │
                └────────────────────────┼────────────────────────┘
                                         ▼
┌─────────────────────────────────────────────────────────────────────────┐
│                         cmd/neo COMPOSITION ROOT                        │
│                                                                         │
│  config  provider  session  system prompt  tools  permissions           │
│  skills  AGENTS.md  subagent supervisor  workflow events               │
└───────────────┬───────────────────────────────┬─────────────────────────┘
                │                               │
                ▼                               ▼
┌───────────────────────────────┐   ┌─────────────────────────────────────┐
│      internal/agent           │   │       Product capabilities          │
│                               │   │                                     │
│  transcript and usage         │   │  TUI, sessions, auth, project       │
│  compact → complete → tools   │   │  context, skills, workflows,        │
│  steering and cancellation    │   │  subagent supervision               │
│  typed event stream           │   │                                     │
└──────────┬───────────┬────────┘   └─────────────────────────────────────┘
           │           │
           ▼           ▼
┌──────────────────┐  ┌──────────────────────────────────────────────────┐
│ internal/llm     │  │ internal/tools + internal/permission             │
│ Provider         │  │                                                  │
│ interface        │  │ bash, file I/O, search, workflow, agent          │
└────────┬─────────┘  └────────────────────────┬─────────────────────────┘
         │                                     │
         ▼                                     ▼
┌───────────────────────────────┐   ┌─────────────────────────────────────┐
│ Anthropic, OpenAI, Codex,     │   │ Local repository, shell processes, │
│ OpenRouter, Google Gemini     │   │ ~/.neo sessions and credentials    │
└───────────────────────────────┘   └─────────────────────────────────────┘
```

The composition root is intentionally the only place that knows the full
application. Core packages expose small interfaces and do not reach upward into
the CLI or TUI.

---

### Dependency Direction

```text
internal/llm          internal/tools          internal/permission
     ▲                      ▲                         ▲
     │                      │                         │
     └──────────── internal/agent ────────────────────┘
                              ▲
                              │
              internal/factory and internal/tui
                              ▲
                              │
                           cmd/neo

Supporting leaf packages:
  internal/atomicfile   internal/auth       internal/compact
  internal/config       internal/logx       internal/projectctx
  internal/session      internal/skills     internal/workflow
  internal/workspace
```

Key rules:

- `internal/llm` defines the provider-neutral conversation protocol.
- `internal/tools` defines executable capabilities, not when they are allowed.
- `internal/permission` decides whether a concrete tool call is allowed, denied,
  or requires approval.
- `internal/agent` coordinates providers, tools, and policy through interfaces.
- `internal/factory` reuses the core loop for supervised child agents.
- `internal/tui` consumes events and supplies user interaction. It does not
  implement agent decisions.
- `cmd/neo` owns construction, feature selection, and cross-package wiring.

---

## 2. Process and Startup Lifecycle

Neo is a single foreground process. `cmd/neo/main.go` installs signal handling,
initializes optional debug logging, dispatches the command, and exits when that
command finishes.

### Interactive Chat Startup

`neo` and `neo chat` follow this sequence:

```text
1. Load the first available neo.yaml configuration
2. Select the provider and model
3. Create or restore the local session
4. Resolve the repository workspace root
5. Discover skills and AGENTS.md files when enabled
6. Build flattened and segmented system prompts
7. Construct workflow and subagent capabilities
8. Build the tool registry and permission policy
9. Construct the core agent with restored messages and usage
10. Start the Bubble Tea TUI
11. Save the session after each completed user send
```

Configuration uses first-hit semantics:

```text
./neo.yaml
    ↓ when absent
~/.neo/config.yaml
    ↓ when absent
embedded internal/config/defaults/neo.yaml
```

Files are not merged. Parsing applies defaults and validates provider,
authentication, subagent backend, and permission values before the chat starts.

### Resume

`neo resume <id>` loads a session, attempts to restore its saved working
directory, and prefers its saved provider and model when the required local
credential source is available. If that backend is unavailable, startup warns
and uses the configured default. Provider-specific opaque transcript blocks are
preserved in storage; adapters ignore blocks they cannot safely replay.

### Headless Run

`neo run` uses the same provider interface, agent loop, compactor, base tools,
project instructions, and advertised skill catalog as interactive chat, with
these differences:

- it processes one prompt and exits;
- it defaults to read-only permissions;
- read-only mode exposes only `read_file`, `grep`, and `glob`;
- it has a default ten-minute wall-clock timeout;
- it does not create or update a session;
- it can return a JSON summary with timing and tool counts;
- it does not expand `$name` or `/name` skill invocations;
- it has no TUI approver, subagent tool, or visible workflow tool.

---

## 3. Provider-Neutral Conversation Model

`internal/llm` is the boundary between Neo and model vendors.

### Core Types

```go
type Provider interface {
    Name() string
    Complete(context.Context, Request) (*Response, error)
}

type Request struct {
    Model        string
    System       string
    SystemBlocks []SystemBlock
    Messages     []Message
    Tools        []ToolSpec
    MaxTokens    int
}

type Message struct {
    Role    Role
    Content []ContentBlock
}
```

A content block can contain text, an image, a tool request, a tool result, or
opaque provider data. Opaque `Raw` blocks let an adapter replay provider
metadata such as reasoning items without making the core loop understand it.

### Provider Adapters

| Package | Transport | Authentication |
| --- | --- | --- |
| `internal/llm/anthropic` | Anthropic Messages API | `ANTHROPIC_API_KEY` |
| `internal/llm/openai` | OpenAI Responses API | `OPENAI_API_KEY` |
| `internal/llm/openai` Codex client | ChatGPT Codex Responses stream | stored device-code credentials |
| `internal/llm/openrouter` + `internal/llm/chatcompletions` | OpenAI-compatible Chat Completions API | `OPENROUTER_API_KEY` |
| `internal/llm/google` | Gemini generateContent API | `GOOGLE_API_KEY` |

Each adapter translates the shared messages and tools into vendor wire types,
normalizes stop reasons and usage, and applies the shared retry policy to
transient transport or HTTP failures. The agent loop sees only normalized
responses. `internal/llm/chatcompletions` owns the reusable OpenAI-compatible
wire conversion and HTTP client used by OpenRouter.

`internal/llm/retry` is the shared transient-failure boundary for Anthropic,
OpenAI, Codex, Google, and Chat Completions transports. It centralizes retry
classification, `Retry-After` handling, backoff, and cancellation-aware waits.

### System Prompt Blocks

Neo maintains both a flattened system prompt and ordered `SystemBlock` values:

```text
Block 1: static Neo instructions + skill catalog     cacheable
Block 2: discovered AGENTS.md project instructions   not cacheable
```

Anthropic can turn the cache marker into a provider-side cache breakpoint.
Providers without matching support concatenate the blocks and use the flattened
text. This keeps caching an adapter capability rather than a requirement of the
agent loop.

---

## 4. Agent Turn Lifecycle

`internal/agent.Agent` owns in-memory messages and cumulative usage for one
conversation. One call to `Send` or `SendWith` appends one user message and
starts the provider/tool loop.

```text
1. Append the user text and best-effort image attachments
2. Compact the transcript when it crosses the configured threshold
3. Call Provider.Complete with messages, system prompt, and tool specs
4. Add response usage to the session total
5. Emit assistant text and tool activity as events
6. Execute requested tools through the permission path
7. Append the assistant message and matching tool results atomically
8. Continue at step 2 while tool results or steering require another response
9. Emit done when the provider ends the turn
```

The normal stop condition is the provider ending its turn. `MaxTurns` defaults
to 500 as a runaway-loop fuse, not as a work budget. A provider
`max_tokens` stop returns the partial text with a distinct truncation error
instead of silently asking the provider to continue.

### Transcript Invariants

The loop builds an assistant response and all matching tool results before
committing either to the transcript. This preserves two important invariants:

- every `tool_use` has a corresponding `tool_result`;
- tool results remain in model-request order, even when execution was parallel.

Unknown, denied, failed, canceled, and skipped calls still produce error-shaped
tool results. This keeps the transcript structurally valid for the next
provider call.

Tool result content is capped at 256 KiB before it enters the transcript.
Individual tools also apply narrower limits where appropriate.

### Steering and Cancellation

The TUI can add steering while a turn is active. Neo waits until the current
provider response and any already-running tool group are structurally complete,
then appends the steering as user text. Remaining calls are represented as
skipped results. This avoids orphaning provider tool calls.

Context cancellation stops new work, propagates to providers and tools, and
still records results for calls that were already announced.

### Compaction

The default summarizer estimates transcript size at roughly four characters per
token and triggers at 70 percent of the configured context window. It asks the
active provider to summarize older messages while retaining the most recent 20.

The split point is moved backward to a fresh user turn so compaction never
separates a tool result from the assistant tool request that created it. If no
safe boundary exists, the transcript is left unchanged.

---

## 5. Tool Execution

Tools implement one interface:

```go
type Tool interface {
    Name() string
    Spec() llm.ToolSpec
    Run(context.Context, map[string]any) (string, error)
}
```

The registry gives sorted tool specifications to providers and resolves names
at runtime. Two optional capability interfaces classify concrete calls:

- `ParallelTool` says whether this input is safe to run concurrently;
- `ReadOnlyTool` says whether this input can run under read-only policy.

The runtime owns both decisions. Model-supplied arguments cannot declare
themselves parallel-safe or read-only.

### Built-in Tools

| Tool | Package | Behavior |
| --- | --- | --- |
| `bash` | `internal/tools` | Runs `/bin/bash -c` in the session directory with a default two-minute timeout and bounded output. |
| `read_file` | `internal/tools` | Reads a file or a line window, capped at about 256 KiB. |
| `write_file` | `internal/tools` | Creates parent directories and writes a file. |
| `edit_file` | `internal/tools` | Replaces exactly one occurrence of a string. |
| `grep` | `internal/tools` | Regex search under the workspace, capped by match count. |
| `glob` | `internal/tools` | Glob search under the workspace, capped by match count. |
| `workflow` | `internal/workflow` | Emits checklist state for the interactive UI. |
| `agent` | `internal/factory` | Runs a supervised child agent and returns its report. |

The composition root decides which tools are present. The core loop does not
special-case any tool name.

### Parallel Scheduling

Only adjacent runs of parallel-safe calls from one provider response are
eligible for concurrent execution. Neo:

1. resolves tools and makes permission decisions serially;
2. splits the run at approval barriers;
3. executes allowed parallel groups with a bounded semaphore;
4. waits for the whole group;
5. emits and appends results in original request order.

The default maximum is eight concurrent tool calls. Unclassified tools fail
closed to serial execution. File reads and searches are parallel-safe. Writable
tools and `bash` are serial. An `agent` call is parallel-safe only in
read-only `inspect` mode.

---

## 6. Permissions and Workspace Boundary

Every model-requested call passes through `permission.Policy` before execution.
The decision is one of `Allow`, `Ask`, or `Deny`.

| Mode | Read operations | Mutating operations |
| --- | --- | --- |
| `readonly` | allowed | denied |
| `ask` | allowed | require user approval |
| `trusted` | allowed | allowed |

Path-shaped file tools are denied when their resolved path is outside the
workspace root. `internal/workspace.ResolveWithin` also evaluates existing
symlinks and the nearest existing ancestor of a new path, which blocks common
symlink escapes.

In `ask` mode, dangerous shell patterns and obvious paths outside the workspace
receive an explicit approval reason. Examples include `sudo`, recursive forced
removal, recursive ownership or mode changes, and destructive Git commands.
The TUI can grant a narrow in-memory allow rule for the current session, but
high-risk shell calls always require a fresh decision.

`bash` is a real local shell, not a sandbox. In trusted mode it has the authority
of the Neo process. The permission layer is a user-consent and workspace policy,
not operating-system isolation.

---

## 7. Interactive TUI and Event Flow

`internal/tui` is a Bubble Tea application. It owns input, transcript rendering,
tool cards, workflow state, subagent trees, model selection, approvals, and
steering. It does not call providers directly.

```text
Agent events ───────────────┐
Factory supervisor events ─┼─→ Bubble Tea messages → model.Update → View
Workflow tool events ──────┘

Approval request ←──────────── TUI choice
User send/steer ─────────────→ Agent
AfterSend callback ──────────→ Session store
```

The agent event model includes assistant text, commentary, parallel group
starts, tool calls, tool results, applied steering, completion, and errors.
This keeps rendering outside the loop and also lets headless mode collect
metrics without depending on terminal code.

Successful tool output is concise by default. Verbose mode restores full result
cards. Errors and direct shell command output remain visible.

---

## 8. Subagent Architecture

Interactive chat can expose the `agent` tool implemented by `internal/factory`.
The parent chat agent remains the coordinator. Each child is a fresh core
`agent.Agent` with a self-contained prompt, no parent transcript, and no nested
`agent` tool.

```text
Parent agent
    │ tool call: agent(prompt, mode)
    ▼
Supervisor
    ├── enforce session count and per-run time budgets
    ├── allocate node ID and attach parent call metadata
    └── AgentRunner
            ├── fresh transcript
            ├── provider/model snapshot
            ├── bounded tool registry
            └── core agent loop
```

The default supervisor admits at most 20 children per chat session and gives
each child 15 minutes. Events are attributed to a node and sent to the TUI over
a bounded, non-blocking channel. The supervisor reports execution status but
does not judge whether a child's answer is correct.

Two modes define immutable capabilities for a run:

| Mode | Tools | Permission | Scheduling |
| --- | --- | --- | --- |
| `work` | bash, read, write, edit, grep, glob | inherited session intent; autonomous after admission | serial |
| `inspect` | read, grep, glob | forced read-only | parallel-safe |

If no separate subagent backend is configured, future children follow the
coordinator's active provider and model. Existing children keep the backend
snapshot they started with.

---

## 9. Local Persistence

Neo stores user state below `~/.neo/`. There is no database.

| Path | Owner | Purpose |
| --- | --- | --- |
| `~/.neo/config.yaml` | `internal/config` | optional user configuration |
| `~/.neo/auth.json` | `internal/auth` | refreshable OpenAI subscription credentials |
| `~/.neo/sessions/index.json` | `internal/session` | session metadata index |
| `~/.neo/sessions/<id>.json` | `internal/session` | full transcript, backend metadata, cwd, and usage |
| `~/.neo/AGENTS.md` | `internal/projectctx` | optional global project instructions |
| `~/.neo/skills/<name>/SKILL.md` | `internal/skills` | user-global reusable skills |

Session and auth writes use `internal/atomicfile`: write a sibling temporary
file, set permissions, and rename it into place. Credential and transcript files
use mode `0600`. A newly created auth directory uses `0700`.

Session storage uses one JSON file per conversation plus a metadata index.
Index updates are read-modify-write without a cross-process lock. This assumes
one interactive Neo process per storage directory. Concurrent processes can
lose index updates, though their separate session files remain intact.

Session transcripts may contain source code, shell output, prompts, and other
sensitive repository content. They are local files but should still be treated
as sensitive data.

---

## 10. Project Context and Skills

These features augment prompts at the application edge. Neither changes the
core loop.

### AGENTS.md

`internal/projectctx` discovers instructions in increasing priority:

```text
~/.neo/AGENTS.md
repository-root/AGENTS.md
...
current-directory/AGENTS.md
```

The upward walk stops at the Git repository root or filesystem root.
Instructions are appended as a dynamic, non-cacheable system prompt block.
Discovery failures warn and allow chat startup to continue.

### Skills

`internal/skills` discovers:

```text
~/.neo/skills/<name>/SKILL.md
<workspace>/.neo/skills/<name>/SKILL.md
```

Project skills override global skills of the same name. Only each skill's name
and description are advertised in the stable system prompt. The full body is
expanded into a user turn when the user references `$name` or invokes
`/name`. This avoids placing every skill body in every provider request.

---

## 11. Package Reference

| Path | Responsibility | Explicit boundary |
| --- | --- | --- |
| `cmd/neo/` | CLI dispatch and application composition | The only layer that wires the full product together. |
| `internal/agent/` | Transcript, provider/tool loop, parallel scheduling, steering, usage, events | No config, session, TUI, or provider-specific policy. |
| `internal/atomicfile/` | Atomic single-file replacement helper | No domain-specific serialization. |
| `internal/auth/` | Device-code login, token refresh, credential storage | Used only for OpenAI subscription auth. |
| `internal/compact/` | Compactor interface, transcript summarization, safe splitting | No session persistence or UI. |
| `internal/config/` | Config discovery, defaults, validation, feature flags | No provider construction. |
| `internal/factory/` | Child runner, supervisor budgets, attribution, `agent` tool | Does not interpret child results or permit nested children. |
| `internal/llm/` | Shared request, response, message, content, usage, and provider types | No network transport in the root package. |
| `internal/llm/chatcompletions/` | Reusable OpenAI-compatible wire conversion and HTTP client | Used by provider setup packages such as OpenRouter. |
| `internal/llm/<provider>/` | Vendor wire conversion, HTTP transport, retry integration | Does not execute tools or own transcripts. |
| `internal/llm/retry/` | Shared transient failure classification, backoff, and `Retry-After` handling | Does not know provider wire formats. |
| `internal/logx/` | Optional structured debug logging with payload controls | Disabled unless configured by environment. |
| `internal/permission/` | Tool-call policy, path checks, approval rules, session allowlist | Does not execute tools or render prompts. |
| `internal/projectctx/` | AGENTS.md discovery and prompt augmentation | Layered feature, not core behavior. |
| `internal/session/` | Local session metadata, transcripts, usage, list and search | No provider calls. |
| `internal/skills/` | Skill discovery, catalog, reference and slash expansion | Layered feature, not core behavior. |
| `internal/tools/` | Tool interfaces, registry, shell, file and search tools | Does not choose permission mode. |
| `internal/tui/` | Bubble Tea state, input, rendering, approvals, steering | Consumes agent events; does not implement the loop. |
| `internal/workflow/` | Visible checklist model and tool | UI state only, not a durable workflow engine. |
| `internal/workspace/` | Repository root, ancestor, and safe path helpers | Shared filesystem boundary logic. |
| `website/` | Astro documentation and marketing site | Not linked into the Neo binary or runtime. |

---

## 12. Security Model

Neo's main trust boundaries are the model provider, local tool execution, the
workspace boundary, and local persisted secrets.

### Provider Boundary

Conversation messages, system instructions, attached images, and tool results
are sent to the configured provider. Users should assume that any content placed
in the active transcript can cross that network boundary.

API keys are read from environment variables. OpenAI subscription access and
refresh tokens are stored in `~/.neo/auth.json`. Debug logging is optional and
payload logging is separately controlled to reduce accidental disclosure.

### Tool Boundary

Tool definitions describe capabilities, but the permission policy decides each
call. File tools are workspace-bounded. Read-only subagents receive a filtered
registry as well as a read-only policy, providing two independent restrictions.

Shell execution remains the broadest capability. Timeouts and output limits
control resource use and transcript growth, but they are not a security sandbox.

### Persistence Boundary

Credential and session files are written with restrictive file modes and atomic
replacement. Atomicity protects against partial writes, not concurrent logical
updates. There is no encryption at rest beyond protections provided by the
host filesystem.

### Resource Bounds

| Resource | Bound |
| --- | --- |
| Agent loop | 500 provider turns |
| Concurrent parallel tools | 8 by default |
| Tool result in transcript | 256 KiB |
| Direct file read | about 256 KiB per selection |
| Primary shell command | 2 minutes with bounded output |
| Headless run | 10 minutes by default |
| Child agents | 20 per interactive session |
| Child runtime | 15 minutes per child |
| Child shell command | 5 minutes with bounded output |
| Search results | 200 by default |

---

## 13. Build, Test, and Release Shape

The runtime is one Go module and one primary binary:

```text
go test ./...              all Go tests
go vet ./...               standard static checks
go build ./cmd/neo         local build
just build                 version-stamped ./neo binary
just performance           release-shaped performance checks
```

Tests live beside their packages. Provider adapters use fake HTTP servers or
fake providers; agent tests exercise transcript and scheduling invariants;
permission and workspace tests cover path and command rules; TUI tests drive
Bubble Tea updates without requiring a live provider.

The `website/` directory is a separate Astro project. Its build copies or
prepares documentation for the public site, but the website is not required to
build or run Neo.

---

## 14. Known Limitations

These are current architectural limits, not planned behavior:

| # | Limitation | Detail |
| --- | --- | --- |
| 1 | No OS sandbox | Trusted shell commands run with the Neo process's host permissions. |
| 2 | Single-process local stores | Auth and session index updates have no cross-process lock, so concurrent writers can lose logical updates. |
| 3 | Approximate compaction threshold | Context use is estimated from characters rather than provider tokenization. |
| 4 | Provider feature differences | Prompt caching, opaque reasoning replay, model discovery, image handling, and usage fields vary by adapter. |
| 5 | In-memory workflow state | The visible checklist is UI state and is not a durable workflow execution engine. |
| 6 | In-memory subagent supervision | Child state and events exist only for the current process and are not resumable after exit. |
| 7 | Best-effort attachments | Unreadable image attachments are skipped with an inline note instead of failing the turn. |

---

## 15. Extension Points

### Add a Provider

1. Implement `llm.Provider`.
2. Translate shared messages, tools, stop reasons, and usage at the adapter edge.
3. Add provider construction and credential checks in `cmd/neo`.
4. Add configuration defaults and validation.
5. Test wire conversion, errors, retries, tool calls, and transcript replay.
6. Update the provider and configuration developer docs.

Provider-specific data should stay in the adapter or in opaque `Raw` blocks.

### Add a Tool

1. Implement `tools.Tool`.
2. Implement `ParallelTool` only when concurrent execution is safe for the
   concrete input.
3. Implement `ReadOnlyTool` only when the concrete input cannot mutate state.
4. Register the tool at the appropriate application surface.
5. Extend path policy if the tool accepts path-shaped arguments.
6. Test success, malformed input, cancellation, output bounds, and permission
   behavior.
7. Update `docs/developer/tools.md`.

Unclassified tools deliberately run serially and fail closed under read-only
policy.

### Add a Product Capability

Capabilities such as project instructions, skills, workflows, and subagents
should be composed in `cmd/neo`, communicate through interfaces or events, and
leave `internal/agent` unchanged unless the provider/tool loop itself requires a
new invariant. This keeps the core loop small, reusable, and testable.

For shorter change-oriented references, continue with
[`docs/developer/index.md`](docs/developer/index.md).
