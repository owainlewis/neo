# Parallel Subagents

## Outcome

Neo's coordinator can launch several independent inspection subagents in one response. They run concurrently with tools, return evidence-rich summaries, and cannot race changes in the shared workspace.

The parent remains the coordinator, writer, and final judge.

## Why

Neo already has fresh child contexts, bounded supervision, cancellation, retries, event attribution, and a first-class `agent` tool. The missing piece is safe concurrent execution.

The current dynamic child receives `bash`, `read_file`, `write_file`, `edit_file`, `grep`, and `glob`. Running several such children concurrently in one worktree would allow write/write and read/write races. Worktree isolation could solve that, but it adds branch, merge, cleanup, and conflict behavior that is not needed to make parallel investigation valuable.

## Contract

Extend the existing `agent` tool with one optional mode:

```json
{
  "prompt": "Inspect authentication for privilege escalation paths. Return file:line evidence.",
  "mode": "inspect"
}
```

Modes are parsed once through a typed `AgentMode` used by both classification and execution:

- `work`: existing behavior and default for backward compatibility. It has the current toolset and executes serially.
- `inspect`: read-only child with `read_file`, `grep`, and `glob`. It is parallel-safe.

The raw `prompt` remains the primary interface. No batch tool, structured delegation schema, roles system, or arbitrary tool list is added.

The JSON schema declares `mode` as `enum: ["work", "inspect"]`. Missing mode means `work`. Unknown values fail closed and never fall back to the writable mode.

`agent.ParallelSafe(input)` returns true only after parsing a valid `mode=inspect` call. The generic tool scheduler then supplies concurrency. The supervisor does not need a second scheduler.

Dynamic child execution receives immutable per-run options rather than changing shared `AgentRunner` state:

```go
type RunOptions struct {
	PermissionMode permission.Mode
	Tools          []string
}
```

Inspect calls pass a read-only permission mode and the exact inspection tool allowlist. Work calls preserve the existing tool and permission behavior. This permits inspect and work children to coexist without a race on runner configuration.

## Coordinator Pattern

The parent model emits several `agent` calls in one response:

```text
agent(mode=inspect, prompt="Trace request authentication...")
agent(mode=inspect, prompt="Inspect authorization checks...")
agent(mode=inspect, prompt="Find relevant tests and gaps...")
```

Neo runs them concurrently within the existing agent and supervisor limits. Each child gets only its prompt and inspection tools. The parent receives ordered results, compares the evidence, then decides what to change.

Add a short instruction to the agent tool description:

> For independent investigations, issue several inspect calls together. Use work mode for one delegated change at a time.

This encourages native multi-call output. The runtime does not need to force or rewrite model output.

## Safety

- Inspect children use an explicit allowlist: `read_file`, `grep`, `glob`.
- Inspect children receive a read-only permission policy as defense in depth.
- They do not receive `bash`, file mutation tools, or the `agent` tool.
- Work children preserve current parent permission behavior and remain serial.
- Parent cancellation propagates through the shared context to every active child. Every child event send, including the final usage event, must select on that context.
- Existing supervisor limits bound depth, child count, total agents, and wall time.
- The generic tool scheduler adds a maximum number of concurrent calls.
- One failed, denied, or timed-out child returns its own result and does not cancel siblings.
- Results stay in request order even when workers finish out of order.

Live child events may interleave because the work is genuinely concurrent. Existing node IDs preserve attribution. The parent-facing transcript remains deterministic.

The parent tool-call metadata is carried through the internal execution context into the supervisor. Factory events include the parent call and group identity so the TUI can correlate each child with its source-ordered placeholder. This metadata is runtime-only and never appears in the model-facing tool schema. See `docs/parallel-execution-ui/design.md`.

## Compatibility

Existing calls with only `prompt` or with `max_retries` continue to use `work` mode and execute serially. Existing static workflow agents are unchanged.

Retries stay inside each child call. `StepResult` gains an internal structured failure code or `Retryable` field so the caller does not infer retry behavior from output text. Admission and budget denial, invalid input, permission denial, parent cancellation, and parent timeout are terminal. Only explicitly transient execution failures may retry.

## Scope

In scope:

- `inspect` and `work` modes on the existing `agent` tool.
- Strict shared mode parsing and immutable per-run child options.
- Safe tool and permission filtering for inspect children.
- Parallel classification through the shared tool scheduler.
- Prompt guidance for multi-investigation fan-out.
- Concurrent supervisor, event, cancellation, retry, and ordering tests.
- Parent-call correlation for the parallel subagent UI.
- Generated tool documentation updates.

Out of scope:

- Concurrent writable children in a shared worktree.
- Automatic worktree or container creation.
- Merging child branches or resolving conflicts.
- External coding agents as workers.
- Durable/background agents.
- Nested delegation.
- Arbitrary child tool lists or a role/plugin system.
- A separate batch-agent API.
- General shell access for inspect children.
- New read-only Git tools.

Writable parallel workers should be designed later around isolated worktrees or sandboxes. They must not be enabled by weakening this contract.

Inspection without shell cannot query diffs, staged state, blame, or history. That limitation is accepted for the first release. A narrowly scoped `git_diff` tool is the preferred follow-up if real usage shows that parent-provided context is insufficient. Do not attempt to classify arbitrary shell commands as read-only.

## Acceptance Criteria

- Two `mode=inspect` calls emitted together demonstrably overlap.
- An inspect child cannot invoke `bash`, `write_file`, `edit_file`, or `agent`.
- Invalid mode values return an error and never receive the work toolset.
- Concurrent inspect calls use independent immutable tool and permission options.
- A prompt-only call retains the current work toolset and serial behavior.
- A work call never overlaps another tool call.
- Concurrent children retain distinct node IDs and correctly attributed events.
- Supervisor events correlate each child node with the correct parent call and source position.
- Results are returned in request order, including mixed success, timeout, and denial.
- Parent cancellation stops cancellation-compliant active children, all event sends unblock, and no Neo-owned goroutine leaks.
- Supervisor concurrency stays within existing child, agent, and wall-time budgets.
- Budget denials are not retried.
- Invalid input, permission denial, cancellation, and parent timeout are not retried.
- The race detector reports no event, node, transcript, or policy races.

## Checks

```sh
go test ./internal/factory ./internal/agent ./cmd/neo
go test -race ./internal/factory ./internal/agent ./internal/tui
go test ./...
```

Manual smoke test:

1. Ask Neo to inspect three independent code areas with subagents.
2. Confirm three child nodes are active at once.
3. Confirm the parent remains responsive to interrupt.
4. Ask an inspect child to edit a file and confirm denial.
5. Confirm the parent receives three ordered summaries and can act on them.

## Relevant Code

- `internal/factory/supervisor.go`
- `internal/factory/runner.go`
- `internal/factory/supervisor_test.go`
- `internal/factory/runner_test.go`
- `internal/agent/agent.go`
- `cmd/neo/factory.go`
- `cmd/neo-docs/main.go`
