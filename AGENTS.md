# Neo Repository Guide

Neo is a small Go coding agent. Keep changes focused, preserve the core
invariants below, and prove behavior with the narrowest useful checks.

## Start Here

- Read `docs/developer/index.md` before changing code. Follow the linked
  developer docs relevant to the task.
- Treat the current implementation and tests as the source of truth when a
  design document describes planned behavior.
- Update `docs/developer/` when behavior, configuration, commands, or module
  responsibilities change.

## Repository Map

| Path | Responsibility |
| --- | --- |
| `cmd/neo/` | CLI commands, chat startup, provider and tool wiring. |
| `internal/agent/` | Policy-free model and tool loop, transcript state, events. |
| `internal/llm/` | Provider-neutral types and provider adapters. |
| `internal/tools/` | Built-in tool implementations and registry. |
| `internal/permission/` | Tool approval policy and workspace boundaries. |
| `internal/tui/` | Bubble Tea UI, input, and transcript rendering. |
| `internal/factory/` | Subagent supervision, budgets, and event aggregation. |
| `internal/session/`, `internal/compact/` | Persistence and context compaction. |
| `internal/projectctx/`, `internal/skills/` | AGENTS.md and skill discovery. |
| `docs/developer/` | Current technical reference. |
| `website/` | Astro documentation site. |

## Architecture Rules

- Keep `internal/agent` policy-free. Add product behavior around the loop
  unless it is provider or tool-turn mechanics.
- Preserve transcript validity: every assistant `tool_use` must have a matching
  user `tool_result` before the next provider request or persisted transcript.
- Keep provider-specific request, response, retry, and auth translation inside
  its `internal/llm/<provider>` adapter. Use provider-neutral types elsewhere.
- Keep tools small and inspectable. Register new tools on each intended runtime
  surface, including `cmd/neo` and `internal/factory` when subagents need them.
  Test success and failure paths, and update `docs/developer/tools.md`.
- Do not bypass `internal/permission`. Path-shaped file tools must stay inside
  the workspace root. Read-only mode must propagate to subagents. In ask mode,
  work subagents run autonomously only after parent approval; inspect subagents
  remain read-only.
- Preserve configuration semantics: the first config file found wins, files
  are not merged, and absent feature flags use built-in defaults.
- Keep always-loaded prompt text short. Put detailed or optional workflows in
  developer docs or skills instead of the base prompt or AGENTS.md.

## Checks

Run focused package tests while iterating. Before a pull request, match the CI
checks for the changed area.

For Go changes:

```bash
gofmt -w .
go build ./...
go test -race ./...
go vet ./...
golangci-lint run
```

For `website/` or developer-doc changes that affect the published site:

```bash
cd website
bun install --frozen-lockfile
bun run build
```

For documentation-only changes, verify links, file paths, and command examples
against the repository. State why code checks were not run.
