# Neo Goal Runner - Design Spec

Status: Draft.

This spec is for `/goal`, not `/loop`.

A goal keeps the current session working until a condition is met, blocked, paused, cleared, or budget-limited.

A loop runs a prompt on a schedule.

Those are different product features and should stay separate in Neo.

## 1. Source Review

This design is based on the current implementations in Hermes and Codex.

Sources reviewed:

- Hermes `/goal` implementation: https://github.com/nousresearch/hermes-agent/blob/f477f89/hermes_cli/goals.py
- Hermes CLI continuation hook: https://github.com/nousresearch/hermes-agent/blob/f477f89/cli.py
- Hermes user docs: https://hermes-agent.nousresearch.com/docs/user-guide/features/goals
- Codex goal extension: https://github.com/openai/codex/tree/f959e7f/codex-rs/ext/goal
- Codex goal runtime: https://github.com/openai/codex/blob/f959e7f/codex-rs/ext/goal/src/runtime.rs
- Codex goal tools: https://github.com/openai/codex/blob/f959e7f/codex-rs/ext/goal/src/tool.rs
- Codex goal steering prompt: https://github.com/openai/codex/blob/f959e7f/codex-rs/ext/goal/templates/goals/continuation.md

Hermes implements goals as a session-scoped `GoalManager`.

It stores goal state in session metadata, runs a separate judge model after each completed turn, and enqueues a normal continuation prompt when the judge says to continue.

The useful Hermes lessons are:

- Goal state belongs to the session, so resume restores the goal.
- The continuation is a normal turn, not a system prompt mutation.
- User input preempts automatic continuation.
- Ctrl-C pauses the goal instead of immediately re-queueing work.
- Judge failures fail open, with budget as the backstop.
- Weak judge models need a parse-failure backstop.
- Wait barriers matter for long-running processes.

Codex implements goals as a first-class thread extension.

It persists thread goal state, exposes goal tools to the model, injects goal steering when the thread is idle, tracks token and wall-clock budget, emits goal update events, and lets the model mark the goal complete or blocked through `update_goal`.

The useful Codex lessons are:

- Goal state should be explicit product state, not inferred from chat text.
- Completion and blocked status should be explicit state transitions.
- Only the user or system should control pause, resume, budget-limited, usage-limited, and clear.
- Continuation should only start when the thread is idle.
- Budget accounting and goal status changes need serialization so external mutations cannot race idle continuation.
- The continuation prompt must tell the agent not to shrink the objective.
- Goal objective text is user-provided data, not higher-priority instruction.

## 2. Decision

Build `/goal` first.

Do not build `/loop` in this feature.

Do not build reusable goal files in this feature.

Do not expose YAML, TOML, or another goal definition format.

Use plain English as the user surface and typed Go state as the internal representation.

Phase 1 should use a Codex-style goal tool for explicit completion and blocked transitions.

Phase 1 should use a Hermes-style TUI scheduler to enqueue continuation turns when idle.

Do not add a separate judge model in Phase 1.

The judge model is attractive, but it adds a second provider path, latency, parse failures, and a second place for "looks done" mistakes.

Neo can add an optional judge or audit pass later if self-reported completion is too weak.

## 3. Product Definitions

| Term | Meaning | Neo feature |
|------|---------|-------------|
| Goal | Keep working until a condition is met or the run must stop. | `/goal` |
| Workflow | A visible series of steps for how to do current work. | `workflow` tool, often skill-assisted |
| Skill | A reusable instruction package or procedure. | `$skill` expansion |
| Loop | Run on a schedule or interval. | Future automation feature |
| Agent loop | The internal provider and tool-use cycle for one user turn. | Already exists in `internal/agent` |

A workflow is how the agent plans and communicates the work.

A goal is the durable control-plane state that decides whether Neo should continue.

A skill may create or recommend a workflow, but a skill is not a goal.

## 4. User Surface

