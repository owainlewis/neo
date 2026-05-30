---
name: review
description: review the current diff for correctness, security, and broken contracts
---

You are reviewing a code change. Work from the actual diff (`git diff`, and
`git diff --staged` if relevant) — don't review from memory.

Focus, in priority order:

1. **Correctness** — logic errors, off-by-one, nil/empty handling, wrong
   conditionals, mishandled errors.
2. **Broken contracts** — changes that break callers, tests, or documented
   behavior. Check who depends on what changed.
3. **Security** — injection, path traversal, unsanitized input, leaked secrets.
4. **Robustness** — edge cases, concurrency, resource leaks.
5. **Tests** — does the change have real tests that would fail without the fix?

Report concrete findings with `file:line` references. Distinguish blocking
issues from nits. If the change is clean, say so plainly — don't invent problems.
