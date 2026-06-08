# Tickets

Detailed, actionable local tickets for Neo. Higher-level themes live in
`ROADMAP.md`; this file holds curated slices that are useful to keep near the
codebase.

Maintenance note: GitHub Issues are the source of truth for live issue status
and backlog planning. When a GitHub Issue exists, treat this file as a local
planning aid only; update or remove entries here when they drift from the issue
tracker or the live code.

Status legend: `todo` · `in-progress` · `done`

---

## Active next tickets

These are small local slices that still look actionable from the current code.
Before starting any of them, check the matching GitHub Issue when one is listed.

### NEO-1 — `neo update`: self-update to the latest release

**Status:** todo

**Summary.** Add a `neo update` command that checks GitHub Releases for a newer
version and updates the installed binary in place, mirroring what `install.sh`
does but from within the running binary.

**Scope.**
- New subcommand `neo update` and CLI help entry.
- Query GitHub Releases for the latest tag and compare against the stamped
  binary version.
- Download the matching OS/arch release archive, verify checksums, and
  atomically replace the current executable.
- Support `--check` and `--version <vX.Y.Z>`.

**Acceptance criteria.**
- `neo update --check` reports current vs. latest and exits 0 without modifying
  anything.
- `neo update` replaces an out-of-date binary and leaves an up-to-date install
  untouched.
- Checksum mismatch aborts without replacing the existing binary.

**Verify.**
- `go test ./cmd/neo ./internal/...`
- `go test ./...`

---

### NEO-6 — Session transcript search

**Status:** todo

**Summary.** Add CLI/API search over saved session transcripts. The TUI session
browser already has in-view filtering; this ticket is for reusable search over
persisted session JSON.

**Scope.**
- Add a session search API in `internal/session`.
- Search saved session JSON text for a plain query.
- Return session id, updated time, title, cwd, and a short matching snippet.
- Add a CLI surface, likely `neo sessions search <query>`.

**Acceptance criteria.**
- Searching finds matches in user and assistant text.
- Results are newest-first and include enough context to choose a session.
- Empty results produce a clear message.
- Search does not mutate session files.

**Verify.**
- `go test ./internal/session ./cmd/neo`
- `go test ./...`

---

### NEO-7 — Experimental memory stub

**Status:** todo

**Related:** GitHub issue #96 tracks the larger memory direction.

**Summary.** Add an off-by-default memory feature flag and a minimal `/memory`
surface before implementing real project memory.

**Scope.**
- Add `features.memory` to config, defaulting to false.
- Add `/memory` in the TUI.
- When disabled, show how to enable it.
- When enabled, show a placeholder explaining planned project memory and session
  search behavior.
- Do not add memory writes yet.

**Acceptance criteria.**
- Memory is off by default.
- `/memory` gives a useful disabled message by default.
- `features.memory: true` changes the message without affecting the agent loop.
- Developer docs and teaching guides mention the flag via `cmd/neo-docs`.

**Verify.**
- `go test ./internal/config ./internal/tui ./cmd/neo-docs`
- `go run ./cmd/neo-docs --check`
- `go test ./...`

---

### NEO-8 — Improve `/tools` display

**Status:** todo

**Summary.** Make `/tools` easier to scan by grouping tools and showing how the
current permission mode affects each group.

**Scope.**
- Group tools into read/search, shell, and mutation categories.
- Show whether each category runs, asks, or is denied under the current
  permission mode.
- Keep tool schemas in generated docs; the TUI should stay compact.

**Acceptance criteria.**
- `/tools` is readable at normal terminal widths.
- Permission-mode hints update when the active mode changes.
- Tests cover `ask`, `trusted`, and `readonly` rendering.

**Verify.**
- `go test ./internal/tui ./internal/permission`
- `go test ./...`

---

### NEO-9 — Teaching guide polish pass

**Status:** todo

**Summary.** Refine the generated teaching guides so they read like practical
"explain it simply" docs rather than reference pages.

**Scope.**
- Add one tiny concrete example to each guide.
- Add Mermaid diagrams where they clarify the idea.
- Keep each page short enough to read quickly.
- Preserve generated-doc workflow by editing only `cmd/neo-docs`.

**Acceptance criteria.**
- Each guide has a simple example section.
- The guides remain generated and linked from `docs/developer/index.md`.
- `go run ./cmd/neo-docs --check` passes.

**Verify.**
- `go test ./cmd/neo-docs`
- `go run ./cmd/neo-docs --check`

---

### NEO-15 — Git context injection

**Status:** todo

**Related:** GitHub issue #63.

**Summary.** Snapshot lightweight git context at session start and inject it as
a dynamic prompt section.

**Scope.**
- Capture current branch, `git status --short`, and `git log --oneline -5`.
- Degrade silently outside a git repository or when git is unavailable.
- Keep the dynamic section separate from static prompt blocks for prompt-cache
  friendliness.

**Acceptance criteria.**
- Chat sessions in a git repo include concise current git context.
- Non-git directories still start cleanly.
- Prompt caching keeps static and dynamic sections separated.

**Verify.**
- `go test ./internal/projectctx ./internal/agent ./cmd/neo`
- `go test ./...`

