# Nested subagents demo

Resources for the nested subagents video (CID: c8). Pattern:
coordinator → workers → sub-workers, three levels.

## Layout

- `.claude/agents/` — six project-level agents:
  - Level 2 (have Task, can delegate): `code-implementer`,
    `independent-reviewer`, `test-runner`
  - Level 3 (leaves, no Task tool): `check-runner`,
    `security-reviewer`, `correctness-reviewer`, `failure-analyst`
- `demo/prompt-1-depth-probe.md` — verify nesting depth empirically
- `demo/prompt-2-issue-to-pr.md` — the full issue-to-PR pipeline
  (uses issue #114 on this repo)

## Design rules

1. **Leaves can't spawn.** Level-3 agents do not get the Task tool.
   The depth limit is enforced by you, in config.
2. **Review is independent.** The reviewer is spawned by the
   coordinator, briefed with the issue and the diff only, never the
   implementer's notes.
3. **Agents decide, scripts verify.** Pass/fail is an exit code, not
   an opinion.

## Run order

1. Fresh session → paste Prompt 1, capture the boundary, run `/usage`.
2. Fresh session → paste Prompt 2, narrate the beats, capture the
   draft PR, run `/usage`.
