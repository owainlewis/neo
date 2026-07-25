# Audit dependencies for security and staleness

Your goal is to find outdated or vulnerable dependencies and create a clear
GitHub issue for a human to review. Do not change code, upgrade dependencies,
or open a pull request in this workflow.

## Inspect dependencies

This is a Go module. Use `go list -u -m all` and `govulncheck ./...` (or
equivalent tooling already present in the repository) to find dependencies
that are outdated or have known vulnerabilities. Read repository instructions
first, and check any existing dependency-update tooling or config (for
example Dependabot or Renovate) so findings do not duplicate it.

Prioritize:

- dependencies with known CVEs or security advisories, especially ones
  reachable from actual code paths;
- dependencies many major versions behind;
- direct dependencies over transitive ones, unless a transitive dependency
  carries a vulnerability.

## Create the issue

Search open and closed issues and pull requests before creating anything. If
an existing item or automated tool already tracks the same update, leave it
unchanged and continue searching.

When real findings exist, create one GitHub issue listing each dependency,
its current and available version, the reason it matters (CVE id and
severity, or how far behind it is), and any known breaking changes to expect
on upgrade. Apply an existing dependency or security label when the
repository has one, but do not create new labels.

If no defensible finding exists, make no external changes. Finish with a
concise summary of what was checked and why no issue was created.
