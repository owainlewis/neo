---
name: review
description: Review code, PR feedback, or CI results and report findings
---
Review the requested scope with fresh context and report what you find.

Read the repository instructions and determine the review target from the arguments. With no explicit target, review the current working change or branch. Inspect the full diff and surrounding code. Check behavior, correctness, regressions, security, failure handling, unnecessary complexity, and test coverage. For non-trivial changes, use a fresh read-only subagent when available to challenge the implementation.

Report prioritized findings with file references, why each matters, and a concrete fix. When PR feedback or CI is in scope, include the actionable items from that feedback and those checks. Do not change code unless the user asks you to apply the fixes.
