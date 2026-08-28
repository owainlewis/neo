# Workflow

Neo uses [GitHub Issues](https://github.com/owainlewis/neo/issues) as the source
of truth for planned work. The Neo project records where each issue is in its
lifecycle.

## Statuses

Every active issue has one project status.

| Status | Description |
| --- | --- |
| Todo | Work we intend to do but have not prioritised or refined. |
| Ready | An agent can work on it. |
| In Progress | An agent is actively working on it. |
| Blocked | Work cannot continue until a dependency, decision, or human input is resolved. |
| Review | The work is complete and being reviewed. |
| Done | The work is delivered and the issue is closed. |

Move work to Ready only when the outcome, scope, acceptance criteria, and checks
are clear. Move it to In Progress when work starts, Review when the change is
ready for review, and Done after it is delivered. Move work to Blocked whenever
it cannot progress, then to Ready or In Progress when it can resume.

## Labels

Labels identify the kind of work or who owns the next action. These are the only
standard labels.

| Label | Description |
| --- | --- |
| `needs-agent` | The next action belongs to an agent and normally accompanies Ready. |
| `needs-human` | Human review or input is needed before an agent can continue. |
| `bug` | Existing behaviour is broken or unsafe. |
| `enhancement` | New or improved product or engineering behaviour. |
| `documentation` | A documentation-only change. |

Use either `needs-agent` or `needs-human`, never both. Use at most one of `bug`,
`enhancement`, or `documentation`. Status remains the source of truth for the
issue lifecycle.
