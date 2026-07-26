# Code quality audit: bugs and outdated docs

Find real bugs, security vulnerabilities, and documentation that contradicts the code in this repository. Open one GitHub issue per problem. Do not change code or docs, and do not open pull requests.

High bar: only report what a maintainer would actually fix. No style nits, no hypothetical edge cases, no missing features, no awkward wording. Finding nothing is a normal outcome. Never invent a finding to fill the run. Cap: 5 issues per run, highest severity first.

## Investigate

Use authenticated `git` and `gh`. Read repository instructions (`AGENTS.md`, `CONTRIBUTING.md`) first, then recent commits and open and closed issues and PRs.

Prioritize: recently changed code, error handling, untrusted input, process and persistence boundaries, concurrency and cleanup, weak test coverage.

Trace real execution paths. Compare behavior against tests, docs, and call sites. Run focused tests or minimal reproductions where practical.

Security issues count as bugs here: injection, unsafe deserialization, path traversal, secrets in code or logs, missing authz, unvalidated input.

For docs (`README.md`, `AGENTS.md`, `docs/`, other checked-in files), look for: commands, flags, config keys, env vars, or paths that no longer exist; removed or renamed features; setup steps that no longer work; broken links; claims that contradict current behavior. Verify against current code, and run documented commands where practical.

## Report

Skip anything already covered by an open or closed issue or PR, including your own prior runs. One root cause equals one issue, even if it spans several files.

Each issue must contain, and is only worth filing if you can supply:

- concise title;
- observable wrong behavior or false doc claim;
- exact file, line, and triggering conditions;
- why it is wrong, not a preference;
- reproduction steps or equivalent hard evidence;
- correct expected behavior or correct documentation;
- bounded acceptance criteria and how the fixer verifies.

Apply an existing bug or documentation label if one exists. Do not create labels. Do not apply `factory:ready-for-spec` or `factory:ready-to-implement`; a human triages.

If nothing qualifies, make no external changes and end with a short summary of areas inspected, checks run, and why nothing was filed.
