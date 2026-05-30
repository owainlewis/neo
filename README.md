# Neo

[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Neo is a fast, minimalist coding agent written in Go.

An interactive terminal UI lets you chat with the agent directly — watch it read
files, run commands, and make edits in real time. The codebase is intentionally
small and modular: a policy-free core agent loop, with capabilities layered on
top as independent, feature-flagged modules.

![neo splash screen](docs/screenshot.png)

## Features

- **Interactive chat.** `neo` opens a Bubble Tea terminal UI. Type a task and
  watch the agent work.
- **Small tool surface.** bash, read\_file, write\_file, edit\_file — inspectable
  and easy to reason about.
- **AGENTS.md support.** Drop an `AGENTS.md` in your project (or `~/.neo/`) and
  its guidance is loaded into the agent's system prompt. Feature-flagged.
- **Skills.** Reusable prompt snippets in `.neo/skills/<name>/SKILL.md`. Mention
  `$name` in a message and the skill's instructions are expanded into that turn.
- **Modular core.** The agent loop knows nothing about coding, files, or project
  context — capabilities are injected and can be toggled in config.

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
neo
```

## Usage

```bash
# Interactive terminal chat (default)
neo

# Same thing, explicit
neo chat

neo help
```

### Commands

| Command | Description |
|---------|-------------|
| `neo` / `neo chat` | Open the interactive terminal coding agent |
| `neo help` | Show CLI help |

## AGENTS.md

Neo loads project instructions from `AGENTS.md` into the chat system prompt.
It discovers, in increasing priority:

1. `~/.neo/AGENTS.md` — user-global guidance
2. `AGENTS.md` from the repository root down to your working directory

Disable it by setting the feature flag to `false` (see Configuration).

## Skills

Skills are reusable prompt snippets you invoke on demand. Each lives at
`.neo/skills/<name>/SKILL.md` (project) or `~/.neo/skills/<name>/SKILL.md`
(global), with simple frontmatter:

```markdown
---
name: review
description: review the current diff for correctness and broken contracts
---

You are reviewing a code change. Work from the actual diff…
```

Neo advertises each skill's **name + description** in the system prompt (so the
model knows they exist), and when you mention **`$name`** in a message it expands
that skill's full body into the turn:

```
use the $review skill on my changes
```

Project skills override global ones of the same name. This repo ships
`$review` and `$commit` under `.neo/skills/` as working examples. Disable the
feature by setting `skills: false` (see Configuration).

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
# Model used by the agent. Default: claude-sonnet-4-6
model: claude-sonnet-4-6

# Optional, layered capabilities. Each defaults to on when omitted; set a flag
# to false to disable it. The core agent loop is never affected by these.
features:
  agents_file: true   # load AGENTS.md into the system prompt
  skills: true        # discover .neo/skills, advertise them, expand $name
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
internal/config/        Config loading and feature flags
internal/config/defaults/   Embedded neo.yaml
internal/llm/           Provider interface + Anthropic client
internal/projectctx/    AGENTS.md discovery and system-prompt injection
internal/skills/        skill discovery, catalog, and $name expansion
internal/tools/         bash, read_file, write_file, edit_file implementations
internal/tui/           Bubble Tea terminal UI
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
