# Neo

[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Neo is a fast, minimalist coding agent with first-class workflow support, written in Go.

An interactive terminal UI lets you chat with the agent directly. A headless
CLI lets you run repeatable multi-step flows in CI or from scripts — each step
is a plain Markdown prompt file that you can read and edit without touching
any code.

![neo splash screen](docs/screenshot.png)

## Features

- **Interactive chat.** `neo chat` opens a Bubble Tea terminal UI. Type a task,
  watch the agent read files, run commands, and make edits in real time.
- **Multi-step flows.** `neo flow <name> "<task>"` runs a named sequence of
  agent steps. Each step receives the outputs of all prior steps as context.
- **Single-step runs.** `neo step <name> "<task>"` runs one step prompt
  against a task — useful for iterating on a prompt without a full flow.
- **Plain-text prompts.** Step prompts are Markdown files (`flows/*.md`).
  Customize them by adding a `flows/` directory to your project or `~/.neo/flows/`.
- **Per-step overrides.** A step's Markdown file can carry optional YAML
  frontmatter to restrict its tool set or pin a specific model.
- **Small tool surface.** bash, read\_file, write\_file, edit\_file — inspectable
  and easy to reason about.

## Quick Start

**Prerequisites:** An [Anthropic API key](https://console.anthropic.com/).

### One-line install (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
```

The script auto-detects your OS and architecture, downloads the matching
pre-built binary from GitHub Releases, and installs it to `~/.local/bin`.
If no pre-built binary is available for your platform it falls back to
`go install` (requires Go 1.25+).

Options:

```bash
# Pin a specific version
curl -fsSL .../install.sh | bash -s -- --version v1.2.3

# Install to a custom directory
curl -fsSL .../install.sh | bash -s -- --bin-dir /usr/local/bin
```

### Manual install

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
just build                          # or: go build -o neo ./cmd/neo
export ANTHROPIC_API_KEY="sk-ant-..."
./neo                               # opens the chat TUI (default)
```

`just build` stamps the current git description into the binary as the
version shown on the splash screen (use `just print-version` to preview).

Install onto your `$GOBIN` path:

```bash
go install github.com/owainlewis/neo/cmd/neo@latest
neo chat
```

## Usage

```bash
# Interactive terminal chat
neo chat

# Run a flow file against a task
neo run build-flow.yml "Please fix GitHub issue 21"

# Run the built-in "code" flow (write → review) against a task
neo flow code "Add request-cancellation support to the HTTP client"

# Run a single step headlessly
neo step write "Refactor the config loader"
neo step review "Check the current diff for blocking issues"

neo help
```

### Commands

| Command | Description |
|---------|-------------|
| `neo chat` | Open the interactive terminal coding agent |
| `neo run <flow.yml\|name> "<task>"` | Run a flow file or named flow |
| `neo flow <flow.yml\|name> "<task>"` | Alias for `neo run` |
| `neo step <name> "<task>"` | Run a single step prompt from `flows/<name>.md` |
| `neo help` | Show CLI help |

## Flow Files

The chat UI starts in normal chat mode. From chat, run a repeatable workflow
with:

```text
/flow build-flow.yml Please fix GitHub issue 21
```

Neo switches into workflow mode, renders a checklist while the flow runs, then
returns to chat when the flow completes or fails.

A flow file is a small YAML pipeline of agent and command steps:

```yaml
steps:
  - name: plan
    type: agent
    prompt: flows/PLAN.md

  - name: check-plan
    type: command
    run: test -s "docs/{{ .RunID }}/PLAN.md"

  - name: build
    type: agent
    prompt: flows/BUILD.md

  - name: test
    type: command
    run: go test ./...

  - name: finalizer
    type: agent
    prompt: flows/FINALIZER.md
```

Agent prompt paths are resolved relative to the flow file. Prompts are
Markdown files rendered with a small template context:

```markdown
Task:

{{ .Task }}

Run ID:

{{ .RunID }}

Create a plan and save it to:

docs/{{ .RunID }}/PLAN.md
```

Command steps run through the shell and pass when they exit with code `0`.
Agent steps pass when the agent completes without a runtime error. There is no
semantic pass/fail or looping in flow-file v1; use command steps as hard gates.
In chat, a completed flow shows the final step output under the progress
widget, so a final summary agent can leave a useful result behind.

Try the local smoke flow without needing a GitHub issue:

```bash
neo run examples/neo-smoke-flow.yml "Check the simple flow runner"
```

Or from chat:

```text
/flow examples/neo-smoke-flow.yml Check the simple flow runner
```

## Config Flows

A flow is a named sequence of steps defined in `neo.yaml`. The engine runs
each step in order, passing prior step outputs as context. If a step reports
failure and `retry_from` is set, the engine rewinds to that step and tries
again (up to `max_rounds` times).

### Built-in flow: `code`

```yaml
flows:
  code:
    steps: [write, review]
    retry_from: write
    max_rounds: 2
```

| Flow | Steps | Behaviour |
|------|-------|-----------|
| `code` | `write` → `review` | Writes the change, then reviews it. Retries `write` if review fails. |

### Defining your own flows

Add a `neo.yaml` to your project (or `~/.neo/config.yaml` globally):

```yaml
model: claude-sonnet-4-6

flows:
  ship:
    steps: [write, test, review]
    retry_from: write
    max_rounds: 3
```

Then add the corresponding step prompts in `flows/write.md`, `flows/test.md`,
and `flows/review.md`. Any step not found locally falls back to the embedded
defaults.

## Step Prompts

A step prompt is a Markdown file. It may include optional YAML frontmatter to
restrict the tool set or override the model for that step:

```markdown
---
tools: [bash, read_file]
model: claude-haiku-4-5
---

You are the REVIEW step of a coding flow.
…
```

**Prompt resolution order** (first match wins):

1. `./flows/<name>.md` — project-local override
2. `~/.neo/flows/<name>.md` — user-global override
3. Embedded defaults (shipped with the binary)

## Configuration

Neo looks for a config file in this order:

1. `./neo.yaml` — project config
2. `~/.neo/config.yaml` — user config
3. Embedded defaults — no file required to get started

The only required environment variable is `ANTHROPIC_API_KEY`.

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

**`neo.yaml` reference:**

```yaml
# Model used for all steps unless overridden in frontmatter.
# Default: claude-sonnet-4-6
model: claude-sonnet-4-6

# Directory where step artifacts are written.
# Default: .agent/runs
artifacts_dir: .agent/runs

flows:
  code:
    steps: [write, review]
    retry_from: write   # rewind to this step on failure
    max_rounds: 2       # maximum retry rounds (0 or omitted = 1)
```

## Tools

The agent has four built-in tools:

| Tool | Description |
|------|-------------|
| `bash` | Run a shell command (2-minute timeout) |
| `read_file` | Read a file from disk |
| `write_file` | Create or overwrite a file |
| `edit_file` | Replace one exact string match in a file |

## Project Layout

```text
cmd/neo/                CLI entry point and command dispatch
internal/agent/         Core agent loop and event model
internal/config/        Config loading, flow definitions, step resolution
internal/config/defaults/   Embedded neo.yaml and built-in step prompts
internal/artifact/      Per-run artifact storage (.agent/runs/)
internal/flow/          (reserved)
internal/phase/         Single-step runner
internal/tools/         bash, read_file, write_file, edit_file implementations
internal/tui/           Bubble Tea terminal UI
internal/workflow/      Multi-step workflow engine
```

## Development

[`just`](https://github.com/casey/just) is used as a task runner. All targets
also work as plain `go` commands.

```bash
just build        # go build -o neo ./cmd/neo
just test         # go test ./...
just test-verbose # go test -v ./...
just install      # go install ./cmd/neo
just fmt          # gofmt -w .
just lint         # go vet ./...
just clean        # remove the ./neo binary
```

## License

[MIT](LICENSE) © Neo Contributors
