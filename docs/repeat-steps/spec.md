# Repeat Steps Spec

## What

Add `type: repeat` to Neo flow files as a first-class step kind. A repeat step runs an evaluator, decides whether the loop has passed, and only runs repair steps when the evaluator reports failure. This keeps simple linear flows unchanged while allowing complex flows like "write code, judge code, fix findings until clean, then open a PR" in the same YAML DSL.

## Context

Neo currently supports file-based workflows with a single top-level `steps:` list. Each step is either:

- `type: agent`, with a Markdown prompt loaded by `internal/workflow/flowfile.go`
- `type: command`, with a shell command rendered through the same template context

The engine in `internal/workflow/workflow.go` executes these steps linearly, emits workflow events for the TUI, writes step artifacts, and passes prompt context through `internal/phase/phase.go`.

This works well for simple cases:

```yaml
steps:
  - name: plan
    type: agent
    prompt: flows/PLAN.md

  - name: test
    type: command
    run: go test ./...

  - name: summarize
    type: agent
    prompt: flows/SUMMARY.md
```

It does not yet support evaluator-driven iteration. The prior `retry_from` behavior for named config flows relies on agent output heuristics and is intentionally not part of file-flow v1. The desired next primitive is a bounded loop where the runtime owns control flow and the evaluator owns pass/fail.

## Requirements

1. Simple flow files without repeat steps must continue to work exactly as they do now.
2. The same top-level `steps:` DSL must support both simple linear steps and complex repeat steps.
3. `type: repeat` must be usable anywhere a normal step is usable in a flow file.
4. A repeat step must have a bounded `max` attempt count.
5. A repeat step must have one `evaluate` block.
6. A repeat step must have a non-empty nested `steps:` list of repair steps.
7. The evaluator must run before repair steps on each attempt.
8. If the evaluator passes, the repeat step completes and the outer workflow continues.
9. If the evaluator fails, the nested repair steps run, then the evaluator runs again on the next attempt.
10. If the evaluator is unsure, the repeat step fails as blocked and the outer workflow stops.
11. If `max` attempts are exhausted without a pass, the repeat step fails and the outer workflow stops.
12. Evaluators must support both deterministic command checks and agent judges.
13. Agent judges must report structured status through a workflow-only outcome tool, not through free-form magic strings.
14. Repair prompts must be able to read the latest evaluator findings through template context.
15. Repeat events must be visible in the TUI without requiring a separate "flow mode" or separate DSL.
16. The implementation must avoid provider-specific reasoning, thinking traces, or streaming dependencies.

## Design

### YAML Shape

Simple flows remain unchanged:

```yaml
steps:
  - name: write-code
    type: agent
    prompt: flows/WRITE_CODE.md

  - name: open-pr
    type: agent
    prompt: flows/OPEN_PR.md
```

Complex flows add a repeat step inside the same list:

```yaml
steps:
  - name: write-code
    type: agent
    prompt: flows/WRITE_CODE.md

  - name: review-until-passed
    type: repeat
    max: 3
    evaluate:
      name: judge-code
      type: agent
      prompt: flows/JUDGE_CODE.md
      outcome_tool: report_outcome
      tools: [read_file, bash]
    steps:
      - name: fix-review-findings
        type: agent
        prompt: flows/FIX_REVIEW_FINDINGS.md
        tools: [read_file, edit_file, write_file, bash]

  - name: open-pr
    type: agent
    prompt: flows/OPEN_PR.md
```

Command evaluators use shell exit status:

```yaml
steps:
  - name: build
    type: agent
    prompt: flows/BUILD.md

  - name: tests-until-passed
    type: repeat
    max: 3
    evaluate:
      name: go-test
      type: command
      run: go test ./...
    steps:
      - name: fix-tests
        type: agent
        prompt: flows/FIX_TESTS.md
```

PR review repair flow:

```yaml
steps:
  - name: write-code
    type: agent
    prompt: flows/WRITE_CODE.md

  - name: open-pr
    type: agent
    prompt: flows/OPEN_PR.md

  - name: resolve-pr-feedback
    type: repeat
    max: 5
    evaluate:
      name: collect-pr-feedback
      type: agent
      prompt: flows/COLLECT_PR_FEEDBACK.md
      outcome_tool: report_outcome
      tools: [bash, read_file, write_file]
    steps:
      - name: fix-pr-feedback
        type: agent
        prompt: flows/FIX_PR_FEEDBACK.md
        tools: [read_file, edit_file, write_file, bash]

      - name: push-fixes
        type: command
        run: git diff --quiet || git push
```

