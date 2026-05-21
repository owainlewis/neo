# Neo Documentation

Neo is a fast, minimalist coding agent with first-class workflow support. It is
built in Go around a small agent loop, a practical terminal UI, and plain-file
workflows for building, reviewing, evaluating, and landing code changes.

These docs are intended for Neo itself as much as for humans. When a task asks
about Neo internals, tools, workflows, prompts, configuration, or modifying Neo,
read this page before changing code.

## Quick Start

Build Neo from a checkout:

```bash
go build ./cmd/neo
```

Set an Anthropic API key and start an interactive session:

```bash
export ANTHROPIC_API_KEY="your_key"
./neo chat
```

Or run a workflow:

```bash
./neo flow implementation "Add request cancellation"
```

## Start Here

- `README.md` - user-facing overview, commands, configuration, tools, and
  project layout.
- `ROADMAP.md` - current direction and planned work.
- `cmd/neo/main.go` - CLI wiring, default tools, provider setup, and chat prompt.
- `internal/agent` - core agent loop and event model.
- `internal/tools` - built-in tool implementations and registry.
- `internal/phase` - single phase runner.
- `internal/workflow` - flow engine, phase ordering, retry behavior, and events.
- `internal/tui` - interactive terminal UI.
- `phases/*.md` - role prompts used by workflow phases.
- `flows/*.yaml` - workflow definitions.

## How Neo Works

The core agent loop sends a system prompt, conversation transcript, and tool
specs to a provider. It emits assistant text, runs requested tools, appends tool
results to the transcript, and continues until the provider ends the turn.

Workflows run ordered phases. Each phase is a Markdown prompt used as the system
prompt for a focused agent session. Later phases receive artifacts produced by
earlier phases. Flow YAML controls phase order, retry point, and max rounds.

## Built-In Tools

Neo currently registers:

- `bash` - run shell commands with a timeout.
- `read_file` - read a file from disk.
- `write_file` - create or overwrite a file.
- `edit_file` - replace one exact string match in a file.

Keep tools simple, explicit, and well tested.

## Configuration

Neo is configured with environment variables:

- `ANTHROPIC_API_KEY` - required by the Anthropic provider.
- `NEO_MODEL` - model name. Defaults to `claude-sonnet-4-6`.
- `NEO_PHASES_DIR` - phase prompt directory. Defaults to `./phases` or
  `~/.neo/phases`.
- `NEO_FLOWS_DIR` - flow definition directory. Defaults to `./flows` or
  `~/.neo/flows`.
- `NEO_ARTIFACTS_DIR` - artifact output directory. Defaults to `.agent/runs`.

## Development

Run the full test suite before reporting behavioral changes as complete:

```bash
go test ./...
```

Useful focused packages:

```bash
go test ./internal/agent
go test ./internal/tools
go test ./internal/workflow
go test ./internal/tui
```

## Editing Guidance

- Prefer small changes that preserve current package boundaries.
- Read the relevant source package before changing it.
- Keep phase prompts and flow definitions understandable as plain files.
- Keep provider-specific translation in provider packages.
- Avoid network access for core startup behavior.
- For released binaries, bundled docs should be embedded with `go:embed`,
  materialized with `os.UserCacheDir()`, and referenced by absolute path in the
  system prompt.
