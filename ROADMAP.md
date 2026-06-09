# Neo Roadmap

Neo is a small, readable Go coding agent. The product goal is to become a
local-first coding agent that is trustworthy enough for real projects while
remaining simple enough to teach from.

This file is not the backlog. It describes the high-level product milestones
that make Neo feel production-ready. GitHub Issues are the source of truth for
implementation status and detailed task breakdowns. If this roadmap disagrees
with an open issue, trust the issue and update this file.

## Production-ready means

Neo should be good enough that a developer can install it, point it at a real
repository, ask for a non-trivial code change, understand what it is doing, stop
it when needed, and resume or inspect the work later without losing trust.

The essential gaps are therefore not just more tools. They are trust, context,
ergonomics, and operational polish.

## Milestone 1: Easy to adopt

New users should understand what Neo is, install it, configure a model provider,
and run a first useful coding session without reading source code.

Essential capabilities:

- Clear README onboarding and first-run path.
- Reliable provider setup for Anthropic, OpenAI API keys, and OpenAI
  subscription auth.
- `neo doctor` or equivalent environment checks for common setup failures.
- Regular releases, clear versioning, and an update path for installed binaries.
- Contributor docs that explain the architecture without becoming product docs.

Related tracking:

- [#110 Improve README onboarding and project overview](https://github.com/owainlewis/neo/issues/110)
- [#66 OpenAI provider support](https://github.com/owainlewis/neo/issues/66)
- [#53 neo doctor](https://github.com/owainlewis/neo/issues/53)
- `TICKETS.md` NEO-1 for `neo update`

## Milestone 2: Safe and controllable coding sessions

Neo should be safe to use in a real repository. The user should always be able
to see risky actions, approve or deny them, and cancel work cleanly.

Essential capabilities:

- Strong workspace boundaries for file and shell tools.
- Approval UX that is clear, focused, and inspectable for long diffs or shell
  commands.
- Mid-turn cancellation that stops model loops and tool execution while
  preserving transcript validity.
- Better visibility into what tools are available and what the current
  permission mode allows.
- Structured logging or trace output for debugging surprising behavior.

Related tracking:

- [#16 Path sandbox for filesystem and bash tools](https://github.com/owainlewis/neo/issues/16)
- [#94 Permission UX and approval modal](https://github.com/owainlewis/neo/issues/94)
- [#64 Mid-turn cancellation through tool execution](https://github.com/owainlewis/neo/issues/64)
- [#18 Structured logging to file](https://github.com/owainlewis/neo/issues/18)

## Milestone 3: Comfortable day-to-day coding UX

Neo should feel fast and practical during repeated terminal use, not merely
correct. The agent needs enough project context and interface polish to keep
ordinary coding work moving.

Essential capabilities:

- Git context in the prompt: branch, status, and recent commits.
- Fast file references, search tools, and repository inspection without needing
  shell commands for every lookup.
- Multi-line input, better handling of large tool results, and compact TUI
  displays for common workflows.
- Custom slash commands or prompt snippets for repeated local project tasks.
- Model and session browsing that stays useful as the number of sessions grows.

Related tracking:

- [#63 Git context injection](https://github.com/owainlewis/neo/issues/63)
- [#23 TUI multi-line input and expanded tool results](https://github.com/owainlewis/neo/issues/23)
- [#69 Custom slash commands](https://github.com/owainlewis/neo/issues/69)
- `TICKETS.md` NEO-6 for session transcript search
- `TICKETS.md` NEO-8 for `/tools` display polish

## Milestone 4: Longer useful context

Neo should handle larger projects and longer conversations without silently
degrading as the transcript grows. Users should understand token usage and be
able to recover useful prior context.

Essential capabilities:

- Token-aware context compaction that cuts only at valid transcript boundaries.
- Session-level token accounting and cost visibility.
- Model catalog data for context windows, pricing, and compaction thresholds.
- Search over saved sessions as the first practical memory layer.
- A deliberately scoped memory feature that is off by default until it earns
  trust.

Related tracking:

- [#65 Token-aware context compaction](https://github.com/owainlewis/neo/issues/65)
- [#55 Cost accounting per chat session](https://github.com/owainlewis/neo/issues/55)
- [#95 Model catalog and cost display](https://github.com/owainlewis/neo/issues/95)
- [#96 Memory store and recall](https://github.com/owainlewis/neo/issues/96)
- `TICKETS.md` NEO-6 for session transcript search
- `TICKETS.md` NEO-7 for the memory stub

## Milestone 5: Project workflows and larger tasks

Once single-agent sessions are dependable, Neo can grow into larger coding
workflows without making the core loop complicated. This is a key product
differentiator: users should be able to define repeatable project workflows
that chain shell checks, agent calls, and later subagents into visible,
step-by-step runs.

Essential capabilities:

- Project workflows stored under `.neo/workflows/`, each with a name,
  description, and ordered steps.
- A simple workflow format that initially supports linear `shell` and `agent`
  steps.
- Slash-command or command-palette invocation so workflows feel native in the
  TUI.
- Step-by-step run UI that shows each workflow step, status, output, and failure
  point.
- Optional subagents with scoped tools, bounded turns, isolated transcripts, and
  clear handoff back to the parent agent.
- Long-lived or always-on mode for event-driven work, with stronger safety
  boundaries than interactive use.

Related tracking:

- [#97 Subagents with scoped tools](https://github.com/owainlewis/neo/issues/97)
- [#71 Workflows, done properly](https://github.com/owainlewis/neo/issues/71)
- [#70 Long-lived / always-on agent mode](https://github.com/owainlewis/neo/issues/70)

## Recently shipped foundations

These are no longer roadmap goals, but they define the base Neo is building on:

- Policy-free agent loop with strict `tool_use` / `tool_result` transcript
  invariants.
- Anthropic and OpenAI provider adapters.
- Permission modes: `ask`, `trusted`, and `readonly`.
- File, edit, shell, grep, and glob tools.
- AGENTS.md and skill loading.
- Session persistence and session/model browsers.
- Slash-command observability: `/help`, `/tools`, `/permissions`, `/tokens`,
  `/model`, `/sessions`, and `/clear`.
- Fast terminal affordances: `@file` references and `!command`.
- Generated developer docs and teaching guides.

## Product principles

- **Trust before autonomy.** Neo should earn confidence in interactive sessions
  before running larger unattended workflows.
- **Local-first and inspectable.** The user should be able to understand what
  Neo knows, what it is allowed to do, and what it changed.
- **Small core, layered capability.** The agent loop stays policy-free;
  production behavior is injected around it.
- **Readable over clever.** The codebase is a teaching artifact as well as a
  tool.
- **Context is a product feature.** Token usage, compaction, sessions, memory,
  and model choice should be visible and intentional.
- **Issues hold the detail.** This roadmap sets direction; issues and
  `TICKETS.md` hold implementation slices.
