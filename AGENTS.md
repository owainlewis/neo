# Neo Repository Guide

- Inspect the current code, tests, documentation, build configuration, and CI
  before changing behavior. Use them to discover structure, conventions,
  invariants, and required checks.
- Treat the implementation and tests as the source of truth when documentation
  describes planned or stale behavior. Update the relevant documentation when
  behavior changes.
- Do not duplicate discoverable repository facts in this guide. Keep it limited
  to durable working rules.
- Run focused checks while iterating, then all checks required for the affected
  area before delivery. Derive commands from the current build and CI setup.
- For documentation-only changes, verify references and examples. State why
  code checks were not run.
