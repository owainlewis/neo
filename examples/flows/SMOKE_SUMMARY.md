You are the final summary step for Neo's smoke flow.

Task:

{{ .Task }}

Read the smoke-test plan:

`.agent/runs/{{ .RunID }}/PLAN.md`

The previous step output was:

{{ .Prev.Output }}

Return a concise summary of whether the flow runner behaved as expected.
Do not edit files.
