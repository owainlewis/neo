# Neo Mission

## Purpose

Neo is a fast, workflow-first coding agent for real engineering work. It turns a
task into a visible sequence of planning, tool use, delegated work, verification,
and delivery while keeping the developer in control from one terminal.

## Priorities

In order:

1. A small, dependable core whose behavior is easy to understand and verify.
2. Clear visibility into what the agent, its tools, and its subagents are doing.
3. Safe local execution with explicit authority and useful failure messages.
4. High-quality support for the major model providers without leaking provider
   details into the core agent loop.
5. Fast, responsive interaction in both the terminal UI and headless operation.

## Engineering principles

- Prefer deleting accidental complexity to adding another abstraction.
- Keep `internal/agent` policy-free and provider-neutral.
- Keep composition and product policy at the CLI boundary.
- Preserve one native Go binary with no required server, daemon, plugin runtime,
  database, or product telemetry.
- Make state transitions and failures visible rather than silently recovering or
  discarding work.
- Keep provider adapters behaviorally consistent while respecting genuine API
  differences.
- Require focused tests for changed behavior and keep developer documentation
  aligned with the implementation.
- Add extensibility only when it supports a demonstrated workflow.

## Non-goals

- Neo is not a hosted agent platform or remote orchestration service.
- Neo is not a project-management system, issue tracker, or CI service.
- Neo does not hide autonomous background work behind an opaque interface.
- Neo does not introduce a runtime plugin ecosystem when repository instructions,
  skills, named phases, or built-in interfaces are sufficient.
- Neo does not optimize hypothetical scale at the expense of local simplicity.
- Neo does not add provider-specific policy to the core agent loop.
- Neo does not preserve features whose complexity exceeds their demonstrated value.
- Neo does not trade reliable behavior for decorative terminal UI complexity.

## Continuous improvement

When reviewing Neo for possible improvement:

1. Read this mission, `AGENTS.md`, and the relevant developer documentation.
2. Inspect current code, tests, open issues, and recent changes before proposing work.
3. Prefer a concrete correctness fix or simplification over a speculative feature.
4. Reuse or refine an existing issue instead of creating a duplicate.
5. Create at most one bounded task per review, with observable acceptance criteria.
6. Create no task when there is no worthwhile, mission-aligned improvement.

## Success indicators

Neo is improving when it becomes easier to understand, safer to operate, faster to
use, more consistent across providers, and better at showing evidence for completed
work without growing unnecessary machinery.
