# Workflow model

Status: proposed. Closes #4. Follow-ups: #5, #7.

## Goal

A workflow is a multi-phase, possibly multi-round agent run that the user
kicks off and observes from inside `neo chat`. Workflows have structure
(named phases in order, status per phase, retries, a final summary) that
deserves a dedicated UI affordance rather than a wall of per-phase agent
output.

The reference UX is Pi's PBE Issue Harness: a workflow run renders as one
addressable block in the chat scrollback with:

- header — workflow name, current round, task summary
- phase list — one row per phase with `✓ / ▶ / ○ / ✗` status + step counter
- detail line — what the active phase's agent is doing right now
- footer / status bar — mirrored in the TUI status line while the run is active

## Non-goals (for v1)

- Running multiple workflows concurrently. v1 is **blocking**: while a
  workflow is active the user cannot start a new chat turn. They can Esc
  to cancel.
- Natural-language workflow invocation. v1 only supports an explicit
  slash command.
- A new role taxonomy (planner / builder / evaluator — #10). The existing
  phase YAML format is unchanged; phases are still just named prompts.
- Persistence as a first-class concern (#13). Artifacts continue to land
  in `.agent/runs/<run-id>/` as today.

## Lifecycle

1. User types `/run <flow-name> <task>` in chat.
2. The TUI appends a `workflowBlock` to its scrollback. The block owns a
   sub-context derived from the chat's context.
3. The TUI starts the workflow engine in a goroutine and binds its event
   sink to push events into the block via `program.Send`.
4. While the workflow runs:
    - Input is disabled (placeholder shows "workflow running — Esc to cancel").
    - Status line shows current phase + round + agent activity.
    - Each agent event for the active phase updates the block's detail line.
    - Phase transitions update the block's phase list.
5. On completion or failure:
    - The block collapses to a one-line summary
      (`✓ implementation — 3 phases, 42s` or `✗ implementation — eval failed`).
    - The full block is re-expandable with `ctrl+o` while focused.
    - The workflow's final assistant text is appended to the chat
      transcript as if a normal turn had produced it, so subsequent chat
      turns can reference it.
6. On Esc during a workflow:
    - Sub-context is cancelled.
    - In-flight tool calls die; phase emits a cancellation marker.
    - Block marks the current phase as ✗ with reason "cancelled by user".

## Engine

New package `internal/workflow`. Engine takes a `Definition` (the existing
flow.Definition shape is fine to start) and a `Sink`. Engine knows nothing
about the TUI; it only emits events.

```go
type Sink interface {
    OnWorkflow(Event)         // workflow-level events
    OnAgent(phase string, e agent.Event)  // bubbled-up agent events
}

type Event struct {
    Kind    EventKind  // WorkflowStarted, PhaseStarted, PhaseCompleted, PhaseFailed, WorkflowCompleted, WorkflowFailed
    Phase   string     // empty for workflow-level events
    Round   int
    Index   int        // 1-based
    Total   int
    Message string     // failure reason, retry note, etc.
    Output  string     // phase output, only on PhaseCompleted
}
```

`internal/flow` becomes a deprecated thin wrapper that delegates to
`internal/workflow` and translates events for the line printer. It will be
deleted once #22 is resolved.

## Sinks

Two implementations land with this work:

- `internal/tui` — sink pushes events into the active `workflowBlock`.
- `cmd/neo` — sink for `neo flow` headless prints the same line-printer
  output as today, so CI / scripts keep working.

## TUI changes

- New block: `workflowBlock`. Renders the Pi-style structure. Holds its
  own phase state slice (`status`, `elapsed`, `agentDetail`).
- Slash command parsing: if `Trim(input).HasPrefix("/")`, route to a
  command handler before treating it as chat text. v1 commands:
    - `/run <flow> <task>` — start a workflow
    - `/cancel` — cancel current workflow (also bound to Esc)
- Status line gains a fourth state: workflow active. Color: magenta or
  cyan, to distinguish from tool-active.

## What gets deleted afterwards

- `internal/flow` (after `internal/workflow` is the only caller).
- `internal/ui` line printer (#22 — once `neo flow` shim uses a minimal
  inline printer).

## Open questions

- Should the workflow block be a single re-rendering block, or one
  appended block per phase transition? Re-rendering is closer to Pi and
  what this design assumes; appending is simpler but spammy.
- Where do phase artifacts surface in chat? Suggest: `/artifacts <run-id>`
  command renders a paged view, doesn't dump into transcript automatically.

## Implementation order

1. Extract `internal/workflow` engine + Sink interface. Translate the
   existing `flow.Runner` tests and behaviour. No UI changes yet.
2. Add `workflowBlock` rendering to the TUI with a fake sink (drive from
   a unit test or a hardcoded scenario) — get the visual right.
3. Wire `/run` slash command + connect engine to TUI sink.
4. Update `cmd/neo flow` to use the same engine via the line-printer sink.
5. Remove `internal/flow`.
