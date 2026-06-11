# Prompt 1 — Depth probe

Paste into a fresh Claude Code session.

Note: early builds capped nesting at depth 5. The latest version has no
hard cap, so this prompt carries its own stop condition (depth 8). Without
it, the chain recurses until you run out of patience or tokens.

```text
I want to test the nested subagent depth limit empirically.

Spawn a subagent with this exact instruction, passing DEPTH=1:

"You are at depth DEPTH. Report 'alive at depth DEPTH'. Then, ONLY if
DEPTH is less than 8, attempt to spawn one subagent with this same
instruction, passing DEPTH+1. If DEPTH is 8 or more, stop and report
'reached safety cap at depth 8'. If spawning fails or is blocked,
report exactly what error or limitation you observed, then stop."

Afterwards, report back: the maximum depth reached, and whether the
chain stopped because of a platform limit or because of our safety
cap. Do not do anything else.
```

## What to narrate on camera

- Each level reports in: alive at depth 1, 2, 3...
- Watch where the chain stops. If it reaches depth 8 and stops at our
  safety cap, the platform limit is gone (or higher than 8). If a spawn
  is blocked earlier, capture that error verbatim.
- Run `/usage` right after. Even this trivial chain has a cost. Plant
  that seed now and come back to it after the real demo.