The first useful version is:

```text
/goal <objective>
/goal status
/goal pause
/goal resume
/goal clear
```

`/goal <objective>` sets or replaces the active goal and starts the first turn immediately.

`/goal status` shows the active goal, state, attempts used, budget, and last reason.

`/goal pause` stops automatic continuation but keeps the goal.

`/goal resume` resumes automatic continuation with a fresh attempt budget.

`/goal clear` removes the goal.

Optional later commands:

```text
/goal draft <objective>
/subgoal <criterion>
/subgoal list
/subgoal remove <n>
/goal wait <pid> [reason]
/goal unwait
```

Do not add `/loop` as an alias for this.

When Neo eventually adds scheduled work, design it separately as automation.

## 5. Internal State

Add an `internal/goal` package.

The user writes plain English.

Neo stores a typed goal object.

The minimum shape is:

```go
type State struct {
    ID          string
    Objective   string
    Contract    Contract
    Status      Status
    Attempts    Attempts
    LastReason  string
    CreatedAt   time.Time
    UpdatedAt   time.Time
}

type Contract struct {
    Outcome      string
    Verification string
    Constraints  []string
    Boundaries   []string
    StopWhen     []string
}

type Attempts struct {
    Used int
    Max  int
}

type Status string

const (
    Active        Status = "active"
    Paused        Status = "paused"
    Complete      Status = "complete"
    Blocked       Status = "blocked"
    BudgetLimited Status = "budget_limited"
    UsageLimited  Status = "usage_limited"
    Cleared       Status = "cleared"
)
```

Persist the state inside the session JSON.

Do not put the full goal in the session index unless the UI later needs it for listing.

Backward compatibility should be simple: old sessions have no `goal` field.

## 6. Goal Tool

Add a model-visible `goal` tool when a goal is active.

It should support two read/write operations:

```text
get_goal
update_goal
```

If Neo prefers one tool, use an `action` field:

```json
{ "action": "get" }
{ "action": "complete", "reason": "go test ./... passed" }
{ "action": "blocked", "reason": "needs API credentials" }
```

The model may only mark a goal `complete` or `blocked`.

The user and system own `active`, `paused`, `budget_limited`, `usage_limited`, and `cleared`.

That split is important.

It avoids letting the model silently resume itself, erase the goal, or bypass budget.

`complete` requires a reason with concrete evidence.

`blocked` should require the same blocker to recur for multiple goal turns before the tool accepts it.

Codex uses a strict blocked audit in its continuation prompt.

Neo should enforce the threshold in code, not only in prose.

## 7. Continuation Algorithm

The goal runner sits above the existing agent loop.

It should not make `internal/agent` understand goals.

Control flow:

```text
/goal objective
create session goal with status active
send objective as the first normal user turn
after send completes, save session
if goal status is complete, blocked, paused, cleared, budget-limited, or usage-limited: stop
if a real user message is queued: let it run first
if attempts are exhausted: mark budget_limited and stop
if TUI is idle: enqueue a continuation turn
repeat
```

Continuation turns are normal user-role messages in Phase 1.

They should be visibly marked as goal continuations so transcripts are understandable.

Later, Neo can add an internal-context message type if we want Codex-style hidden steering.

The continuation prompt must preserve the full objective.

It must tell the agent to verify before calling `goal complete`.

It must tell the agent not to redefine success around a smaller task.

It must tell the agent to call `goal blocked` only after the blocked threshold is satisfied.

## 8. Budget

Phase 1 should use attempts, not token budget.

Attempts are easier to reason about in Neo today because `internal/agent` already reports turn completion but does not yet have Codex-style extension token accounting.

Add config:

```yaml
goals:
  max_attempts: 20
```

An attempt is one automatic continuation turn.

The initial `/goal <objective>` turn does not count as an automatic continuation attempt.

When attempts are exhausted, mark the goal `budget_limited` and show a handoff:

