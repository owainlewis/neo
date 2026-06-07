# Tickets

Detailed, actionable tickets for Neo. Higher-level themes live in `ROADMAP.md`;
this file holds the specifics (scope, acceptance criteria, notes) for work that's
queued or in progress.

Status legend: `todo` · `in-progress` · `done`

---

## NEO-1 — `neo update`: self-update to the latest release

**Status:** todo

**Summary.** Add a `neo update` command that checks GitHub Releases for a newer
version and updates the installed binary in place, mirroring what `install.sh`
does but from within the running binary.

**Motivation.** Today the only way to upgrade is to re-run the install script or
`go install ... @latest`. A built-in `neo update` makes upgrades a single,
discoverable command and keeps users current.

**Scope.**
- New subcommand `neo update` (and surface it in `neo help`).
- Query the GitHub Releases API for the latest tag; compare against the
  version stamped into the binary at build time.
- If newer: download the matching OS/arch release archive, verify its checksum
  (reuse the same checksum convention as `install.sh`), and atomically replace
  the current executable.
- Flags: `--check` (report only, don't install) and `--version <vX.Y.Z>` (pin a
  specific version), consistent with `install.sh` options.
- Clear messaging when already up to date, when no prebuilt binary exists for the
  platform, and when the binary lives in a non-writable location.

**Acceptance criteria.**
- `neo update --check` prints current vs. latest and exits 0 without modifying
  anything.
- `neo update` on an out-of-date install replaces the binary and the new version
  is reported on next launch.
- Checksum mismatch aborts the update without touching the existing binary.
- `neo update` on an up-to-date install is a no-op with a friendly message.

**Notes.**
- Version is already stamped at build time (`just print-version`); reuse that as
  the comparison baseline.
- Keep the download/verify/replace logic factored so it can be unit-tested
  against a fake release server (see `internal/llm/llmtest` for the testing
  style used elsewhere).

---

## NEO-2 — OpenAI provider (API key)

**Status:** done

**Summary.** Add a second `llm.Provider` implementation backed by the OpenAI API
(API-key auth) to prove the provider seam holds and to give users an alternative
to Anthropic API credits.

**Motivation.** This delivered the roadmap's second provider item. The
`llm.Provider` interface (`internal/llm/provider.go`) is already
provider-agnostic; this ticket exercises that seam with a real second
implementation. OIDC / subscription-based auth was explicitly **out of scope**
here and delivered separately in NEO-3.

**Delivered.**
- New package `internal/llm/openai` with a `Client` implementing
  `llm.Provider` (`Name()` + `Complete()`).
- Translate `llm.Request` → OpenAI Responses API requests and
  the response back into `llm.Response`, including:
  - system prompt (flatten `SystemBlocks` for OpenAI),
  - multi-turn messages with tool calls (`tool_use` / `tool_result` mapping),
  - tool specs (`ToolSpec` → OpenAI `tools` / `function` schema),
  - token usage accounting into `llm.Usage` (cache fields zero).
- Auth via `OPENAI_API_KEY`.
- Retry/backoff parity with the Anthropic client (429/5xx, `Retry-After`).
- Config: add a `provider:` key (`anthropic` | `openai`) and make
  `cmd/neo` select the provider in `mustProvider()` instead of hardcoding
  Anthropic. Sensible default model per provider.

**Acceptance criteria.**
- With `provider: openai` and `OPENAI_API_KEY` set, a chat turn completes and
  tool calls round-trip correctly.
- `provider: anthropic` (the default) behaves exactly as today.
- Unit tests for request/response translation against a fake HTTP server,
  matching the `anthropic/client_test.go` style.
- `go test ./...` and `go vet ./...` pass.

**Notes.**
- Keep the translation layer self-contained in the package; the core agent loop
  must remain provider-agnostic.

---

## NEO-3 — OpenAI OIDC / subscription auth (follow-up to NEO-2)

**Status:** done

**Summary.** Authenticate to OpenAI via the Codex device-code flow so a
ChatGPT subscription can be used instead of pay-per-token API credits.

