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
| `trusted` | Built-in tools run automatically, except high-risk bash commands ask first. Workspace path checks still apply. |
| `ask` | Read/search tools run automatically. Bash and file mutations ask first. |
| `readonly` | Read/search tools run. Bash and file mutations are denied. |

By default, Neo uses `trusted` to avoid prompting for routine coding work while still pausing before commands such as `rm -rf`, `sudo`, recursive ownership/permission changes, `git clean -fd`, and `git reset --hard`.

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

For path-shaped tools, Neo checks that paths stay inside the workspace root. This still matters in `trusted` mode.

The point of `trusted` is to stop asking for every mutation, not to let the agent write anywhere on the machine.

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
