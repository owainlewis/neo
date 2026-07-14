# Neo

[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

Most coding agents are free-form: you type a request and hope the model picks
the right sequence of steps. Neo is workflow-first — encode a checklist once,
in a prompt, a skill, or an `AGENTS.md`, and Neo runs the agent through it step
by step with live, visible progress instead of a black box. That checklist is
what makes a multi-step task repeatable instead of a one-off improvisation.

Neo is also a single Go binary with sensible defaults (permission modes and
workflows work out of the box, no plugins to assemble first) and support for
multiple providers (Anthropic, OpenAI API key, OpenAI subscription, OpenRouter,
or Google Gemini) so switching backends is a config line, not a rewrite.

![neo splash screen](docs/screenshot.png)

## Features

- **Workflow-first.** Give Neo numbered steps, or ask it to plan a workflow,
  and the TUI shows a live checklist while the agent works through each step
  in order. Skills let you encode that checklist once and reuse it.
- **Sensible defaults.** Permission modes (`ask`, `trusted`, `readonly`),
  `AGENTS.md` support, and visible workflows all work the moment you install
  Neo — nothing to configure before it's useful.
- **Multi-provider.** Anthropic, OpenAI (API key), OpenAI (ChatGPT/Codex
  subscription), OpenRouter, or Google Gemini. Switch with one config line.
- **Minimalist.** A single Go binary, six built-in tools (read, search, shell,
  write, edit), no runtime dependency. The core agent loop is small and
  policy-free on purpose — file, shell, session, and prompt features are
  layered on as independent modules, not baked into the loop.
- **Interactive chat.** `neo` opens a Bubble Tea terminal UI. Type a task and
  watch the agent work.
- **AGENTS.md support.** Drop an `AGENTS.md` in your project (or `~/.neo/`) and
  its guidance is loaded into the agent's system prompt. Feature-flagged.
- **Skills.** Reusable prompt snippets in `.neo/skills/<name>/SKILL.md`. Mention
  `$name` in a message or run `/name args` and the skill's instructions are
  expanded into that turn.

## Install

Choose the path that fits your setup:

| Method | Best for | Command |
|------|------|------|
| One-line installer | Most users; downloads a release binary when available | `curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh \| bash` |
| Homebrew | macOS users already using Homebrew | `brew install --cask owainlewis/tap/neo` |
| `go install` | Go users who want Neo on their existing `$GOBIN` path | `go install github.com/owainlewis/neo/cmd/neo@latest` |
| Manual build | Contributors or anyone who wants a local checkout | `just build` or `go build -o neo ./cmd/neo` |

### One-line installer

```bash
curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
```

The script auto-detects your OS and architecture, downloads the matching
pre-built release archive from GitHub Releases, verifies its checksum when
available, and installs it into the first writable directory it finds from
`~/.local/bin`, `~/bin`, or `/usr/local/bin`.
If no pre-built binary is available for your platform it falls back to
`go install` (requires Go 1.25+).

Options:

```bash
# Pin a specific version
curl -fsSL .../install.sh | bash -s -- --version v1.2.3

# Install to a custom directory
curl -fsSL .../install.sh | bash -s -- --bin-dir /usr/local/bin
```

If none of those directories exist and are writable, the installer creates and
uses `~/.local/bin`.

### Homebrew

```bash
brew install --cask owainlewis/tap/neo
```

### `go install`

```bash
go install github.com/owainlewis/neo/cmd/neo@latest
neo
```

### Manual build

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
just build                          # or: go build -o neo ./cmd/neo
just install                        # install into a writable bin dir on PATH
```

`just build` stamps the current git description into the binary as the version
shown on the splash screen. Run `just print-version` to preview the stamped
value.

## Quick Start

Follow this once and you should be able to reach your first chat from the
README alone.

### 1. Choose a backend

Neo defaults to Anthropic. Set `provider: openai`, `provider: openrouter`, or
`provider: google` to use a different backend.

| Backend | What you need | Config | Extra step |
|------|------|------|------|
| Anthropic | `ANTHROPIC_API_KEY` | No config required | None |
| OpenAI API key | `OPENAI_API_KEY` | `provider: openai` | None |
| OpenAI subscription | ChatGPT/Codex subscription | `provider: openai` and `openai_auth: subscription` | Run `neo login` once |
| OpenRouter | `OPENROUTER_API_KEY` | `provider: openrouter` | None |
| Google Gemini | `GOOGLE_API_KEY` | `provider: google` | None |

If you are using OpenAI with an API key, you do not need `neo login`.
`neo login` is only for the device-code subscription flow.

### 2. Set credentials

Anthropic:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

OpenAI API key:

```bash
export OPENAI_API_KEY="sk-..."
```

OpenAI subscription:

```bash
neo login
```

`neo login` prints a device-code URL and one-time code, then stores the
subscription credentials in `~/.neo/auth.json`.

OpenRouter:

```bash
export OPENROUTER_API_KEY="sk-or-..."
```

Google Gemini:

```bash
export GOOGLE_API_KEY="..."
```

### 3. Create `neo.yaml` only if you need a non-default backend

Anthropic users can skip this step because `provider: anthropic` is the default.

OpenAI API key:

```yaml
provider: openai
openai_auth: api_key
```

OpenAI subscription:

```yaml
provider: openai
openai_auth: subscription
```

OpenRouter:

```yaml
provider: openrouter
```

Google Gemini:

```yaml
provider: google
```

Neo reads the first config file it finds in this order:

1. `./neo.yaml`
2. `~/.neo/config.yaml`
3. Embedded defaults

### 4. Start your first chat

```bash
neo
```

`neo` and `neo chat` open the same interactive terminal UI. Once it starts, try
a first prompt like:

If you built Neo locally but did not install it onto your `PATH`, run `./neo`
instead.

```text
Summarize this repository and suggest a good first change.
```

### Common commands

| Command | What it does |
|------|------|
| `neo` | Open interactive chat mode |
| `neo chat` | Open interactive chat mode explicitly |
| `neo sessions` | List saved chats |
| `neo doctor` | Check local config, credentials, sessions, git, and workspace |
| `neo sessions search <query>` | Search saved chat transcripts |
| `neo update` | Install the latest stable release |
| `neo update --check` | Check for a stable release without installing |
| `neo update --nightly` | Install the latest nightly release |
| `neo update --nightly --check` | Check for a nightly release without installing |
| `neo resume <id>` | Resume a saved chat |
| `neo login` | Set up OpenAI subscription auth |
| `neo logout` | Remove stored OpenAI subscription credentials |
| `neo help` | Show CLI help |

### Common config flags

| Key | Default | Meaning |
|------|------|------|
| `provider` | `anthropic` | Select `anthropic`, `openai`, `openrouter`, or `google` |
| `openai_auth` | `api_key` when using OpenAI | Choose `api_key` or `subscription` |
| `permissions.mode` | `ask` | Prompt before bash and file mutations |
| `compaction.context_window_tokens` | `200000` | Compact at 70% of this context window estimate |
| `features.agents_file` | `true` | Load `AGENTS.md` instructions |
| `features.skills` | `true` | Enable `.neo/skills` discovery and `$name` or `/name args` expansion |
| `features.prompt_caching` | `true` | Cache the stable system prompt prefix when supported |

## Usage

```bash
neo

neo chat

neo sessions
neo doctor
neo sessions search "old task"
neo update
neo update --check
neo update --nightly --check
neo resume <session-id>

neo login
neo logout

neo help
```

## Sessions

Neo saves chat sessions under `~/.neo/sessions/` so conversations can be
resumed later. Session files contain the agent transcript, basic metadata such
as cwd and model, and tool call/result messages needed to continue the model
conversation.

```bash
neo sessions        # list recent sessions
neo sessions search "bug fix"  # search transcript text
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

Skills also appear in `/help` and the slash picker as `/name`. Built-in
commands keep priority over skills with the same name.

Small examples:

```text
/model              # open the model picker
/permissions        # switch between ask, trusted, readonly
/sessions           # resume a saved session for this workspace
/memory prefer table-driven tests   # append a project memory entry
/review staged diff # apply the review skill with arguments
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
model knows they exist). When you mention **`$name`** in a message, or run
**`/name args`** from the TUI, Neo expands that skill's full body into the turn:

```
use the $review skill on my changes
/review staged diff
```

Project skills override global ones of the same name. This repo ships
`/review`, `/commit`, and `/coordinator-worker` under `.neo/skills/` as working
examples. Built-in slash commands such as `/help` always keep priority over
skills with the same name. Disable the feature by setting `skills: false` (see
Configuration).

For a read-only coordinator-worker smoke test, try:

```text
$coordinator-worker

Run a read-only coordinator-worker workflow for this repository.

Goal: assess whether the current uncommitted changes are safe to commit.

Workflow:
1. Plan the assessment
2. Inspect the current git status and diff
3. Delegate a review of the current diff to a subagent
4. Run the test suite
5. Summarize blockers, risks, and whether this looks safe to commit

Constraints:
- Do not edit files.
- Do not stage or commit anything.
- Do not run formatters.
- Treat this as an assessment only.
```

## Configuration

Neo defaults to Anthropic. Set `provider: openai`, `provider: openrouter`, or
`provider: google` if you want a different backend. Config files are not
merged; the first file found wins.

OpenAI API key:

```yaml
provider: openai
openai_auth: api_key
```

OpenAI subscription:

```yaml
provider: openai
openai_auth: subscription
```

OpenRouter:

```yaml
provider: openrouter
```

Google Gemini:

```yaml
provider: google
```

**`neo.yaml` reference:**

```yaml
# LLM backend: "anthropic" (default), "openai", "openrouter", or "google".
# anthropic  -> requires ANTHROPIC_API_KEY
# openai     -> uses the Responses API; auth via openai_auth.
# openrouter -> uses Chat Completions via OPENROUTER_API_KEY
# google     -> uses Gemini via GOOGLE_API_KEY
provider: anthropic

# How the "openai" provider authenticates (ignored for other providers):
#   api_key      -> uses OPENAI_API_KEY (default)
#   subscription -> uses a ChatGPT/Codex subscription via device-code auth; run `neo login`
openai_auth: api_key

# Model used by the agent. Defaults by provider/auth mode:
#   anthropic                -> claude-opus-4-8
#   openai + api_key         -> gpt-4o
#   openai + subscription    -> gpt-5-codex
#   openrouter               -> see OpenRouter's model catalogue
#   google                   -> gemini-3.5-flash
model: claude-opus-4-8

# Tool permission mode:
#   trusted  -> allow built-in tools; ask before high-risk bash commands; paths stay inside repo
#   ask      -> allow read/search, ask before bash and file mutations
#   readonly -> allow read/search only
permissions:
  mode: trusted

# Long transcripts compact at 70% of this context window estimate.
# Raise this for larger-context models.
compaction:
  context_window_tokens: 200000

# Optional, layered capabilities. Each defaults to on when omitted; set a flag
# to false to disable it. The core agent loop is never affected by these.
features:
  agents_file: true   # load AGENTS.md into the system prompt
  memory: true        # load and update project-root memory.md
  skills: true        # discover .neo/skills, advertise them, expand $name and /name
  prompt_caching: true # cache the static system prompt prefix

# Tool activity rendering during chat:
#   verbose: false (default) -> live activity plus concise completed receipts;
#                                errors always show in full
#   verbose: true            -> full tool call/result cards (file contents,
#                                command output, etc)
output:
  verbose: false
```

### Permissions

Neo defaults to `permissions.mode: trusted`.

| Mode | Behavior |
|------|----------|
| `trusted` | Built-in tools run automatically, except high-risk bash commands ask first |
| `ask` | Read/search tools run automatically; bash and file mutations ask first |
| `readonly` | Read/search tools run; bash and file mutations are denied |

Path-shaped tools (`read_file`, `write_file`, `edit_file`, `grep`, and `glob`)
are workspace-bound: Neo denies paths outside the workspace root even in
`trusted` mode.

Approved or trusted `bash` is different. It runs `/bin/bash -c` in the working
directory with a timeout, but it is not a true filesystem sandbox. A shell
command can still affect files outside the repo if the command does so. Keep
`ask` mode on when you want to review all shell commands first; `trusted` still
asks before high-risk commands such as `rm -rf`, `sudo`, recursive
ownership/permission changes, `git clean -fd`, and `git reset --hard`.

### Output

Neo defaults to concise tool output. While a turn runs, a live row shows elapsed
time, the interrupt key, and real in-flight activity. Completed calls leave
short past-tense receipts (e.g. `Read internal/tui/model.go`) instead of full
file contents or command output. Errors and failed tool calls always render in
full, even in concise mode. Output from direct `!` shell commands also remains
visible because it was explicitly requested. Set `output.verbose: true` in
`neo.yaml` to restore the full tool call/result cards.

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
internal/llm/           Provider interface and provider adapters
internal/projectctx/    AGENTS.md discovery and system-prompt injection
internal/session/       Saved session metadata and transcripts
internal/skills/        skill discovery, catalog, and $name or /name expansion
internal/tools/         bash, read_file, write_file, edit_file, grep, glob
internal/tui/           Bubble Tea terminal UI
```

## Docs Site

A browsable docs site lives in [`website/`](website/), built with Astro + Starlight and deployed
to Cloudflare Pages. It covers install, quick start, and the same generated developer reference
described below, organized into a proper sidebar with search. Run it locally with:

```bash
cd website
bun install
bun run dev
```

## Developer Docs

If you want to use Neo, the README should be enough to get you started.
If you want to contribute, start with [docs/developer/index.md](docs/developer/index.md).
Those docs are generated from repository code and defaults, so regenerate them
instead of editing them by hand:

```bash
go run ./cmd/neo-docs
go run ./cmd/neo-docs --check
```

Neo is pointed at these docs through `AGENTS.md`, so local agent sessions can
read the same developer reference humans use.
For background on the safety and observability milestone behind the current
tooling, see [docs/robust-core-plan.md](docs/robust-core-plan.md).

## Development

[`just`](https://github.com/casey/just) is used as a task runner. All targets
also work as plain `go` commands.

```bash
just build        # go build -o neo ./cmd/neo
just test         # go test ./...
just test-verbose # go test -v ./...
just install      # install neo into a writable bin dir on PATH
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
