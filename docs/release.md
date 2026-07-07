# Neo Release SOP

Use this guide when asked to cut a stable Neo release. It is written for an
agent: focus on the desired outcome, verify each result, and ask before taking
any destructive action.

## Goal

Publish a new stable release that is available in all supported install paths:

- GitHub Releases, with Linux/macOS archives and checksums.
- The one-line installer, which installs the latest stable GitHub release.
- Homebrew, via the `owainlewis/tap/neo` cask when the tap token is configured.

## Important context

- Stable releases are created by pushing a `v*` tag.
- The release workflow is `.github/workflows/release.yml`.
- GoReleaser config is `.goreleaser.yaml`.
- Release notes live in `CHANGELOG.md`.
- Generated docs under `docs/developer/` must not be edited by hand.
- The Homebrew tap update needs the `HOMEBREW_TAP_GITHUB_TOKEN` Actions secret.
  If the secret is missing, the GitHub release can still succeed, but Homebrew
  publishing will be skipped.

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

### 2. Check publishing prerequisites

Confirm GitHub CLI access is available for this repository.

Check whether the `HOMEBREW_TAP_GITHUB_TOKEN` secret exists by name only. Do not
show its value. If it is missing, tell the user that Homebrew publishing will be
skipped unless they add the secret before release.

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

### 10. Verify Homebrew, when enabled

If the Homebrew token was configured, confirm the tap repository was updated.
The expected result is a commit in `owainlewis/homebrew-tap` like:

```text
Brew cask update for neo vX.Y.Z
```

Also confirm `Casks/neo.rb` contains the released version without the leading
`v`, for example:

```ruby
version "X.Y.Z"
```

If the token was missing or Homebrew publishing was skipped, clearly report that
GitHub release publishing succeeded but Homebrew was not updated.

## Final report

End with a concise release summary:

- Version released.
- GitHub release URL.
- Assets published.
- Release workflow result.
- Homebrew tap result, including tap commit evidence when available.
- Local checks that were run.
- Any unrelated local changes that were intentionally left untouched.