### Evaluator Semantics

Repeat runs `evaluate` first on every attempt.

For `type: command` evaluators:

- exit code `0` means `passed`
- non-zero exit means `failed`
- findings are the formatted command output
- command evaluators cannot produce `unsure`

For `type: agent` evaluators:

- the evaluator receives a workflow-only `report_outcome` tool
- the evaluator must call the tool before completing
- the engine captures the tool input as the repeat outcome
- normal final assistant text may still be stored as evaluator output, but it does not drive control flow

Outcome schema:

```json
{
  "status": "passed",
  "findings": "No blocking issues found."
}
```

Tool schema:

```json
{
  "name": "report_outcome",
  "description": "Report the evaluator outcome for workflow control flow.",
  "input_schema": {
    "type": "object",
    "properties": {
      "status": {
        "type": "string",
        "enum": ["passed", "failed", "unsure"]
      },
      "findings": {
        "type": "string"
      }
    },
    "required": ["status", "findings"]
  }
}
```

Runtime behavior:

```text
for attempt in 1..max:
  outcome = evaluate()
  if outcome.status == passed:
    complete repeat step
    continue outer workflow
  if outcome.status == unsure:
    fail repeat step as blocked
    stop outer workflow
  if attempt == max:
    fail repeat step with latest findings
    stop outer workflow
  run repair steps with latest findings
```

### Prompt Context

Extend `phase.Input` and the template context with an optional `.Repeat` object.

Proposed fields:

```go
type RepeatRef struct {
    Name     string
    Attempt  int
    Max      int
    Status   string
    Findings string
}
```

Available in evaluator and repair prompts inside the repeat step:

| Variable | Description |
|---|---|
| `.Repeat.Name` | Repeat step name, e.g. `review-until-passed`. |
| `.Repeat.Attempt` | 1-based current attempt. |
| `.Repeat.Max` | Configured max attempts. |
| `.Repeat.Status` | Latest evaluator status. Empty before the evaluator has run on the current attempt. |
| `.Repeat.Findings` | Latest evaluator findings. Empty on the first evaluate call unless prior findings exist. |

Repair prompt example:

```markdown
# Task

{{ .Task }}

# Review findings

{{ .Repeat.Findings }}

# Instructions

Fix only the blocking findings above. Do not expand scope.
```

Judge prompt example:

```markdown
# Task

{{ .Task }}

# Role

Review the current implementation for blocking issues.

# Outcome

Call `report_outcome` with:

- `status: passed` if there are no blocking issues
- `status: failed` if there are blocking issues
- `status: unsure` if you cannot determine correctness

Put the findings in plain English.
```

### Data Model Changes

Extend `flowFileStep`:

```go
type flowFileStep struct {
    Name        string         `yaml:"name"`
    Type        string         `yaml:"type"`
    Prompt      string         `yaml:"prompt"`
    Run         string         `yaml:"run"`
    Tools       []string       `yaml:"tools"`
    Model       string         `yaml:"model"`
    Max         int            `yaml:"max"`
    Evaluate    *flowFileStep  `yaml:"evaluate"`
    Steps       []flowFileStep `yaml:"steps"`
    OutcomeTool string         `yaml:"outcome_tool"`
}
```

Extend `StepKind`:

```go
const (
    StepAgent   StepKind = "agent"
    StepCommand StepKind = "command"
    StepRepeat  StepKind = "repeat"
)
```

Extend `StepDefinition`:

```go
type StepDefinition struct {
    Name        string
    Kind        StepKind
    Prompt      string
    Run         string
    Tools       []string
    Model       string
    Source      string
    Max         int
    Evaluate    *StepDefinition
    Steps       []StepDefinition
    OutcomeTool string
}
```

Validation:

- `agent` requires `prompt`
- `command` requires `run`
- `repeat` requires `max > 0`
- `repeat` requires `evaluate`
- `repeat.evaluate` must be `agent` or `command`
- `repeat.steps` must be non-empty
- nested repair steps may be `agent` or `command`
- nested repeat inside repeat is out of scope for v1
- all step names in a flow file, including evaluator and nested repair steps, must be unique

The unique-name rule keeps `.Steps` behavior simple and avoids ambiguous event routing.

### Engine Changes

Keep the existing linear engine shape. `Run` still iterates over top-level `StepDefs`. `runStep` dispatches by kind:

- `StepAgent`: current behavior
- `StepCommand`: current behavior
- `StepRepeat`: new `runRepeatStep`

