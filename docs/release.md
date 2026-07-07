# Release SOP for Neo

This document is an agent-friendly standard operating procedure for cutting a Neo
stable release. Read it fully before acting. Follow the steps in order and stop
on any ambiguity or failed verification.

## Purpose

Publish a new stable Neo release that is available from:

- GitHub Releases with checksums and Linux/macOS archives.
- The one-line installer, which resolves the latest stable GitHub release.
- Homebrew cask `owainlewis/tap/neo`, when the Homebrew tap token is configured.

## Release machinery

- Stable releases are triggered by pushing a `v*` git tag.
- Workflow: `.github/workflows/release.yml`.
- Release builder/config: `.goreleaser.yaml`.
- Changelog: `CHANGELOG.md`.
- Installer: `install.sh`.
- Homebrew tap: `owainlewis/homebrew-tap`, cask `Casks/neo.rb`.
- Required Actions secret for Homebrew publishing:
  `HOMEBREW_TAP_GITHUB_TOKEN` in repo `owainlewis/neo`.

GoReleaser publishes the GitHub release. It updates the Homebrew tap only when
`HOMEBREW_TAP_GITHUB_TOKEN` is present; otherwise `.goreleaser.yaml` skips the
Homebrew upload so the GitHub release can still complete.

## Agent constraints

- Do not edit generated files under `docs/developer/` by hand.
- Preserve unrelated working-tree changes. Stage only release-intended files.
- Do not print secret values. It is OK to list secret names.
- Do not delete or overwrite existing release tags unless explicitly instructed.
- If a release workflow fails after creating a GitHub release, do not rerun the
  same tag blindly. Diagnose first; prefer a patch tag if a new commit is needed.

## Preflight

1. Read required repository guidance:

   ```bash
   sed -n '1,160p' docs/developer/index.md
   ```

2. Inspect repository state:

   ```bash
   git status --short --branch
   git log --oneline -5
   git tag --list 'v*' --sort=-v:refname | head -10
   gh release list --repo owainlewis/neo --limit 10
   ```

3. Confirm you are working from the intended release base. Normally release from
   the current `main`/`origin/main` tip. If the current branch is not `main`,
   confirm whether the user expects this branch to be released.

4. Check whether the Homebrew secret exists, without printing its value:

   ```bash
   gh secret list --repo owainlewis/neo | grep '^HOMEBREW_TAP_GITHUB_TOKEN\b' || true
   ```

   If missing, tell the user Homebrew publishing will be skipped. GitHub release
   publishing can still proceed.

## Choose the next version

1. Find the latest stable semver tag:

   ```bash
   git tag --list 'v*' --sort=-v:refname | head -1
   ```

2. Choose the next version:
   - Patch, e.g. `v0.2.2` -> `v0.2.3`, for release automation fixes, docs-only
     release notes corrections, or small fixes.
   - Minor, e.g. `v0.2.2` -> `v0.3.0`, for user-visible features since the last
     stable release.
   - Major only with explicit user instruction.

3. If unsure, ask the user before editing or tagging.

## Update `CHANGELOG.md`

If `CHANGELOG.md` does not exist, create it. Add a new section at the top:

```markdown
## [vX.Y.Z] - YYYY-MM-DD
```

Use concise categories where applicable:

- `### Highlights`
- `### Added`
- `### Changed`
- `### Fixed`

Generate candidate notes from commit subjects since the previous stable tag:

```bash
git log <previous-tag>..HEAD --pretty=format:'%s' --reverse
```

Write human-readable notes. Do not dump raw merge commits. Include compare links
at the bottom:

```markdown
[vX.Y.Z]: https://github.com/owainlewis/neo/compare/<previous-tag>...vX.Y.Z
```

Ensure `.goreleaser.yaml` includes `CHANGELOG.md` in archive files. It should
contain:

```yaml
archives:
  - id: neo
    files:
      - LICENSE
      - README.md
      - CHANGELOG.md
```

## Local verification

