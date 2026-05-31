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

**Status:** in-progress

**Summary.** Add a second `llm.Provider` implementation backed by the OpenAI API
(API-key auth) to prove the provider seam holds and to give users an alternative
to Anthropic API credits.

**Motivation.** The roadmap's "Second provider" item. The `llm.Provider`
interface (`internal/llm/provider.go`) is already provider-agnostic; this ticket
exercises that seam with a real second implementation. OIDC / subscription-based
auth is explicitly **out of scope** here and tracked separately (see NEO-3).

**Scope.**
- New package `internal/llm/openai` with a `Client` implementing
  `llm.Provider` (`Name()` + `Complete()`).
- Translate `llm.Request` → OpenAI Chat Completions (or Responses) request and
  the response back into `llm.Response`, including:
  - system prompt (flatten `SystemBlocks` since OpenAI has no cache breakpoints),
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

**Status:** todo

**Summary.** Support authenticating to OpenAI via an OIDC/OAuth flow so a
ChatGPT subscription can be used instead of pay-per-token API credits.

**Motivation.** Reduce ongoing cost vs. API credits. Bigger than NEO-2: needs a
browser-based auth flow, token storage, and refresh handling — none of which
exists yet. Deliberately sequenced after NEO-2 proves the provider seam.

**Scope (high level).**
- A new auth flow (browser/device code), secure token storage, and refresh.
- Wire the OpenAI provider to use the obtained token instead of `OPENAI_API_KEY`.

**Notes.** Design ticket — scope to be refined once NEO-2 lands.
