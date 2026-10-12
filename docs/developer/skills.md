# Skills

A skill is a focused prompt for one interactive turn, stored as a `SKILL.md`
file. Neo ships with four built-in skills and discovers more from the user's
home directory and the project.

```text
/review internal/tui        invoke a skill with arguments
Fix this, then $review it   pull a skill's body into an ordinary message
```

Ordinary prose never activates a skill. `Run the review skill` is sent to the
model unchanged.

## Built-in skills

| Skill | Purpose |
| --- | --- |
| `design` | Ground a proposed product change, feature, or bug fix in the current system and define acceptance criteria. |
| `plan` | Break accepted work into small, ordered tasks with checks. |
| `build` | Implement, test, self-review, simplify, and verify a complete change. |
| `review` | Review code, PR feedback, or CI results with fresh context and report prioritized findings. It does not change code unless asked. |

The built-ins are embedded from `internal/skills/defaults/<name>.md` and are
always available, even when `features.skills` is off.

## Discovery and override

```text
~/.neo/skills/<name>/SKILL.md            user-global
<repo-or-cwd>/.neo/skills/<name>/SKILL.md   project
```

Later sources win by name: a project skill replaces a global one, and either
replaces a built-in of the same name. To change how `/review` behaves in one
repository, create `.neo/skills/review/SKILL.md`. Built-ins keep their order
at the front of the slash picker; discovered skills follow, sorted by name.

Symlinked skill directories are followed, including user-global links to a
separate dotfiles directory. Files with read or YAML errors are skipped with
path-specific warnings; valid skills and lower-priority fallbacks remain usable.
Each discovered `SKILL.md`, including frontmatter, is limited to 32 KiB; larger
files are skipped with a warning instead of injecting partial instructions.

A `SKILL.md` has optional YAML frontmatter (LF or CRLF) and a markdown body:

```markdown
---
name: security
description: Review authentication and trust boundaries
---
Inspect the requested security boundary.
Report actionable findings with file references.
```

`name` falls back to the directory name. Names are lowercased. A skill named
after a native command (`help`, `clear`, `model`, `quit`, `exit`) is still
usable through `$name` but cannot be invoked with a slash.

## What the model sees

The system prompt advertises names and descriptions only. A skill body is sent
only when it is invoked: `/name args` expands to the body plus an `Arguments`
section, and `$name` prefixes the message with the body. Headless `neo run`
does not advertise or expand skills.

The TUI shows the invoked skill's name as the turn label ahead of workflow and
tool activity and in the completion receipt. The saved message keeps a separate
display value, so resume, session titles, and transcript search show the user's
`/review` invocation instead of the expanded body.

## Boundaries

`internal/skills` owns discovery, the embedded defaults, override precedence,
the catalog section, and invocation expansion. The TUI owns slash routing and
the turn label. The core agent loop does not know skills exist.
