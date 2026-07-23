# Permissions

## The Simple Idea

Permissions decide whether a tool call runs immediately, asks the user, or is denied.

Think of it as the safety latch between "the model wants to do this" and "Neo actually does it."

## The Problem

Coding agents need powerful tools. They may run shell commands or edit files. That is useful, but it also means a bad tool call can make a mess.

Approvals keep the user in control, especially for side effects.

## How Neo Solves It

Neo has three permission modes:

| Mode | What happens |
| --- | --- |
| `trusted` | Built-in tools, including bash, run automatically with no approval prompts. Path-shaped tools (`read_file`, `write_file`, `edit_file`, `grep`, `glob`) still must stay inside the workspace root. |
| `ask` | Read/search tools run automatically. Bash and file mutations ask first, and high-risk bash commands (`rm -rf`, `sudo`, recursive ownership/permission changes, `git clean -fd`, `git reset --hard`, paths outside the workspace) always ask. |
| `readonly` | Read/search tools run. Bash and file mutations are denied. |

By default, Neo uses `trusted` to avoid prompting at all for routine coding work. This includes high-risk bash commands like `rm -rf` and `sudo` — trusted mode does not pause for those. Use `ask` mode if you want that safety net back.

Configure it in `neo.yaml`:

```yaml
permissions:
  mode: trusted
```

For stricter approval prompts:

```yaml
permissions:
  mode: ask
```

## Workspace Boundaries

For path-shaped tools (`read_file`, `write_file`, `edit_file`, `grep`, `glob`), Neo checks that paths stay inside the workspace root. This still matters in `trusted` mode and cannot be disabled by permission mode.

Bash commands are not path-checked this way: in `trusted` mode a bash command can read or write anywhere the OS user can, including outside the workspace.

## Approval Previews

When a write or edit asks for approval, Neo shows a preview. Long previews are truncated in the TUI so the approval question stays visible.

The preview is there to answer: "what am I about to allow?"

## How To Extend It

The policy interface is small:

```go
type Policy interface {
    Decide(ctx context.Context, req Request) Result
}
```

Future policies could add per-tool rules, command allowlists, or stronger sandboxing without changing the core agent loop.

## Where To Look

- `internal/permission/policy.go`: permission modes and path checks.
- `internal/agent/agent.go`: approval hook.
- `internal/tui/blocks.go`: approval rendering.
- `internal/agent/preview.go`: write/edit previews.
