# Neo

[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Neo is a fast, minimalist coding agent with first-class workflow support.

It is being rewritten in Go around a small core loop, a practical terminal UI,
and explicit multi-phase flows for building, reviewing, evaluating, and landing
code changes. The goal is simple: keep the agent easy to understand, easy to
run, and strong at the real shape of software work.

## What Neo Does

- **Interactive coding.** Use `neo chat` for a focused terminal coding agent
  that can inspect files, run commands, and make edits.
- **Workflow automation.** Use `neo flow` to run repeatable phase-based
  workflows such as build -> review -> eval -> finalize.
- **Single-phase runs.** Use `neo phase` when you want one role prompt to act on
  a task without running a full workflow.
- **Plain files as workflow.** Phases live in `phases/*.md`; flows live in
  `flows/*.yaml`. You can edit the process without changing code.
- **Small tool surface.** Neo starts with bash, read, write, and exact-match edit
  tools. The shape is intentionally boring and inspectable.

## Status

Neo is in an active Go rewrite. The old Rust implementation has been moved to
`legacy/` while the new Go implementation lives under `cmd/neo` and `internal/`.

## Quick Start

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
go build ./cmd/neo
export ANTHROPIC_API_KEY="your_key"
./neo chat
```

Or install it onto your `GOBIN` path:

```bash
go install ./cmd/neo
neo chat
```

## Usage

```bash
neo chat
neo flow implementation "Add request cancellation"
neo flow full "Refactor the config loader"
neo phase review "Check the current diff for blocking issues"
neo help
```

### Commands

| Command | Description |
|---------|-------------|
| `neo chat` | Start the interactive terminal coding agent |
| `neo flow <name> "<task>"` | Run a named workflow from `flows/<name>.yaml` |
| `neo phase <name> "<task>"` | Run a single phase prompt from `phases/<name>.md` |
| `neo help` | Show CLI help |

## Workflows

A flow is a YAML file that names the phases to run and where to retry from if a
later phase fails.

```yaml
name: implementation
retry_from: build
max_rounds: 3
phases:
  - build
  - eval
  - finalize
```

The default flows are:

| Flow | Phases |
|------|--------|
| `implementation` | `build`, `eval`, `finalize` |
| `full` | `build`, `review`, `eval`, `finalize` |

Phase prompts are Markdown files. A phase gets the original task plus artifacts
from earlier phases, then writes its result into `.agent/runs/`.

## Configuration

Neo is configured with environment variables.

```bash
export ANTHROPIC_API_KEY="your_key"
export NEO_MODEL="claude-sonnet-4-6"
export NEO_PHASES_DIR="./phases"
export NEO_FLOWS_DIR="./flows"
```

| Variable | Description |
|----------|-------------|
| `ANTHROPIC_API_KEY` | Required API key for the Anthropic provider |
| `NEO_MODEL` | Model name. Defaults to `claude-sonnet-4-6` |
| `NEO_PHASES_DIR` | Directory for phase prompts. Defaults to `./phases` or `~/.neo/phases` |
| `NEO_FLOWS_DIR` | Directory for flow definitions. Defaults to `./flows` or `~/.neo/flows` |

Artifacts are written to `.agent/runs` by default.

## Tools

| Tool | Description |
|------|-------------|
| `bash` | Run shell commands with a timeout |
| `read_file` | Read a file from disk |
| `write_file` | Create or overwrite a file |
| `edit_file` | Replace one exact string match in a file |

## Project Layout

```text
cmd/neo/                 CLI entry point
internal/agent/          Agent loop and event model
internal/flow/           Multi-phase workflow runner
internal/phase/          Single phase runner
internal/tools/          Built-in tool implementations
internal/tui/            Bubble Tea terminal UI
flows/                   Workflow definitions
phases/                  Phase prompts
legacy/                  Previous Rust implementation
```

## License

MIT