```text
Goal budget reached: 20/20 attempts used.
Last reason: tests still fail in internal/session.
Use /goal resume to continue or /goal clear to stop.
```

Later, add token and wall-clock accounting.

Codex shows that token accounting is valuable, but it is too much for Phase 1.

## 9. User Input And Interrupts

User input always wins.

If the user sends a message while a goal is active, Neo should process the user message before any queued continuation.

After that user turn finishes, the goal scheduler runs again.

Slash commands that inspect or mutate goal control state should be allowed while a turn is active if they are safe.

Slash commands that start new work should require idle.

Esc or Ctrl-C during an active turn should pause the goal after cancellation.

It should not immediately enqueue another continuation.

## 10. Workflow Relationship

Do not merge goals and workflows.

The `workflow` tool is for step visibility.

The `goal` tool is for lifecycle control.

A good goal turn may use the workflow tool to show its current plan.

A workflow item completing does not complete the goal.

Only `goal complete` or a user command should complete the goal.

This keeps "how I am doing it" separate from "whether the durable objective is done."

## 11. Safety

Goals do not grant new permissions.

Goals do not widen write access.

Goals do not bypass approval policy.

Goals do not edit generated files by hand.

The active goal objective is user-provided data.

Continuation prompts must wrap it as task context, not higher-priority instruction.

Path boundaries can be advisory in Phase 1.

Enforced path boundaries can come later through the permission layer.

## 12. Implementation Boundaries

Keep the core agent loop policy-free.

Likely package boundaries:

| Area | Responsibility |
|------|----------------|
| `internal/goal` | Goal state, status transitions, blocked threshold, continuation prompt, tool implementation. |
| `internal/session` | Persist and restore optional goal state. |
| `internal/tui` | Slash commands, status rendering, idle continuation scheduling, interrupt handling. |
| `internal/config` | `goals.max_attempts`. |
| `cmd/neo-docs` | Generated docs for config and commands. |

Do not implement this by teaching `internal/agent` about `/goal`.

Do not implement this by making a workflow checklist durable.

Do not implement this by adding YAML files.

## 13. Alternatives Considered

Hermes-style external judge:

- Upside: independent evaluator can catch some premature self-completion.
- Upside: no model-visible goal tool required.
- Downside: another provider call after every turn.
- Downside: parse failures become product behavior.
- Downside: judge model selection becomes a new config problem.
- Downside: a second model can still be wrong, especially with weak transcript evidence.

Codex-style goal tool and steering:

- Upside: goal state transitions are explicit and inspectable.
- Upside: no hidden judge call or second model config.
- Upside: completion happens in the same transcript as the evidence.
- Upside: continuation prompt can strongly audit completion and blocked claims.
- Downside: the model is grading its own work.
- Downside: prompt quality and tool constraints matter a lot.

Reusable goal files:

- Upside: goals can become shared project assets.
- Downside: introduces trust, path scope, executable verifier, and file format decisions.
- Downside: confuses goals with scheduled loops and workflows.

The Phase 1 recommendation is Codex-style goal tool plus Hermes-style TUI scheduling.

## 14. Build Order

1. Add `internal/goal` state, status transitions, and tests.
2. Add optional goal state to `session.Session` and session save/load tests.
3. Add the `goal` tool with `get`, `complete`, and `blocked` actions.
4. Add `/goal`, `/goal status`, `/goal pause`, `/goal resume`, and `/goal clear`.
5. Add continuation prompt generation and TUI idle scheduling.
6. Add attempt budget handling and `budget_limited`.
7. Add interrupt behavior so cancellation pauses the goal.
8. Add config docs through `go run ./cmd/neo-docs`.
9. Add optional `/goal draft` contract generation later.
10. Add optional wait barriers later.

Stop before scheduled loops.

Stop before reusable goal files.

Stop before YAML.

Those are separate product decisions.
