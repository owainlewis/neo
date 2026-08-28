# System Prompt

## The Simple Idea

The system prompt is the agent's instruction sheet. It tells the model what Neo is, what behavior to prefer, and what extra project context is available.

If the user message is the task, the system prompt is the job description.

## The Problem

A coding agent needs stable instructions, but it also needs local context. Some context is almost always the same, such as "prefer small verified changes." Other context changes by project, such as AGENTS.md files or available skills.

If all of that is mashed into one giant string, it is harder to understand, harder to cache, and harder to customize.

## How Neo Solves It

Neo builds the prompt in ordered blocks:

1. A stable base: the core instructions, the capability sections for tools that
   are actually registered, and the named-phase and skill catalogs.
2. A dynamic environment section: working directory, repository root, platform, date.
3. Dynamic project instructions from AGENTS.md files.

The flattened prompt is still available for providers that only accept a string. Providers that support structured system prompts can use `llm.SystemBlock` values instead.

## What Goes Where

| Source | Purpose |
| --- | --- |
| Core prompt | Neo's default behaviour, assuming no particular tool: inspect before changing, smallest change that works, run and report the checks, answer the request that was made, do not commit or deploy unasked. Replaced wholesale by an agent profile when `--agent` is given. |
| Capability sections | Instructions for a specific tool, appended only when that tool is in the registry. |
| Named phase catalog | Names and descriptions of built-in and configured prompts. Full bodies are injected only when invoked. |
| Skill catalog | Names and descriptions of available skills. Full skill bodies are only expanded when invoked with `$name` or `/name args`. |
| AGENTS.md | Project or user instructions that should guide work in this repo. |

## How To Customize It

Use AGENTS.md for durable project instructions. For example:

```md
Read docs/developer/index.md before making changes.
Keep developer docs current when behavior changes.
Run go test ./... after Go changes.
```

Use skills for reusable workflows that should only apply when requested, such as review or commit behavior. Invoke a skill by mentioning `$name` in chat or by running `/name args` from the TUI.

Use named phases for low-friction, configurable prompts that should appear as
first-class slash commands and as the active TUI label. Phase names and
descriptions are always advertised, while their full bodies remain outside the
base prompt until invocation.

## Capability Sections

Behaviour that depends on a tool lives with that tool, not in the always-loaded
core. `capabilitySections` appends the workflow checklist rules when the
`workflow` tool is registered and the delegation rules when `agent` is.

This matters because the registries differ: interactive chat has both tools,
`neo run` has neither. Before this split, headless models were told to "create a
visible workflow checklist with the workflow tool" and to "delegate ... with the
agent tool" — a contract they could not keep, which wastes tokens and invites
invented tool calls.

Two tests enforce it: one asserts the headless prompt never names either tool
while the chat prompt names both, and one checks that any tool the prompt
mentions is in the registry the prompt was built for.

Adding a conditional tool means adding its guidance to `capabilitySections`
rather than to the core prompt.

## Agent Profiles

`--agent <name>` replaces the base prompt with a markdown file, so one binary
can be a coding agent, a personal assistant, or anything else:

```
~/.neo/agents/<name>.md          user-global
<repo>/.neo/agents/<name>.md     project, wins on a name clash
```

A profile is just the file. No frontmatter, no schema, no config entry.

The body **replaces** the base instructions rather than appending to them: a
personal assistant should not be carrying "run tests after you change code".
Everything after the base block still composes — the environment section,
AGENTS.md, and the phase and skill catalogs all apply. If the default coding
phases are noise for a given profile, override `phases` in config.

Restricting a profile's tool set (a genuinely read-only `reviewer`) is the
obvious next step and can be added as frontmatter without changing this layout.

Change the built-in base prompt only when the default behaviour of Neo itself
should change; reach for a profile when you want a different agent.

## What To Be Careful About

Always-loaded prompt text costs tokens every turn. Keep the core short, put
tool-specific behaviour in a capability section so it is absent where the tool
is, and put big or optional workflows in skills rather than the system prompt.

`TestChatSystemPromptSizeBudgets` holds two ceilings: one for the always-loaded
core, one for the core plus capability sections. The first is the number that
matters most, since it is paid in every mode.

## Where To Look

- `cmd/neo/main.go`: chat startup and prompt assembly.
- `internal/projectctx`: AGENTS.md discovery and rendering.
- `internal/phase`: default and configured named prompts.
- `internal/skills`: skill catalog and expansion.
- `internal/llm/provider.go`: `SystemBlock`.
