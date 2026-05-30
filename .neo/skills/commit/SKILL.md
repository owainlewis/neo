---
name: commit
description: stage the intended changes and write one clear conventional commit
---

Create a single, well-scoped commit for the current work.

1. Review what changed (`git status`, `git diff`) and stage the files that
   belong in this commit — nothing unrelated.
2. Write a Conventional Commits message: `type(scope): summary` where type is
   one of feat, fix, refactor, docs, test, chore. Keep the summary imperative
   and under ~70 characters.
3. Add a short body only when the "why" isn't obvious from the summary.

Do not push unless asked. Do not commit secrets, build artifacts, or unrelated
changes.
