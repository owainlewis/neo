# Named Phases

Named phases are focused prompts for one interactive turn. Neo ships with
`design`, `plan`, `build`, and `review`; users can invoke them with slash
commands or explicitly ask Neo to run named phases:

```text
/review current branch
Run design and plan phases for encrypted sessions.
```

The phase prompt is sent to the model with the user's arguments. The TUI shows
the phase name before normal workflow and tool activity, then leaves a concise
completion receipt. Phase state is not persisted and does not enforce an order
or replace the generic workflow checklist.

## Defaults

| Phase | Purpose |
| --- | --- |
| `design` | Ground a proposed product change, feature, or bug fix in the current system and define acceptance criteria. |
| `plan` | Break accepted work into small, ordered tasks with checks. |
| `build` | Implement, test, self-review, simplify, and verify a complete change. |
| `review` | Review and improve code, PR feedback, and CI results with fresh context. |

## Configuration

The `phases` map in `neo.yaml` adds a named prompt or overrides a default by
name. Names use lowercase letters, numbers, hyphens, or underscores. Native
commands such as `help`, `clear`, and `model` are reserved.

```yaml
phases:
  security:
    description: Review authentication and trust boundaries
    prompt: |
      Inspect the requested security boundary.
      Report and fix actionable findings, then rerun relevant checks.

  review:
    prompt: |
      Apply this project's review policy to the requested scope.
```

Configured fields overlay the matching default, so overriding only `prompt`
retains the built-in description. Additional phases appear after the four
defaults in the slash picker.

Named-phase slash commands take precedence over skills with the same name. A
same-named skill remains available through its `$name` reference.

## Boundaries

`internal/phase` owns definitions, default prompts, config overlay, explicit
natural-language matching, prompt expansion, and display labels. The TUI owns
the active turn label. `internal/workflow` remains the only visible checklist
model. The core agent loop does not interpret phase names or transitions.

Full prompt bodies are injected only when invoked. The saved message keeps a
separate display value, so resume, session titles, and transcript search show
the user's `/review` invocation instead of the expanded prompt body.