`runRepeatStep` owns its nested attempt loop. It does not use the legacy named-flow `retry_from` heuristic.

Pseudo-code:

```go
func (e *Engine) runRepeatStep(ctx context.Context, repeat StepDefinition, in phase.Input) (string, error) {
    var latest Outcome
    var combined strings.Builder

    for attempt := 1; attempt <= repeat.Max; attempt++ {
        repeatInput := in
        repeatInput.Repeat = &phase.RepeatRef{
            Name: repeat.Name,
            Attempt: attempt,
            Max: repeat.Max,
            Status: latest.Status,
            Findings: latest.Findings,
        }

        outcome, evalOutput, err := e.runEvaluator(ctx, repeat, repeatInput)
        if err != nil {
            return combined.String(), err
        }
        latest = outcome

        if outcome.Status == "passed" {
            return formatRepeatOutput(latest, combined.String()), nil
        }
        if outcome.Status == "unsure" {
            return formatRepeatOutput(latest, combined.String()), fmt.Errorf("repeat %q blocked: %s", repeat.Name, outcome.Findings)
        }
        if attempt == repeat.Max {
            return formatRepeatOutput(latest, combined.String()), fmt.Errorf("repeat %q failed after %d attempts: %s", repeat.Name, repeat.Max, outcome.Findings)
        }

        repeatInput.Repeat.Status = outcome.Status
        repeatInput.Repeat.Findings = outcome.Findings
        for _, repair := range repeat.Steps {
            _, err := e.runConcreteStep(ctx, repair, repeatInput)
            if err != nil {
                return combined.String(), err
            }
        }
    }
}
```

The implementation should factor current `runStep` into smaller helpers so repeat can run concrete nested steps without pretending they are top-level indexes.

### Outcome Tool Capture

The clean implementation is not a normal global tool in `newRegistry`.

Add a workflow-local tool implementation, probably in `internal/workflow/outcome_tool.go`, that:

- implements `tools.Tool`
- validates `status`
- captures the most recent outcome in a pointer owned by the evaluator run
- returns a short textual confirmation to the model

For evaluator agent runs:

1. Build a tool registry that includes the evaluator's allowed tools plus `report_outcome`.
2. Run the agent.
3. After agent completion, inspect the captured outcome.
4. If no outcome was captured, fail the evaluator with a clear error:

```text
evaluator "judge-code" did not call report_outcome
```

Assumption: tool calls are already part of the provider abstraction, so this does not require provider-specific structured-output APIs.

### Events and TUI

Do not create a separate workflow mode. The TUI continues to render one top-level row per top-level step. Repeat details appear beneath the active repeat row.

Minimum v1 rendering:

```text
▶ review-until-passed  2/3  attempt 2/3
    ✓ judge-code  failed
    ▶ fix-review-findings
```

Recommended event additions:

```go
const (
    RepeatAttemptStarted EventKind = "repeat_attempt_started"
    RepeatEvaluationCompleted EventKind = "repeat_evaluation_completed"
)
```

Alternatively, v1 can render repeat internals through existing `OnAgent` detail rows first, then add specialized events later. The implementation should prefer the smallest UI change that makes attempts and latest findings visible.

When a repeat step passes, its top-level row completes and stores a repeat summary as output:

```markdown
status: passed
attempts: 2/3

findings:
No blocking issues found.
```

This output becomes available through `.Prev.Output` and `.Steps.review-until-passed.Output`.

### Artifact Layout

Continue using the existing artifact store. Nested steps should be written with stable artifact names that avoid collisions:

```text
<repeat-name>__<attempt>__<child-step-name>
```

Examples:

```text
review-until-passed__1__judge-code
review-until-passed__1__fix-review-findings
review-until-passed__2__judge-code
```

This is implementation detail; prompt context should use `.Repeat` and `.Steps`, not artifact paths.

## Decisions

### Decision: `repeat` is a step type in the same `steps:` list

Alternatives considered:

- a separate top-level `loops:` section
- legacy `retry_from` on file flows
- an orchestrator agent that decides when to loop

Chosen because it keeps simple and complex workflows in one composable DSL. Reversible if repeat grows into a larger workflow language, but good enough for bounded v1 loops.

### Decision: evaluate before repair

Alternatives considered:

- run repair first, then evaluate
- require a separate initial judge step outside repeat

Chosen because it supports both "already clean" cases and repair loops. It also makes command checks like `go test ./...` natural: pass means do nothing, fail means repair.

### Decision: agent judges must use `report_outcome`

