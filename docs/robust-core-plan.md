# Robust Core Plan

Neo should become a teaching-quality coding agent that is also solid enough for
real coding sessions. The core idea is simple: keep the agent loop small and
policy-free, then layer capabilities around it as explicit interfaces with
boring defaults.

## Architecture Principles

- The core loop owns transcript state, provider calls, and tool-use
  continuation only.
- Capabilities are injected into the loop rather than hardcoded into it.
- Optional behavior should have a null/default implementation, such as
  `NoCompaction`, `NoMemory`, or `NoSubagents`.
- Side effects must pass through explicit permission policy.
- Token efficiency matters: keep stable prompt sections deterministic and
  cache-friendly.
- Errors are data: tool failures should return as `tool_result` blocks the
  model can recover from.

## Robust Core Milestone

The first milestone should make Neo safer and more inspectable without making
the architecture harder to teach.

- Add deterministic tool specs and sorted tool listings.
- Add `grep` and `glob` tools so the model does not need shell commands for
  common repository inspection.
- Add `internal/permission` with a small `Policy` interface and modes:
  `ask`, `trusted`, and `readonly`.
- Default to `ask`: allow read/search tools inside the repo root, ask before
  `bash`, `write_file`, and `edit_file`, and deny path-shaped file tools outside
  the repo root.
- Keep `bash` approval-gated by default. It is not fully sandboxed in this
  milestone; a true shell sandbox can come later.
- Add an approval hook to the agent and a TUI approval prompt for asked actions.
- Show write/edit diffs before approving file mutations.
- Add slash commands for observability: `/tools`, `/permissions`, `/tokens`,
  `/model`, `/clear`, and `/help`.
- Accumulate session token usage in the agent so `/tokens` can report current
  input, output, cache-write, and cache-read counts.
- Add an `internal/compact` seam with `NoCompaction` as the default and a
  `SafeSplitPoint` helper for future strategies.

## Long-Term Capabilities

After the robust core is in place, add larger capabilities one interface at a
time.

- Compaction: `NoCompaction`, `SlidingWindow`, `Summarize`, and later
  token-budget strategies. Never cut between `tool_use` and `tool_result`.
- Memory: `NoMemory` first, then session summaries and project memory. Keep
  memory distinct from transcript persistence.
- Subagents: `NoSubagents` first, then bounded child agents with scoped tools,
  separate transcripts, cancellation, and a clear parent result handoff.
- Context providers: AGENTS.md, skills, git status, memory preamble, and other
  workspace facts should remain independently testable.
- Model catalog: context windows and pricing should drive compaction thresholds,
  `/tokens`, and future cost display.
- Shell sandboxing: add a stronger execution boundary before trusting bash in
  unattended workflows.

## Teaching Shape

Each lesson should introduce one interface, one no-op/default implementation,
and one useful implementation. The intended teaching sequence is:

1. Agent loop and transcript invariant.
2. Provider abstraction.
3. Tool registry.
4. Permission policy.
5. Context loading.
6. Compaction.
7. Memory.
8. Subagents.
9. Observability and debugging.

This keeps Neo readable while making every production concern visible.
