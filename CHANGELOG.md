# Changelog

All notable changes to Neo will be documented in this file.

## [v0.2.1] - 2026-07-07

### Fixed

- Skipped Homebrew cask publishing when the `HOMEBREW_TAP_GITHUB_TOKEN` secret is not configured, allowing GitHub release publishing to complete successfully.

## [v0.2.0] - 2026-07-07

### Highlights

- Added persistent chat sessions with resume support, a session browser, transcript search, and persisted token usage.
- Added OpenAI and OpenRouter provider support, including OpenAI subscription/device-code login and live OpenRouter model catalogue loading.
- Added robust tool permissions, approval inspection improvements, workspace path sandboxing, and safer atomic file writes.
- Added project memory, git-context injection, prompt-file slash commands, slash command autocomplete, and a coordinator-worker/subagent workflow experience.
- Added transcript compaction controls, capped tool-result transcript content, expanded truncated tool results, and better cancellation handling.
- Added update tooling for stable and nightly channels, plus release/nightly automation and generated developer documentation.

### Added

- `neo doctor` command for environment diagnostics.
- Saved-session transcript search.
- TUI model picker, permissions picker, file reference picker, and bang-shell alias.
- Structured `slog` tracing.
- Manual project memory support and README guidance for memory configuration.
- Repository skills for coordinator-worker and backlog-manager workflows.
- Nightly build workflow and generated developer docs checks in CI.

### Changed

- Improved TUI workflow status feedback, paste handling, display-width-aware truncation, and approval previews.
- Reused shared atomic file-write helpers across tools.
- Refreshed README onboarding, provider authentication documentation, robust-core usage documentation, roadmap, and developer docs.

### Fixed

- Cleaned up installer temporary directories safely.
- Preserved file modes on atomic writes.
- Preserved transcript invariants when turns are cancelled.
- Stopped bash child processes on cancellation.
- Honored `Retry-After` headers with jitter.
- Validated `openai_auth` configuration modes.
- Blocked `/memory` while a turn is active.
- Capped and streamed large tool results safely.

## [v0.1.0] - 2026-05-30

- Initial public release.

[v0.2.1]: https://github.com/owainlewis/neo/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/owainlewis/neo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/owainlewis/neo/releases/tag/v0.1.0