Run these checks before committing/tagging:

```bash
go test ./...
go run ./cmd/neo-docs --check
bash -n install.sh
```

If GoReleaser is installed locally, also run:

```bash
goreleaser check
```

If `goreleaser` is not installed, note that the local GoReleaser check was
skipped and rely on the GitHub Actions release workflow.

## Commit release metadata

Stage only intended release files, usually:

```bash
git add CHANGELOG.md .goreleaser.yaml
```

If `.goreleaser.yaml` was not changed, stage only `CHANGELOG.md`.

Review staged diff:

```bash
git diff --cached
```

Commit:

```bash
git commit -m "chore(release): prepare vX.Y.Z"
```

If there is nothing to commit because the changelog was already prepared, do not
create an empty commit unless the user explicitly asks.

## Tag and push

Create an annotated tag:

```bash
git tag -a vX.Y.Z -m "vX.Y.Z"
```

Push the commit, then the tag:

```bash
git push origin HEAD:main
git push origin vX.Y.Z
```

If releasing from a branch intentionally, replace `main` only with explicit user
approval. The release workflow triggers from the pushed tag.

## Watch the release workflow

Find and watch the run:

```bash
gh run list --repo owainlewis/neo --workflow release.yml --limit 5
gh run watch <run-id> --repo owainlewis/neo --exit-status
```

If the run fails, inspect logs:

```bash
gh run view <run-id> --repo owainlewis/neo --log-failed
```

Common failure: Homebrew token problems. If GitHub release publishing succeeded
but Homebrew failed, fix the config/secret, then cut a new patch release. Do not
reuse the already-pushed tag unless the user explicitly approves destructive tag
movement.

## Post-release verification

Verify the GitHub release:

```bash
gh release view vX.Y.Z --repo owainlewis/neo \
  --json tagName,url,assets,publishedAt,isPrerelease,targetCommitish \
  --jq '{tagName,publishedAt,isPrerelease,targetCommitish,url,assets:[.assets[].name]}'
```

Expected assets:

- `checksums.txt`
- `neo_darwin_amd64.tar.gz`
- `neo_darwin_arm64.tar.gz`
- `neo_linux_amd64.tar.gz`
- `neo_linux_arm64.tar.gz`

Verify the installer resolves the latest release by checking the release exists.
Do not pipe remote install scripts into bash as a test unless the user explicitly
asks for an install test.

If the Homebrew token is configured, verify the tap update:

```bash
gh api repos/owainlewis/homebrew-tap/commits/main \
  --jq '{sha:.sha[0:7], message:.commit.message, authorDate:.commit.author.date}'

gh api repos/owainlewis/homebrew-tap/contents/Casks/neo.rb?ref=main \
  --jq .content | base64 --decode | sed -n '1,80p'
```

Expected Homebrew evidence:

- Latest tap commit message: `Brew cask update for neo vX.Y.Z`.
- `Casks/neo.rb` contains `version "X.Y.Z"`.
- URLs point at `https://github.com/owainlewis/neo/releases/download/v#{version}/...`.

Optionally, if Homebrew is available and the user approves an install/update
check:

```bash
brew update
brew info --cask owainlewis/tap/neo
```

## Final response template

Report:

- Version released.
- GitHub release URL.
- Assets published.
- Whether the release workflow passed.
- Whether Homebrew tap was updated, with tap commit evidence.
- Checks run locally.
- Any unrelated working-tree changes left untouched.

Example:

```text
Released vX.Y.Z: https://github.com/owainlewis/neo/releases/tag/vX.Y.Z

Published assets: checksums.txt, neo_darwin_amd64.tar.gz, ...
Release workflow: passed.
Homebrew tap: updated at owainlewis/homebrew-tap <short-sha>, commit
"Brew cask update for neo vX.Y.Z"; Casks/neo.rb version is "X.Y.Z".

Local checks: go test ./..., go run ./cmd/neo-docs --check, bash -n install.sh,
goreleaser check.
```
