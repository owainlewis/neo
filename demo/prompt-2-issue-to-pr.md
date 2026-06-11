# Prompt 2 — Issue-to-PR pipeline

Paste into the main session. Requires the six agents in `.claude/agents/`.

For the demo on this repo, use issue #114 (Fix Unicode-safe truncation
and TUI text width helpers). It is small, low risk, labelled
`agent:ready`, and a genuine bug, so the correctness-reviewer and
test-runner have something real to do.

```text
You are coordinating an issue-to-PR pipeline. You manage the work —
you never edit code yourself. Use the project subagents by name.

1. BRIEF — Fetch GitHub issue #114 with `gh issue view 114`. Create a
   working branch `fix/114-unicode-truncate` off the current branch.
   Write a short implementation brief: the goal, the likely files
   involved, and what is explicitly out of scope.

2. IMPLEMENT — Spawn the code-implementer subagent with the brief.
   It must run its checks via the check-runner before reporting back.

3. VERIFY (run these as parallel subagents once the diff is ready):
   a. Spawn the independent-reviewer subagent. Brief it with ONLY the
      original issue and the diff. Do not share the implementer's
      notes or reasoning.
   b. Spawn the test-runner subagent to run the suite.

4. ARBITRATE — If the reviewer or test-runner reports any CRITICAL
   finding, send the finding (with evidence) back to the
   code-implementer subagent for a fix, then re-verify. Maximum 2
   repair loops. If still failing, stop and escalate to me with a
   summary of what's blocked.

5. SHIP — When review and tests are clean, open a DRAFT pull request
   with `gh pr create --draft --base demo/nested-subagents`. The PR
   description must include: what
   changed, the review findings (including resolved ones), and the
   test results. Do not merge anything. Merging is my job.

Hard rules: if anything touches auth, billing, permissions,
deployments, or migrations, stop and ask me before proceeding.
Report your token-relevant activity at the end so we can check
/usage against it.
```

## Beats to call out during the run

- **The brief step** — the coordinator turning a vague issue into a
  bounded brief is the moment "agents coordinate the runtime" becomes
  visible. A static workflow can't write its own brief.
- **The implementer's inner loop** — check-runner fails, implementer
  fixes, re-runs. The thing deterministic pipelines can't do.
- **The independence moment** — point at "Do not share the
  implementer's notes." A reviewer briefed by the thing it's auditing
  inherits its assumptions.
- **Selective spawning** — the reviewer only pulls in the specialists
  the diff warrants.
- **The repair loop cap** — loops with exits are engineering; loops
  without exits are a token bill.
- **The draft PR** — the human gate, enforced structurally. The
  coordinator can't merge.

## After the run

Run `/usage` immediately after the PR opens and read the number out
loud. The goal was never cost per token; it's cost per reliable change.