---

### NEO-16 — Permission approval modal

**Status:** todo

**Related:** GitHub issue #94.

**Summary.** Improve approval prompts so risky tool calls are clear, focused,
and inspectable during real coding sessions.

**Scope.**
- Render approval as a modal or focused panel rather than ordinary scrollback.
- Support scrollable previews for long write/edit diffs.
- Show tool name, reason, path/command summary, and approve/deny keys.
- Preserve `y` approval, `n`/`esc` denial, and cancellation behavior.

**Acceptance criteria.**
- Long diffs can be inspected before approving.
- Approval state cannot be confused with normal transcript output.
- Denied tools still return an error `tool_result` the model can recover from.
- TUI tests cover approve, deny, cancel, and long-preview rendering.

**Verify.**
- `go test ./internal/tui ./internal/permission ./internal/agent`
- `go test ./...`

---

### NEO-18 — OpenAI subscription-auth verification

**Status:** todo

**Related:** GitHub issue #66.

**Summary.** The codebase has OpenAI subscription-auth code, but GitHub issue
#66 remains open. Verify the live implementation against the issue acceptance
criteria, then either implement missing behavior or update/close the issue.

**Scope.**
- Compare `internal/llm/openai`, `internal/auth`, and CLI auth commands against
  GitHub issue #66.
- Keep local docs aligned with the issue result.

**Acceptance criteria.**
- Open provider issues no longer contradict local roadmap/ticket claims.
- Any missing provider behavior is implemented with tests, or the issue tracker
  is updated to reflect delivered work.
- Generated developer docs are updated only through `cmd/neo-docs` if needed.

**Verify.**
- `go test ./internal/llm/... ./internal/auth ./cmd/neo`
- `go test ./...`

---

### NEO-11 — Automated release PRs

**Status:** todo

**Summary.** Add release automation that regularly prepares releases instead of
requiring a human to manually choose and push tags.

**Scope.**
- Use Release Please or an equivalent conventional-commit release PR workflow.
- Run on pushes to `main` and on a weekly schedule.
- Generate/update a changelog and release PR.
- When the release PR is merged, create the tag that triggers the existing
  GoReleaser workflow.

**Acceptance criteria.**
- New conventional commits on `main` produce or update a release PR.
- Merging the release PR creates a `v*` tag.
- The existing release workflow publishes binaries/checksums and updates the
  Homebrew cask.
- Docs-only/test-only changes do not force noisy releases unless configured.

**Verify.**
- Dry-run the release workflow where supported.
- Confirm `release.yml` still triggers only on `v*` tags.
- Confirm repository secrets include `HOMEBREW_TAP_GITHUB_TOKEN`.

---

## Done historical tickets

Completed entries are kept short so this file does not become a second issue
tracker. Use git history and GitHub Issues for full implementation detail.

### NEO-2 — OpenAI provider (API key)

**Status:** done

Delivered `internal/llm/openai`, OpenAI Responses API translation,
`OPENAI_API_KEY` auth, provider config selection, and tests for request/response
translation.

### NEO-3 — OpenAI OIDC / subscription auth

**Status:** done

Delivered `neo login` / `neo logout`, Codex device-code auth, token storage in
`~/.neo/auth.json`, and the experimental subscription-backed OpenAI transport.
GitHub issue #66 is still open, so verification/status reconciliation remains
active in NEO-18.

### NEO-4 — Roadmap and ticket hygiene

**Status:** done

Moved stale completed work out of the active todo set, made GitHub Issues the
explicit source of truth for live backlog status, and kept generated docs
untouched.

### NEO-5 — `/permissions` picker

**Status:** done

Delivered a TUI picker for `ask`, `trusted`, and `readonly` session-local
permission modes. Follow-up permission UX work lives in NEO-16 / GitHub issue
#94.

### NEO-14 — Structured search tool output

**Status:** done

Delivered JSON output for `grep` and `glob`, including empty results,
truncation metadata, structured grep context, generated docs, and tests. GitHub
issue #46 is closed.

### NEO-10 — Enforce generated docs on pull requests

**Status:** done

CI runs `go run ./cmd/neo-docs --check` on pull requests, while the main-branch
developer-docs workflow can regenerate and commit generated output.

### NEO-17 — User docs for robust-core tools

**Status:** done

README now documents permission modes, approval behavior, workspace-bound path
tools, non-sandboxed approved/trusted bash, search tools, slash commands,
sessions, `!`, and `@file`. GitHub issue #98 is closed.

### Provider retry cleanup

**Status:** done

Provider retries now honor standard `Retry-After` headers, HTTP-date hints,
zero-delay hints, caps, jitter, and cancellable waits across Anthropic, OpenAI
API-key, and OpenAI Codex transports. GitHub issue #84 is closed.

### NEO-12 — `!` shell command alias

**Status:** done

Delivered direct `!<command>` execution through the existing bash tool path and
permission policy. GitHub issue #77 is closed.

### NEO-13 — `@file` reference picker

**Status:** done

Delivered TUI `@` path completion with ignored-directory handling and insertion
tests.