Alternatives considered:

- parse final text
- require `PASS` or `FAIL` magic strings
- use provider-specific structured output APIs

Chosen because it gives the runtime structured status without depending on one provider's API. Reversible if the provider abstraction later supports first-class structured outputs.

### Decision: `unsure` blocks instead of looping

Alternatives considered:

- treat `unsure` as `failed`
- ask the fixer to resolve uncertainty

Chosen because "unsure" means the evaluator lacks enough information for a safe decision. Blind repair can waste attempts or create unrelated changes. This is reversible per-flow later if a real use case needs `unsure_behavior: repair`.

### Decision: globally unique step names in flow files

Alternatives considered:

- scoped names like `repeat.step`
- duplicate names with path-based event routing

Chosen to preserve the current `.Steps[name]` mental model and keep TUI event routing simple. Reversible later by introducing explicit step paths.

### Decision: no nested repeat in v1

Alternatives considered:

- arbitrary recursive repeat steps

Chosen to keep the first implementation understandable. Nested repeat is powerful but increases TUI, artifact, and context complexity.

## Invariants

- Existing linear flow files must load and run unchanged.
- Existing command step semantics must remain exit-code based.
- Existing agent step semantics must remain completion based.
- Legacy named config flows must continue to use the existing `retry_from` behavior until explicitly migrated.
- Runtime control flow must not depend on natural-language parsing of agent messages.
- Repeat loops must always be bounded by `max`.
- `report_outcome` must not be available to normal chat or non-evaluator steps by default.
- If an evaluator returns `failed`, the latest findings must be available to repair prompts through `.Repeat.Findings`.
- If a repeat step passes, downstream steps such as `open-pr` must run exactly once after the repeat completes.

## Error Behavior

Flow loading errors:

- missing repeat `max`: fail load
- `max <= 0`: fail load
- missing `evaluate`: fail load
- missing nested `steps`: fail load
- unsupported evaluator type: fail load
- duplicate step name anywhere in the flow tree: fail load
- nested repeat in v1: fail load

Runtime errors:

- command evaluator non-zero: status `failed`, not an engine error until max is exhausted
- command evaluator cancelled: engine error
- agent evaluator runtime error: engine error
- agent evaluator does not call `report_outcome`: repeat step fails immediately
- `status: unsure`: repeat step fails as blocked with findings
- repair step failure: repeat step fails immediately
- max attempts exhausted: repeat step fails with latest findings

TUI behavior:

- failed repeat row should show the latest findings, truncated like other failure messages
- completed repeat row should show elapsed time
- final workflow summary should still render Markdown when the final top-level step returns Markdown

## Testing Strategy

Unit tests for `internal/workflow/flowfile.go`:

- loads a linear flow unchanged
- loads a repeat step with agent evaluator
- loads a repeat step with command evaluator
- rejects repeat with missing max
- rejects repeat with missing evaluate
- rejects repeat with empty nested steps
- rejects nested repeat
- rejects duplicate names across top-level and nested steps

Unit tests for `internal/workflow/workflow.go`:

- command evaluator passes on first attempt and skips repair steps
- command evaluator fails once, repair runs, then evaluator passes
- command evaluator fails until max and workflow fails
- agent evaluator captures `report_outcome: passed`
- agent evaluator captures `report_outcome: failed`, passes findings to fixer prompt
- agent evaluator captures `report_outcome: unsure` and blocks
- agent evaluator missing `report_outcome` fails with clear error
- downstream step runs only after repeat passes
- downstream step does not run when repeat fails

Unit tests for `internal/phase/phase.go`:

- `.Repeat` renders in evaluator and repair prompts
- `.Repeat` is nil or empty outside repeat, whichever implementation chooses and documents

TUI tests:

- repeat row shows current attempt
- latest evaluator findings appear when failed/blocked
- completed repeat row behaves like a normal completed step

Smoke example:

Add an example flow file, possibly `examples/repeat-review-flow.yml`, that uses a deterministic command evaluator first. Agent judge examples can be documented separately because they need model behavior.

Manual verification:

```bash
go test ./...
neo run examples/repeat-test-flow.yml "Exercise repeat steps"
```

## Out of Scope

- Arbitrary nested repeat steps
- Parallel steps
- Streaming model output
- Provider-specific reasoning or thinking traces
- Provider-specific structured-output APIs
- Automatic PR creation for this feature
- A general expression language for `if` conditions
- Looping on `unsure`
- Parsing natural-language judge output
- Migrating legacy named config flows from `retry_from`