**Delivered.**
- `internal/auth`: Codex device-code login against `auth.openai.com`, token
  refresh, JWT account-id extraction, and `~/.neo/auth.json` credential storage
  (0600, atomic) with a refreshing
  `TokenSource`.
- `internal/llm/openai` migrated to the **Responses API** (Chat Completions
  retired). The same translation drives two transports: the API-key `Client`
  (`api.openai.com/v1/responses`) and the subscription `CodexClient`
  (`chatgpt.com/backend-api/codex/responses`, OAuth + `chatgpt-account-id`
  headers, SSE assembled into a blocking response).
- CLI: `neo login` / `neo logout`; config `openai_auth: api_key|subscription`.

**Caveats / follow-ups.**
- The subscription path targets OpenAI's undocumented Codex backend with the
  Codex client_id. It is unverified against the live service and may breach
  OpenAI's ToS; treat as experimental. Default model `gpt-5-codex` is a guess —
  override with `model:` if rejected.
- Force-refresh-on-401 is not implemented.
- `auth.json` has no cross-process lock (fine for a single interactive CLI).

---

## NEO-4 — Roadmap and ticket hygiene

**Status:** todo

**Summary.** Keep `ROADMAP.md` and `TICKETS.md` aligned with the live code and
the generated teaching guides.

**Motivation.** Neo is moving quickly. Stale roadmap items make it harder for a
coordinator or future contributor to know what is actually done.

**Scope.**
- Move completed work into the roadmap's Done section.
- Keep `TICKETS.md` focused on actionable work, not historical noise.
- Add a short maintenance note that GitHub Issues are the source of truth when
  they exist.

**Acceptance criteria.**
- Roadmap no longer lists implemented features as todo.
- Tickets describe the next small slices with scope and acceptance criteria.
- No generated docs are edited by hand.

**Verify.**
- `go test ./...`
- `go run ./cmd/neo-docs --check`

---

## NEO-5 — `/permissions` picker

**Status:** todo

**Summary.** Turn `/permissions` from a read-only status line into a selectable
TUI picker for `ask`, `trusted`, and `readonly`.

**Motivation.** `/model` and `/sessions` are now useful interactive surfaces.
Permissions should behave similarly so users can quickly switch between safe
review mode and trusted local work.

**Scope.**
- Add a full-screen permissions browser or compact picker in the TUI.
- Show the current mode and a plain-English description of each mode.
- Selecting a mode updates the active session's permission policy.
- Keep the change session-local for v1; do not rewrite config yet.

**Acceptance criteria.**
- `/permissions` opens an interactive selector.
- Selecting `trusted` stops approval prompts for built-in tools while workspace
  path checks still apply.
- Selecting `readonly` denies bash and file mutation tools.
- Existing permission policy tests still pass.

**Verify.**
- `go test ./internal/tui ./internal/permission ./internal/agent`
- `go test ./...`

---

## NEO-6 — Session search

**Status:** todo

**Summary.** Add a simple search over saved session transcripts.

**Motivation.** Session search is the most useful form of episodic memory for a
coding agent: "what did we decide last time?"

**Scope.**
- Add a session search API in `internal/session`.
- Search saved session JSON text for a plain query.
- Return session id, updated time, title, cwd, and a short matching snippet.
- Add a CLI surface, likely `neo sessions search <query>`.
- Optionally add `/sessions search <query>` later if the CLI path is clean.

**Acceptance criteria.**
- Searching finds matches in user and assistant text.
- Results are newest-first and include enough context to choose a session.
- Empty results produce a clear message.
- Search does not mutate session files.

**Verify.**
- `go test ./internal/session ./cmd/neo`
- `go test ./...`

---

## NEO-7 — Experimental memory stub

**Status:** todo

**Summary.** Add an off-by-default memory feature flag and a minimal `/memory`
surface before implementing real project memory.

**Motivation.** The teaching guides now explain memory, but Neo should not grow a
full memory system until the shape is clear. A stub makes the feature boundary
explicit without bloating the agent.

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
- Developer docs and teaching guides mention the flag.

