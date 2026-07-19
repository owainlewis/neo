# Neo Release SOP

Use this guide when asked to cut a stable Neo release. It is written for an
agent: focus on the desired outcome, verify each result, and ask before taking
any destructive action.

## Goal

Publish a new stable release through the single supported install path:

- GitHub Releases, with Linux/macOS archives and checksums.
- The one-line installer, which verifies and installs the latest stable GitHub
  release.

## Important context

- Stable releases are created by pushing a `v*` tag.
- The release workflow is `.github/workflows/release.yml`.
- GoReleaser config is `.goreleaser.yaml`.
- Release notes live in `CHANGELOG.md`.
- Developer docs under `docs/developer/` are maintained directly in Markdown.

## Safety rules

- Preserve unrelated working-tree changes.
- Stage and commit only release-related files.
- Never print secret values.
- Never move, delete, or recreate a pushed release tag unless the user explicitly
  asks for that recovery path.
- If anything is ambiguous, stop and ask.

## Release checklist

### 1. Confirm the release base

Start from the intended release branch, normally the latest `main`. Check the
current branch, local changes, recent commits, existing tags, and recent GitHub
releases.

If the repository is not on the expected release base, or unrelated changes are
present, either move to a clean worktree or ask the user how to proceed.

### 2. Check publishing access

Confirm GitHub CLI access is available for this repository.

### 3. Choose the version

Look at the latest stable `v*` tag and choose the next semantic version:

- Patch for release-process fixes, documentation-only release corrections, or
  small bug fixes.
- Minor for user-visible features and normal batches of improvements.
- Major only when explicitly requested.

If the version bump is not obvious, ask the user to choose.

### 4. Update the changelog

Create or update `CHANGELOG.md` with a new top entry for the chosen version and
today's date.

Summarize what changed since the previous stable release. Prefer human-readable
release notes over raw commit lists. Use short sections such as:

- Highlights
- Added
- Changed
- Fixed

Keep the changelog useful to users. Omit noisy merge commits and internal-only
churn unless it affects release behavior.

Ensure release archives include the changelog. In `.goreleaser.yaml`, the
archive file list should include `CHANGELOG.md` alongside `LICENSE` and
`README.md`.

### 5. Verify locally

Run the normal release-relevant checks before tagging:

- Go tests.
- Generated developer docs check.
- Installer shell syntax check.
- GoReleaser config check, if GoReleaser is installed locally.

If a check fails, fix it before continuing. If a local optional tool is missing,
note that and rely on CI for that specific check.

### 6. Commit release metadata

Commit the changelog and any release config updates. Use a clear release-prep
commit message, for example:

```text
chore(release): prepare vX.Y.Z
```

Before committing, review the staged diff and confirm it contains only the
intended release files.

### 7. Tag and publish

Create an annotated `vX.Y.Z` tag on the release commit and push the commit and
tag to the main repository.

The pushed tag should trigger the Release workflow automatically.

### 8. Watch the release workflow

Monitor the Release workflow until it succeeds or fails.

If it fails, inspect the failed logs and report the exact cause. If the failure
happened after a GitHub release was created, prefer fixing the problem in a new
commit and cutting a new patch release. Do not reuse or rewrite the existing tag
without explicit user approval.

### 9. Verify the published release

Confirm the GitHub release exists for the new tag and is not a prerelease.

Expected assets:

- `checksums.txt`
- `neo_darwin_amd64.tar.gz`
- `neo_darwin_arm64.tar.gz`
- `neo_linux_amd64.tar.gz`
- `neo_linux_arm64.tar.gz`

Because the installer resolves the latest stable GitHub release, a successful
latest release with those assets is enough to confirm the download install path.
Do not run remote install scripts unless the user asks for an install test.

## Final report

End with a concise release summary:

- Version released.
- GitHub release URL.
- Assets published.
- Release workflow result.
- Local checks that were run.
- Any unrelated local changes that were intentionally left untouched.
