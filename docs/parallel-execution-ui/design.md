# Parallel Execution UI

## What and why

Neo should make concurrency visible without turning the terminal into a dashboard. Users need to see that several tools or subagents are active, understand what each is doing, and notice partial failure.

The current TUI tracks one `currentTool` and identifies results mainly by tool name. That cannot represent concurrent calls, especially two calls to the same tool. The subagent tree can already show several roots, but it has no explicit parallel state or stable source ordering.

The design adds a small amount of execution identity to events and renders parallel work as an inline group. It uses Neo's existing cyan, green, red, white, and muted-grey palette. There are no cards, borders, new panels, or new controls.

## Requirements

- Show when two or more tool calls are executing as one parallel group.
- Show when two or more top-level subagents are executing as one parallel group.
- Identify every call by an opaque call ID rather than its tool name.
- Preserve the model's source order even when work completes in another order.
- Keep the block height stable from group start to group completion.
- Keep the fixed status area to one line and preserve its existing spacing above the composer.
- Show success, failure, elapsed time, and current child activity through color and small glyphs.
- Work at narrow terminal widths through truncation, not horizontal scrolling.
- Preserve existing rendering for single tool and single subagent calls.
- Remain useful without color.

## Acceptance criteria

- A two-call read group appears atomically as one header and two ordered rows before execution begins.
- Two calls to the same tool update the correct rows.
- Completion order does not reorder rows.
- A parallel subagent group shows each child prompt summary and live attributed activity.
- The status line says `N tools in parallel` or `N subagents in parallel` while the group is active.
- When a workflow is active, its `step/total` summary remains first in the status line.
- Successful rows use green checks, running rows use cyan dots, failures use red crosses, and connectors/details/times remain muted.
- A failed row remains visible and the group header settles to a failed state.
- Cancellation settles every row as completed, failed, or cancelled. Nothing remains visually active.
- Starting and settling a group does not collapse rows, reorder blocks, move the composer, or change the workflow panel height.
- Existing sequential tool receipts and single-agent trees render unchanged.
- Snapshot tests cover 40, 80, and 120-column terminals with color removed.

## Design

### Parallel tools

Concise mode renders one inline block:

```text
● 3 tools in parallel
├─ ● Read internal/agent/agent.go                 2s
├─ ✓ Search "EventToolCall"                     <1s
└─ ● Match internal/tui/*.go                      2s
```

When complete, the same rows remain in place:

```text
✓ 3 tools in parallel                             2s
├─ ✓ Read internal/agent/agent.go                 1s
├─ ✓ Search "EventToolCall"                     <1s
└─ ✓ Match internal/tui/*.go                      2s
```

The block is created from one group-start event containing every call in source order. It therefore appears at its final height in one render. Result events only replace glyphs, details, and elapsed values.

Successful result bodies stay hidden in concise mode. Failed result bodies use the existing error block immediately after the group. Verbose mode keeps the group summary and may also show the existing result cards.

### Parallel subagents

The existing tree gains a group header when several top-level `agent` calls share a parallel group:

```text
● 3 subagents in parallel
├─ ● Inspect authentication   Reading middleware.go      12s
├─ ✓ Find missing tests                                  9s
└─ ● Review permissions      Searching policy.go        12s
```

The rows are preallocated from the parent group-start event using the first line of each child prompt. Supervisor events attach live detail and node identity using the parent call ID. If a supervisor event is dropped, the parent tool result still settles the correct row.

Single subagents keep the existing tree rendering. Nested tree rendering is unchanged and remains below its owning row if nested delegation is enabled later.

### Status line

The fixed status line shows only the most useful concurrency summary:

```text
● 3 tools in parallel · Reading agent.go +2 · 4s
● 3 subagents in parallel · Inspect authentication +2 · 12s
● 2/4 Review implementation · 3 subagents in parallel · 12s
```

Priority is:

1. Approval state.
2. Workflow step.
3. Parallel subagents.
4. Parallel tools.
5. One sequential tool.
6. Thinking.

On narrow terminals, Neo removes the activity detail and keyboard hint before truncating the primary state. The status line never wraps.

### Visual language

- Running group and row: cyan `●` using `styTool`.
- Successful group and row: green `✓` using `styOK`.
- Failed group and row: red `✗` using `styErr`.
- Cancelled or not-started row: muted `-`.
- Header label and count: bold default foreground.
- Connectors, activity, and elapsed time: `styDim` or `styMuted`.
- Model thinking retains the existing orange spinner.

No background fill is added. Color communicates state, while glyphs preserve meaning in monochrome terminals.

## Interfaces and data

Extend core agent events with stable execution identity:

```go
type Event struct {
	// Existing fields...
	ToolUseID string
	GroupID   string
	GroupSize int
	GroupPos  int
	Calls     []ToolCallRef // populated only on EventParallelStart
}

type ToolCallRef struct {
	ID   string
	Name string
	Args map[string]any
}
```

Add `EventParallelStart`. It is emitted only for groups containing at least two calls and is emitted once, before any call begins. Existing `EventToolCall` and `EventToolResult` events carry `ToolUseID`, `GroupID`, `GroupSize`, and `GroupPos`.

Group IDs are opaque and unique within a turn. Positions are zero-based source positions. Consumers must not derive identity from names, arguments, timestamps, or arrival order.

Tool execution context carries the same metadata internally. `AgentTool` passes it to `Supervisor.RunAgentPrompt`. Attributed factory events add:

```go
CallID   string
GroupID  string
GroupPos int
GroupSize int
```

This lets the supervisor tree update the placeholder created by the parent event. It does not expose scheduler metadata to model tool schemas.

The TUI replaces `currentTool` with maps keyed by call and group ID. Existing turn-level counts continue to count individual tools.

No event is added for group completion. The TUI settles a group when every declared call has received a result. `EventDone`, `EventError`, or cancellation force-settles any remaining rows defensively.

## Failure behavior

- A duplicate or unknown call ID is ignored and logged rather than updating the wrong row.
- A result without a group falls back to existing sequential rendering.
- A missing supervisor start event leaves the subagent placeholder intact; the parent result settles it.
- A missing result is force-settled when the turn ends so the UI never displays permanent activity.
- A denied or skipped call uses a red failure or muted cancelled state according to its result.
- If terminal width is too small for detail, preserve glyph, count, primary label, and elapsed time in that order.

## Test approach

- Unit-test group folding with two identical tool names and reversed completion order.
- Unit-test atomic row allocation and constant rendered line count across running, partial, complete, failed, and cancelled states.
- Unit-test supervisor event correlation through call IDs.
- Unit-test status priority with workflow, subagents, tools, approval, queue, and narrow widths.
- Add plain-text golden snapshots for tool and subagent groups at 40, 80, and 120 columns.
- Test missing, duplicate, and late events.
- Run the race detector over agent, factory, and TUI packages.
- Manually verify a three-read group and three inspect subagents in a real TUI session.

## Risks

- More event metadata can couple runtime and UI. Mitigation: keep IDs opaque and lifecycle semantics generic.
- Parallel child activity can be noisy. Mitigation: show only the latest child line and hide successful result bodies in concise mode.
- New blocks can recreate earlier layout jumping. Mitigation: allocate every row atomically at group start and never collapse a live or completed group.
- Dropped factory events can leave stale child detail. Mitigation: use parent tool results as the authoritative completion fallback.

## Out of scope

- A separate agents dashboard or modal.
- Background-agent management controls.
- Expand/collapse controls for parallel groups.
- Per-group token accounting.
- Reordering by completion time.
- New theme configuration or animation system.
- Showing concurrency when only one call is active.
