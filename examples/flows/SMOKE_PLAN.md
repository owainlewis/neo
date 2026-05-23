You are testing Neo's simple flow-file runner against the Neo repository.

Task:

{{ .Task }}

Run ID:

{{ .RunID }}

Read `README.md` and `docs/step-templates.md` just enough to understand the
current workflow feature. Then write a short smoke-test plan to:

`.agent/runs/{{ .RunID }}/PLAN.md`

The plan should include:

- what this smoke flow is checking
- what the next command steps will verify
- the word "smoke" somewhere in the body, so the deterministic content check
  can prove the file was created from this prompt
- any concern you noticed while reading

Do not edit tracked project files.
