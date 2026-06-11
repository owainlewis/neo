# Demo run-of-show: nested subagents

Live runbook for the on-camera demo. This pipeline has NOT been
dry-run end to end. That's fine: every likely stall has a recovery
move below, and the escalation paths are content, not failure. If an
agent stops and asks for help, that is the safety model working.
Narrate it.

Everything below marked VERIFIED was checked on this machine on
2026-06-11.

---

## Pre-flight (off camera, ~10 min)

- [ ] `brew install golangci-lint` — NOT currently installed
      (VERIFIED missing). `just lint` calls it, so without it the
      check-runner reports FAIL on a clean tree and the implementer
      may burn repair attempts on an environment problem.
- [ ] `git checkout demo/nested-subagents && git status` — tree clean
      (ignore untracked `.codex-worktrees/`).
- [ ] `git push -u origin demo/nested-subagents` — the draft PR in
      Part 2 targets this branch as base.
- [ ] `gh auth status` — VERIFIED logged in as owainlewis.
- [ ] Baseline green — VERIFIED: `go build ./...` and `go test ./...`
      both pass on this branch.
- [ ] Bug confirmed real — VERIFIED: `truncate` at
      `internal/tui/helpers.go:36` slices bytes (`s[:n-1]`).
- [ ] Fresh Claude Code session, run `/agents` — confirm all SEVEN
      project agents are listed: code-implementer, check-runner,
      independent-reviewer, security-reviewer, correctness-reviewer,
      test-runner, failure-analyst. (The script says "six files" in
      several places; the actual count is seven. Fix the script.)
- [ ] Decide permission mode. Auto-accept edits keeps the run flowing
      on camera; default mode gives you natural pause points to
      narrate. Pick one before recording, not during.
- [ ] Note your `/usage` numbers BEFORE recording (screenshot).

---

## Part 0 — Show the bug (30 seconds, optional cold open)

Proves the issue is real before any agent touches it.

```bash
go run /tmp/trunc.go
```

If `/tmp/trunc.go` is gone, recreate it: a `main` that calls the
`truncate` implementation copied from `internal/tui/helpers.go` with
`truncate("日本語のセッション", 8)` and prints the result plus
`utf8.ValidString`.

VERIFIED output:

```
output: "日本\xe8…"
valid UTF-8: false
```

Line to camera: "That's the bug an agent tree is about to fix,
review, test, and ship while I watch."

---

## Part 1 — Depth probe (~3 min)

1. Fresh session.
2. Paste the prompt from `demo/prompt-1-depth-probe.md`.
3. Expected: "alive at depth 1" ... up the chain. Since the depth-5
   cap was lifted, the likely outcome is the chain reaching our
   self-imposed stop at depth 8.
4. Run `/usage`. Screenshot. Read the cost of doing literally nothing
   useful, eight levels deep.

| If this happens | Do this |
|---|---|
| A spawn is blocked before depth 8 | Jackpot. Capture the error verbatim, that's the platform limit on record |
| Model refuses or flattens (spawns several at one level) | Re-paste, add "spawn exactly ONE subagent, sequentially, never in parallel" |
| Levels get slow past depth 4-5 | Let it run, you're cutting this in edit anyway |

---

## Part 2 — Issue #114 pipeline (~15-25 min, the main event)

1. Fresh session (clean context is the whole point, say so).
2. Optionally show the issue: `gh issue view 114`.
3. Paste the prompt from `demo/prompt-2-issue-to-pr.md`.

### Checkpoints, in order

| # | What you should see | Narration beat |
|---|---|---|
| 1 | Coordinator runs `gh issue view 114`, branches to `fix/114-unicode-truncate`, writes a brief | "A static workflow can't write its own brief" |
| 2 | code-implementer spawns, edits `internal/tui/helpers.go`, adds tests | One bounded change, smallest diff |
| 3 | Implementer spawns check-runner — a subagent spawning a subagent | THE feature moment. Point at the screen |
| 4 | check-runner reports exit codes, not opinions | "Agents decide, scripts verify" |
| 5 | Coordinator spawns independent-reviewer and test-runner in parallel | Point at "Do not share the implementer's notes" in the prompt |
| 6 | Reviewer spawns correctness-reviewer only, no security-reviewer | Selective spawning: a UTF-8 fix has no auth surface |
| 7 | Arbitration: findings go back to the implementer, max 2 loops | Loops with exits are engineering |
| 8 | Draft PR opens against `demo/nested-subagents` | The human gate. The coordinator can't merge |

### Recovery moves

| If this happens | Do this |
|---|---|
| Coordinator starts editing code itself | Interrupt (Esc), say: "You must not edit code. Delegate to the code-implementer subagent" |
| check-runner FAILs on missing golangci-lint | You skipped pre-flight. Tell the coordinator: "golangci-lint failures are environmental, judge lint by `go vet` only" |
| Reviewer never gets spawned, coordinator reviews inline | Interrupt: "Spawn the independent-reviewer subagent with only the issue and the diff" |
| Reviewer asks for the implementer's reasoning | Refuse on camera. This is the self-review trap segment writing itself |
| Repair loop hits the cap and escalates to you | Best possible footage. The system stopped instead of thrashing. Read the escalation summary out loud |
| PR opens against the wrong base | After the run: `gh pr edit <n> --base demo/nested-subagents` |
| Total stall, nothing recoverable | Cut. Reset (below). Take 2 knows where the potholes are |

4. The moment the PR opens: `/usage`. Screenshot. Read the number out
   loud, compare with the pre-recording screenshot. If it's ugly, say
   it's ugly.

---

## Reset between takes

```bash
gh pr close <PR-NUMBER> --delete-branch   # if a draft PR was opened
git checkout demo/nested-subagents
git branch -D fix/114-unicode-truncate    # if it survived locally
git status                                 # confirm clean
```

Then a fresh Claude Code session. Issue #114 stays open until you
merge a take you like.

---

## Success criteria for the take

- Nested spawn visible on screen at least once (checkpoint 3)
- Independent review with issue + diff only (checkpoint 5)
- A draft PR you would actually merge: Unicode-safe `truncate`, new
  tests for multi-byte text and tiny widths, `go test ./internal/tui`
  green
- A `/usage` number, whatever it is
