# Code quality audit: bugs and outdated docs

Your goal is to find real, material problems in this repository — logical
bugs, security vulnerabilities, and documentation that no longer matches the
code — and open one GitHub issue per problem so it can be triaged and
implemented. Do not change code or documentation, or open a pull request, in
this workflow.

Only open an issue for something a maintainer would actually want fixed. Do
not open issues for style preferences, minor nits, hypothetical edge cases
with no real trigger, or documentation wording you merely find awkward.
Finding nothing real is a valid and common outcome. Never create an issue
just to have something to show for the run.

## Inspect recent and risky code

Use authenticated `git` and `gh` commands to inspect the repository, recent
changes, open issues, and open pull requests. Read repository instructions
before investigating.

Prioritize code that recently changed, handles errors or untrusted input,
crosses process or persistence boundaries, manages concurrency or cleanup, or
has weak test coverage. Trace real execution paths and compare behavior with
tests, documentation, and call-site expectations. Run focused tests or small
reproductions when practical.

Treat security vulnerabilities (injection, unsafe deserialization, path
traversal, secrets in logs or code, missing authz checks, unsafe use of
untrusted input) as a category of bug in scope here, not a separate concern.

## Compare docs against code

Use `git` and `gh` to inspect `README.md`, `AGENTS.md`, files under `docs/`,
and other checked-in documentation. Look for documentation that:

- describes commands, flags, config keys, or file paths that no longer exist
  or have changed;
- references removed or renamed features, packages, or environment
  variables;
- contains setup or usage instructions that no longer work as written;
- links to files, sections, or URLs that are broken or moved;
- contradicts current behavior in the code it describes.

Verify each finding against the current code, not against assumptions. Run
documented commands where practical to confirm they fail or behave
differently than described.

## Prove each problem before reporting it

A useful finding must include:

- the observable incorrect behavior or documentation gap;
- the exact code path, conditions, or file and line involved;
- why it is actually wrong, not a style preference;
- a focused reproduction or other strong evidence;
- the expected behavior or correct documentation;
- a practical verification approach for whoever implements the fix.

Do not report speculative risks, broad code-quality concerns, missing
features, or duplicates. Search open and closed issues and pull requests
before creating anything. If an existing item covers the same root cause,
leave it unchanged and continue searching.

## Create issues

For each real, new, material problem, create one GitHub issue with a concise
title, evidence, reproduction steps (for bugs) or the outdated claim and
correct current behavior (for docs), expected outcome, bounded acceptance
criteria, and a verification plan. Apply an existing bug or documentation
label when the repository has one, but do not create new labels. Do not add
`factory:ready-for-spec` or `factory:ready-to-implement` — a human decides
which findings are worth pursuing and moves the ticket into the pipeline.

If no defensible problem is found, make no external changes. Finish with a
concise summary of the areas inspected, checks run, and why no issue was
created.