**Verify.**
- `go test ./internal/config ./internal/tui ./cmd/neo-docs`
- `go run ./cmd/neo-docs --check`
- `go test ./...`

---

## NEO-8 — Improve `/tools` display

**Status:** todo

**Summary.** Make `/tools` easier to scan by grouping tools and showing
permission behavior.

**Motivation.** The current `/tools` output is technically correct but not very
helpful as a teaching or debugging surface.

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

## NEO-9 — Teaching guide polish pass

**Status:** todo

**Summary.** Refine the generated teaching guides so they read like practical
"explain it simply" docs rather than reference pages.

**Motivation.** The first guide set exists. The next pass should make them more
useful for videos, contributors, and future Neo sessions.

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

## NEO-10 — Enforce generated docs on pull requests

**Status:** todo

**Summary.** Make CI fail when generated developer docs or teaching guides are
out of date.

**Motivation.** The current `docs.yml` workflow can auto-commit generated docs on
`main`, but it does not protect pull requests. Contributors should see stale docs
before merge.

**Scope.**
- Add `go run ./cmd/neo-docs --check` to CI.
- Keep the existing main-branch docs auto-update workflow if desired.
- Ensure generated guide pages are included in the check.

**Acceptance criteria.**
- A PR that changes docs generator output without regenerating docs fails CI.
- A clean checkout passes `go run ./cmd/neo-docs --check`.
- The docs auto-update workflow remains manual/main-only and does not fight PR
  checks.

**Verify.**
- `go run ./cmd/neo-docs --check`
- `go test ./cmd/neo-docs`

---

## NEO-11 — Automated release PRs

**Status:** todo

**Summary.** Add release automation that regularly prepares releases instead of
requiring a human to manually choose and push tags.

**Motivation.** Neo already has GoReleaser on `v*` tags. The missing part is a
safe, repeatable way to decide the next version and create the tag/release.

**Recommended approach.**
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

## NEO-12 — `!` shell command alias

**Status:** todo

**Summary.** Let users type `!<command>` in the TUI to run a shell command
directly, without asking the model to call `bash`.

**Motivation.** Coding-agent users often want quick terminal actions such as
`!git status`, `!go test ./...`, or `!date`. A direct shell alias makes those
fast while keeping them visible in the transcript.

**Scope.**
- Detect input beginning with `!` before normal chat send.
- Run the command through the same bash tool path and permission policy as model
  shell calls.
- Render the command and result as tool call/result blocks.
- Do not send the command text to the model unless the user explicitly asks for
  follow-up analysis.

**Acceptance criteria.**
- `!git status` runs a shell command from the TUI.
- Permission modes still apply: `ask` asks, `trusted` runs, `readonly` denies.
- Command output is visible in the scrollback.
- Empty `!` input shows a helpful error.

**Verify.**
- `go test ./internal/tui ./internal/agent ./internal/tools`
- Manual smoke test with `!echo hello`

---

## NEO-13 — `@file` reference picker

**Status:** todo

**Summary.** Add `@` file reference completion in the TUI so users can search
and insert file paths into a message.

**Motivation.** Claude Code, Codex, and similar tools make file references fast.
Typing `@internal/tui/model.go` should be easier than manually copying paths.

**Scope.**
- When the input contains an active `@` token, show a file picker.
- Search workspace files by path substring.
- Use arrow keys/tab/enter to insert the selected path into the input.
- Respect ignored/heavy directories such as `.git`, `node_modules`, `dist`, and
  `vendor`.
- Keep this as reference insertion only; do not automatically attach file
  contents in v1.

**Acceptance criteria.**
- Typing `@mod` shows matching files such as `internal/tui/model.go`.
- Selecting a file inserts its relative path into the input.
- The picker does not appear for email-like text unless there is a clear file
  reference token.
- The feature works at narrow terminal widths without overlapping the input.

**Verify.**
- `go test ./internal/tui ./internal/tools`
- Manual smoke test by typing `@` in a repo with nested files.
