# Parallel Tool Calls

## Outcome

Neo executes independent, non-mutating tool calls concurrently when a model returns several calls in one response. Mutating and unknown tools remain serial.

The model does not decide whether a call is safe to parallelize. The runtime does.

## Why

The provider adapters already preserve multiple tool calls in one response. The agent loop currently executes those calls one at a time, which wastes time on independent reads and searches.

A `parallel=true` argument on each call would put a safety decision in model output. A mistaken flag could race a write, shell command, or external side effect. It would also leak scheduler details into every tool schema.

## Contract

Add one optional capability beside the existing `tools.Tool` interface:

```go
type ParallelTool interface {
	ParallelSafe(input map[string]any) bool
}
```

The registry exposes the classification to the agent loop. Tools that do not implement the capability, return false, or cannot be resolved are serial. Serial is always the safe default.

For the first release:

- `read_file`, `grep`, and `glob` are parallel-safe.
- `bash`, `write_file`, `edit_file`, `workflow`, and unknown tools are serial.
- `agent` decides from its validated mode. Read-only inspection is parallel-safe. Writable work is serial.

The registry owns the lookup so the agent loop does not contain a list of tool names.

## Scheduling

Before scheduling a call, the coordinator resolves the tool and evaluates its permission policy serially. A denied call receives its normal result. A call requiring approval is handled serially and becomes a barrier. Execution receives the completed authorization decision so policy is not evaluated twice and approval callbacks cannot overlap.

For each provider response, the loop keeps the original content order and divides calls into groups:

- Consecutive parallel-safe calls run concurrently.
- A serial call is a barrier. All earlier calls finish before it starts, and it finishes before later calls start.
- A text block ends the current group and keeps its existing event and transcript position.

Examples:

```text
read A, read B             => A and B overlap
read A, write B, read C    => A, then B, then C
read A, unknown B, read C  => A, then B, then C
```

Concurrency is bounded by an internal agent setting with a default of 8. The first release does not add a YAML option.

Each worker writes one indexed outcome to the coordinator. The coordinator alone emits events and updates the transcript. This keeps `OnEvent` serial and avoids making the TUI concurrency-safe.

Every call and result event carries the provider tool-use ID plus opaque group identity and source position. For groups of at least two calls, one `EventParallelStart` containing the ordered call summaries is emitted before work begins. This lets the TUI allocate a stable block atomically without exposing scheduler metadata to the model.

Tool call events are emitted in source order before a group starts. When the group settles, result events and `tool_result` blocks are emitted in source order. Provider pairing, recordings, UI state, and replay therefore remain deterministic. Attributed progress from child agents may interleave while they run. The full UI contract is in `docs/parallel-execution-ui/design.md`.

One failed call does not cancel its siblings. Context cancellation is delivered to the whole active group. Parallel-enabled tools must honor context cancellation. Calls that were not started receive synthetic cancelled results. The loop waits for started, cancellation-compliant workers before committing paired results.

The coordinator checks steering before every group or serial call and after each active group settles. Once steering is observed, every unstarted call is skipped, including the next serial barrier, using the existing paired error result. Already-started calls are never abandoned in the transcript.

## Getting Models To Use It

Neo cannot parallelize work that the model emits across separate responses. Add one short stable instruction:

> When you need several independent reads, searches, or inspections, issue those tool calls together in one response.

Tool descriptions should describe capability, not scheduler mechanics. No `parallel` call argument is exposed. All supported provider adapters already carry multiple calls, so no provider-specific protocol is required.

## Errors And Limits

- Unknown tools return their normal error and act as serial barriers.
- Permission decisions are made serially before scheduling.
- Approval callbacks always run serially on the coordinator.
- A denied call does not cancel unrelated calls in its group.
- Parallel-enabled tools must document and test prompt cancellation.
- Panics keep the existing process behavior and are outside this change.

## Scope

In scope:

- Optional tool execution capability.
- Bounded scheduler in the agent loop.
- Serial permission preflight and approval handling.
- Parallel classification for built-in read/search tools.
- Deterministic transcript and serialized events.
- Stable call/group event identity for parallel UI rendering.
- Cancellation, failure, ordering, and race tests.
- One concise model instruction.

Out of scope:

- Model-controlled `parallel=true` flags.
- Inferring whether arbitrary shell commands are read-only.
- Dependency graphs or speculative execution.
- User-configured scheduling policy.
- Parallel writes.

## Acceptance Criteria

- Two blocking parallel-safe fake tools demonstrably overlap.
- Serial tools never overlap another tool and form barriers.
- Unknown and unclassified tools default to serial.
- Results are stored in original call order even when completion order differs.
- Tool call and result events are emitted in source order, and callbacks are never invoked concurrently.
- A parallel-start event declares every row before execution and all later events correlate by tool-use ID.
- Permission approval callbacks are never invoked concurrently.
- Failure, denial, timeout, cancellation, and steering keep every tool call paired with one result.
- Steering skips every unstarted call, including a following write barrier.
- Cancellation synthesizes results for calls that have not started.
- Anthropic, OpenAI Responses, OpenAI-compatible Chat Completions, and Google tests each prove that two calls and their paired results preserve order.
- Existing single-call behavior remains compatible.
- Race-enabled agent and full repository tests pass.

## Checks

```sh
go test ./internal/agent ./internal/tools ./internal/factory
go test -race ./internal/agent ./internal/tools ./internal/factory ./internal/tui
go test ./...
```

## Relevant Code

- `internal/tools/tool.go`
- `internal/agent/agent.go`
- `internal/agent/agent_test.go`
- `internal/tools/*_test.go`
- `cmd/neo/main.go`
- Provider adapter tests under `internal/llm/`
