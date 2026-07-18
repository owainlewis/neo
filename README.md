# Neo

[![Go](https://img.shields.io/badge/go-1.25%2B-00ADD8.svg)](https://go.dev/)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

**A fast, open-source coding agent that shows its work.**

Neo turns an engineering task into a visible workflow, then works through it
with the plan, tools, subagents, and results in view. It runs as one native Go
binary and works with Anthropic, OpenAI, OpenRouter, and Google Gemini.

![Neo terminal interface](docs/screenshot.png)

## Why Neo

- **Visible workflows.** Give Neo a checklist in your prompt, `AGENTS.md`, or a
  skill. It can also propose its own. The TUI shows each step and updates it as
  the work progresses.
- **Your choice of model.** Switch between supported providers and models from
  the TUI. The conversation and workflow stay in place.
- **Focused orchestration.** Neo can delegate bounded tasks to subagents and
  show their activity under the relevant workflow step.
- **Control while it works.** Steer the current run, queue the next instruction,
  choose a permission mode, and inspect tool activity without losing context.
- **Small by design.** Neo ships as a single binary with a compact built-in tool
  set. There is no extension system to assemble before it becomes useful.

## Workflows live with the code

Neo follows normal Markdown instructions. A repository can define its process
in `AGENTS.md`:

```markdown
## Code changes

When making code changes:

1. Inspect the relevant code and make a plan.
2. Implement the smallest complete change.
3. Run the relevant tests.
4. Ask a subagent to review the diff.
5. Fix valid findings and report the result.
```

When Neo starts a multi-step task, that process becomes a live checklist in the
terminal. Teams keep the workflow they already use. Neo makes it visible.

## Quick start

Install the latest release:

```bash
curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
```

Neo uses Anthropic by default. Set your key, open a repository, and start it:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
cd your-project
neo
```

Then give Neo a real task:

```text
Find the cause of the failing test, make a plan, implement the fix, and review
the final diff.
```

OpenAI API keys, ChatGPT/Codex subscriptions, OpenRouter, and Gemini are also
supported. See the [quick start](https://neoharness.dev/docs/quick-start/) to
choose a provider, or read the [installation guide](https://neoharness.dev/docs/install/)
for Homebrew, `go install`, and manual builds.

## Documentation

- [Quick start](https://neoharness.dev/docs/quick-start/)
- [Configuration](https://neoharness.dev/docs/reference/config/)
- [CLI reference](https://neoharness.dev/docs/reference/cli/)
- [Tools](https://neoharness.dev/docs/reference/tools/)
- [Permissions](https://neoharness.dev/docs/reference/guides/permissions/)
- [Sessions](https://neoharness.dev/docs/reference/sessions/)
- [Developer documentation](docs/developer/index.md)

## Development

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
go test ./...
go run ./cmd/neo
```

Start with the [developer documentation](docs/developer/index.md) before
changing the agent loop, providers, tools, or TUI.

## License

[MIT](LICENSE) © Neo Contributors
