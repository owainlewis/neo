# Sandbox and Tool Approvals

## The Simple Idea

Neo trusts its execution environment for security. A VM or sandbox decides
which files, processes, networks, credentials, and external services an agent
can reach.

Interactive users can add confirmation prompts for selected tools or Bash
command prefixes. These prompts help prevent accidental execution. They are not
a security boundary.

## Why Neo Does Not Classify Risk

Shell commands can be expressed through wrappers, scripts, aliases, variables,
substitutions, or indirect tools. A command classifier can look reassuring
without reliably containing an agent.

Neo therefore does not maintain trusted, ask, or read-only permission modes. It
does not classify destructive commands or infer paths from shell text. The
runtime environment owns containment.

Filesystem isolation alone may not be enough. A sandbox that exposes Git
credentials, cloud credentials, unrestricted network access, or authenticated
external services still allows changes outside its filesystem.

## Optional Interactive Confirmations

Configure a default-empty list in `neo.yaml`:

```yaml
tool_approvals:
  - git
  - rm -rf
  - write_file
```

Each entry has two independent meanings:

- An exact tool-name match confirms every call to that tool.
- A Bash-prefix match confirms a command that starts with that literal text.

The next Bash character must be whitespace or the end of the command. `git`
matches `git status`, but not `github`. Matching is case-sensitive.

Neo trims entries when loading configuration, rejects empty entries, and keeps
the first exact duplicate. It does not parse command chains or attempt to find
equivalent commands.

Each match prompts every time. Press `y` to run the call or `n`/`esc` to reject
it. Direct `!` commands use the same matcher.

## Scope

Confirmations apply only to the interactive coordinator, where a human can
answer the prompt.

They do not apply to:

- `neo run`
- work subagents
- inspect subagents

Add `agent` to `tool_approvals` if you want confirmation before interactive
delegation.

Inspect subagents are limited through capability selection rather than
permissions. Their tool registry contains only `read_file`, `grep`, and `glob`.

## Migration

The old `permissions:` configuration and `--permission` CLI option have been
removed. Neo rejects them with migration guidance instead of silently changing
their meaning.

Remove the old mode and add `tool_approvals` only for calls you want to confirm.
Run Neo inside a suitable VM or sandbox.

## Where To Look

- `internal/approval/matcher.go`: literal matching.
- `internal/agent/agent.go`: serial confirmation barrier.
- `internal/tui/approvals.go`: interactive yes/no prompt.
- `internal/factory/supervisor.go`: inspect tool selection.
