# Step prompts as templates

Each step prompt (`flows/<name>.md`) is rendered as a
[Go `text/template`](https://pkg.go.dev/text/template) immediately before
it's sent to the model as the system prompt. A prompt with no `{{ }}`
markers renders unchanged — templates are opt-in.

## Available variables

| Variable | Type | Description |
|---|---|---|
| `.Task` | `string` | The original workflow task. Also delivered separately as the user message; mostly useful when you want to inline the task inside the system prompt. |
| `.Round` | `int` | 1-based current round number. Increments on `retry_from`. |
| `.Prev` | `*StepRef` or `nil` | The step that completed immediately before this one. `nil` on the very first executed step of the workflow. |
| `.Prev.Name` | `string` | Step name (e.g. `"review"`). |
| `.Prev.Output` | `string` | The final agent message from that step. |
| `.Prev.Round` | `int` | Which round produced it. |
| `.Steps` | `map[string]*StepRef` | Most recent output of each named step, keyed by step name. Use `.Steps.write.Output`, etc. |

## Common patterns

### Address prior-step feedback when present

```markdown
{{if .Prev}}
You are addressing feedback from the previous step:

{{.Prev.Output}}

Treat each point as a concrete requirement.
{{else}}
First attempt. Implement the task described in the user message.
{{end}}
```

### Swap entire role between rounds

```markdown
{{if .Prev}}
You are a refiner. Tighten the work below:

{{.Prev.Output}}
{{else}}
You are a drafter. Produce a first pass.
{{end}}
```

### Cross-reference a specific earlier step

```markdown
The plan to follow:

{{.Steps.plan.Output}}

Stick to it. If you need to deviate, say so in your summary.
```

### Branch on round number

```markdown
{{if eq .Round 1}}
You are doing the initial implementation.
{{else}}
This is round {{.Round}}. The previous round failed review.
Be especially careful about edge cases.
{{end}}
```

## Errors

Template parse or execution errors fail the step at start with the
source path inline:

```
step "review" (./flows/review.md): template parse: template: review:3:
unexpected EOF
```

References to fields that don't exist (e.g. `.Status` on `.Prev`) render
as the literal string `<no value>` rather than failing — Go's
`text/template` default. To get an explicit error instead, use
`{{.Prev.Status}}` only inside an `{{if}}` block that you know guards it.

## Escaping

If you need a literal `{{` in your prompt (rare), use `{{"{{"}}`.

## Funcmap

Currently the funcmap is the `text/template` built-ins only
(`if`, `range`, `eq`, `ne`, `len`, `printf`, `index`, etc.). Custom
helpers (e.g. `truncate`) will land when a real need arises.
