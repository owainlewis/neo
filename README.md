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
- **Small tool surface.** Read, search, shell, and edit tools are inspectable
  and permissioned.
- **Permission modes.** Choose `ask`, `trusted`, or `readonly` depending on how
  much approval you want before Neo runs tools.
- **AGENTS.md support.** Drop an `AGENTS.md` in your project (or `~/.neo/`) and
  its guidance is loaded into the agent's system prompt. Feature-flagged.
- **Skills.** Reusable prompt snippets in `.neo/skills/<name>/SKILL.md`. Mention
  `$name` in a message and the skill's instructions are expanded into that turn.
- **Modular core.** The agent loop knows nothing about coding, files, or project
  context — capabilities are injected and can be toggled in config.

## Quick Start

**Prerequisites:** Choose one model backend:

- Anthropic: create an [Anthropic API key](https://console.anthropic.com/) and
  set `ANTHROPIC_API_KEY`.
- OpenAI API key: create an [OpenAI API key](https://platform.openai.com/api-keys),
  set `provider: openai`, and set `OPENAI_API_KEY`.
- OpenAI subscription: set `provider: openai` and
  `openai_auth: subscription`, then run `neo login` and enter the printed
  device code in your browser.

### One-line install (recommended)

```bash
curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
```

The script auto-detects your OS and architecture, downloads the matching
pre-built release archive from GitHub Releases, verifies its checksum when
available, and installs it to `~/.local/bin`.
If no pre-built binary is available for your platform it falls back to
`go install` (requires Go 1.25+).

Options:

```bash
# Pin a specific version
curl -fsSL .../install.sh | bash -s -- --version v1.2.3

# Install to a custom directory
curl -fsSL .../install.sh | bash -s -- --bin-dir /usr/local/bin
```

### Homebrew

```bash
brew install --cask owainlewis/tap/neo
```

### Manual install

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
just build                          # or: go build -o neo ./cmd/neo
export ANTHROPIC_API_KEY="sk-ant-..."
./neo                               # opens the chat TUI (default)
```

For OpenAI API-key auth, create `neo.yaml` with `provider: openai` and set
`OPENAI_API_KEY`. For OpenAI subscription auth, create `neo.yaml` with
`provider: openai` and `openai_auth: subscription`, then run `./neo login`
before starting chat.

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

# Saved sessions
neo sessions
neo resume <session-id>

# OpenAI subscription auth
neo login
neo logout
```

### Commands

| Command | Description |
|---------|-------------|
| `neo` / `neo chat` | Open the interactive terminal coding agent |
| `neo sessions` | List saved chat sessions |
| `neo resume <id>` | Resume a saved chat session |
| `neo login` | Log in to an OpenAI ChatGPT/Codex subscription |
| `neo logout` | Remove stored OpenAI subscription credentials |
| `neo help` | Show CLI help |

## Sessions

Neo saves chat sessions under `~/.neo/sessions/` so conversations can be
resumed later. Session files contain the agent transcript, basic metadata such
as cwd and model, and tool call/result messages needed to continue the model
conversation.

```bash
neo sessions        # list recent sessions
neo resume <id>     # reopen a saved session
```

Inside the TUI, use `/sessions` to open the session browser. The in-TUI browser
can resume sessions for the current working directory; use `neo resume <id>`
from the shell when you want Neo to restore a different saved cwd before tools
are created.

## TUI Shortcuts

Slash commands keep common actions out of the chat transcript:

| Command | Description |
|---------|-------------|
| `/help` | Show slash commands and key bindings |
| `/tools` | List available tools |
| `/permissions` | Change the current permission mode |
| `/tokens` | Show token usage for the session |
| `/model` | Pick the active model for this session |
| `/sessions` | Browse saved sessions |
| `/memory <text>` | Append a project memory entry |
| `/clear` | Clear the current transcript |

Small examples:

```text
/model              # open the model picker
/permissions        # switch between ask, trusted, readonly
/sessions           # resume a saved session for this workspace
/memory prefer table-driven tests   # append a project memory entry
!git status         # run a shell command through Neo's bash tool
read @README        # type @ to search workspace files, then tab/enter to insert
```

The `!` alias is a convenience for one-off shell commands. It follows the same
permission policy as the `bash` tool, so `ask` mode prompts, `trusted` runs it,
and `readonly` denies it.

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

Neo defaults to Anthropic. Use `provider: openai` to switch to OpenAI.

Anthropic setup:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

OpenAI API-key setup:

```yaml
provider: openai
openai_auth: api_key
```

```bash
export OPENAI_API_KEY="sk-..."
```

OpenAI subscription setup:

```yaml
provider: openai
openai_auth: subscription
```

```bash
neo login
```

Use `neo logout` to remove stored subscription credentials.

**`neo.yaml` reference:**

```yaml
# LLM backend: "anthropic" (default) or "openai".
# anthropic -> requires ANTHROPIC_API_KEY
# openai    -> uses the Responses API; auth via openai_auth.
provider: anthropic

# How the "openai" provider authenticates:
#   api_key      -> uses OPENAI_API_KEY (default)
#   subscription -> uses a ChatGPT/Codex subscription via device-code auth; run `neo login`
openai_auth: api_key

# Model used by the agent. Defaults by provider/auth mode:
#   anthropic                -> claude-opus-4-8
#   openai + api_key         -> gpt-4o
#   openai + subscription    -> gpt-5-codex
model: claude-opus-4-8

# Tool permission mode:
#   ask      -> allow read/search, ask before bash and file mutations
#   trusted  -> allow built-in tools without prompts; path-shaped tools stay inside repo
#   readonly -> allow read/search only
permissions:
  mode: ask

# Optional, layered capabilities. Each defaults to on when omitted; set a flag
# to false to disable it. The core agent loop is never affected by these.
features:
  agents_file: true   # load AGENTS.md into the system prompt
  skills: true        # discover .neo/skills, advertise them, expand $name
  prompt_caching: true # cache the static system prompt prefix
```

### Permissions

Neo defaults to `permissions.mode: ask`.

| Mode | Behavior |
|------|----------|
| `ask` | Read/search tools run automatically; bash and file mutations ask first |
| `trusted` | Built-in tools run without approval prompts |
| `readonly` | Read/search tools run; bash and file mutations are denied |

Path-shaped tools (`read_file`, `write_file`, `edit_file`, `grep`, and `glob`)
are workspace-bound: Neo denies paths outside the workspace root even in
`trusted` mode.

Approved or trusted `bash` is different. It runs `/bin/bash -c` in the working
directory with a timeout, but it is not a true filesystem sandbox. A shell
command can still affect files outside the repo if the command does so, so keep
`ask` mode on when you want to review shell commands first.

## Tools

The agent has these built-in tools:

| Tool | Description |
|------|-------------|
| `read_file` | Read a file from disk, with offset/limit support for large files |
| `grep` | Search text files under the workspace with a regular expression |
| `glob` | Find files under the workspace with glob patterns such as `**/*.go` |
| `bash` | Run a shell command through `/bin/bash -c` with a 2-minute timeout |
| `write_file` | Create or overwrite a file |
| `edit_file` | Replace one exact string match in a file |

## Project Layout

```text
cmd/neo/                CLI entry point and command dispatch
internal/agent/         Core agent loop and event model
internal/auth/          OpenAI subscription device-code auth and credential storage
internal/config/        Config loading and feature flags
internal/config/defaults/   Embedded neo.yaml
internal/llm/           Provider interface + Anthropic and OpenAI adapters
internal/projectctx/    AGENTS.md discovery and system-prompt injection
internal/session/       Saved session metadata and transcripts
internal/skills/        skill discovery, catalog, and $name expansion
internal/tools/         bash, read_file, write_file, edit_file, grep, glob
internal/tui/           Bubble Tea terminal UI
```

## Developer Docs

Developer docs live in `docs/developer/` and are generated from repository code and defaults:

```bash
go run ./cmd/neo-docs
go run ./cmd/neo-docs --check
```

Neo is pointed at these docs through `AGENTS.md`, so local agent sessions can read the same developer reference humans use.
For background on the safety and observability milestone behind the current
tooling, see [docs/robust-core-plan.md](docs/robust-core-plan.md).

## Development

[`just`](https://github.com/casey/just) is used as a task runner. All targets
also work as plain `go` commands.

```bash
just build        # go build -o neo ./cmd/neo
just test         # go test ./...
just test-verbose # go test -v ./...
just install      # go install ./cmd/neo
just fmt          # gofmt -w .
just lint         # go vet ./... && golangci-lint run
just clean        # remove the ./neo binary
```

Install [`golangci-lint`](https://golangci-lint.run/) to run `just lint`
locally. CI runs the pinned linter version from `.github/workflows/ci.yml`.

## Releasing

Releases are built by GitHub Actions when a `v*` tag is pushed:

```bash
git tag v1.2.3
git push origin v1.2.3
```

The release workflow runs tests, builds Linux and macOS binaries for `amd64`
and `arm64`, publishes GitHub release notes and checksums, and updates the
Homebrew cask in `owainlewis/homebrew-tap`.

The Homebrew tap update requires a repository secret named
`HOMEBREW_TAP_GITHUB_TOKEN` with write access to `owainlewis/homebrew-tap`.

## License

[MIT](LICENSE) © Neo Contributors
