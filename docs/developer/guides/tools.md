# Tools

## The Simple Idea

Tools are the agent's hands. The model can think and write text, but tools let it inspect the repo, run commands, and change files.

## The Problem

A model cannot directly read your filesystem or run tests. Without tools, it has to guess. With tools, it can inspect reality before acting.

Tools have side effects. A shell command or file write can change its
environment, so tools must be small and inspectable, while the VM or sandbox
contains their capabilities.

## How Neo Solves It

Neo exposes a small built-in tool surface:

- `read_file`: read files.
- `grep`: search file contents with a regular expression, via ripgrep.
- `glob`: list files by pattern, via ripgrep.
- `bash`: run shell commands.
- `write_file`: overwrite or create files.
- `edit_file`: replace one exact string.

Interactive chat also adds two product tools:

- `workflow`: maintain the visible checklist for multi-step work.
- `agent`: run a bounded work or read-only inspection subagent.

Headless `neo run` receives only the base file, search, and shell tools.

Each tool implements the same interface:

```go
type Tool interface {
    Name() string
    Spec() llm.ToolSpec
    Run(ctx context.Context, input map[string]any) (string, error)
}
```

The model sees tool specs. Neo runs the tool and feeds the result back into the conversation.

## Why The Tool Surface Is Small

Small tools are easier to trust and easier to teach. For example, `edit_file` replaces exactly one occurrence. If the target text is missing or appears more than once, it fails instead of guessing.

That makes failures useful: the model can inspect again and try a safer edit.

## How To Add A Tool

1. Add a type in `internal/tools`.
2. Implement `Name`, `Spec`, and `Run`.
3. Register it in `cmd/neo/main.go`.
4. Add tests for success, bad input, and edge cases.
5. Update `docs/developer/tools.md` and this guide.

## What To Be Careful About

- Treat errors as data. Return useful output when a tool fails.
- Run Neo inside a sandbox with the intended filesystem and network boundary.
- Prefer structured tools over shell commands for common operations.
- Do not make one giant tool that does everything.

## Where To Look

- `internal/tools/tool.go`: tool interface and registry.
- `internal/tools/fs.go`: file tools.
- `internal/tools/search.go`: grep and glob.
- `internal/tools/bash.go`: shell execution.
