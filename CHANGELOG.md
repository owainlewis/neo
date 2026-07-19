# Changelog

All notable changes to Neo will be documented in this file.

## [v0.3.0] - 2026-07-19

### Highlights

- Added Google Gemini as a first-class provider and made provider and model switching available from the running TUI.
- Added active steering and queued follow-up instructions so users can redirect ongoing work without breaking tool-call transcripts.
- Expanded the coordinator experience with live subagent activity, configurable subagent backends, slash-invoked skills, and clearer workflow progress.
- Reworked the terminal UI around concise tool receipts, visible task status, native text selection, and less noisy output.

### Added

- Google Gemini provider support.
- Runtime provider and model switching from the model picker.
- Active-turn steering and queued follow-up messages.
- Live subagent activity in the terminal UI.
- Configurable provider and model selection for subagents.
- Skill invocation through slash commands.
- Lightweight performance regression budgets for workflows, tools, and the TUI.
- A public Astro and Starlight website for product guidance and generated developer documentation.

### Changed

- Made workflow checklists model-driven and separated compact progress from normal output.
- Defaulted routine tool activity to concise receipts, with verbose output remaining configurable.
- Simplified provider, CLI, composer, agent-loop, and landing-page internals.
- Replaced `.neo/commands` prompt templates and `features.prompt_commands` with slash-invoked `.neo/skills`; existing custom prompt commands must be migrated to skills.
- Removed project memory and its bundled repository artifacts to keep Neo focused on explicit project instructions, skills, and local sessions.
- Moved website deployment from Cloudflare Pages to GitHub Pages while keeping DNS independent.

### Fixed

- Hardened built-in tools, permission checks, and provider-native tool argument decoding.
- Restored native terminal text selection and kept selection working across TUI updates.
- Reported recovered tool failures correctly and kept progress separate from assistant output.
- Ran independent `neo doctor` checks even when configuration loading fails.
- Removed redundant tool output, duplicate max-turn summaries, and dead retry wrappers.
- Installed Neo onto a runnable path more reliably.

## [v0.2.2] - 2026-07-07

### Changed

- Published a patch release to verify the configured Homebrew tap token updates `owainlewis/homebrew-tap` during release automation.

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

[v0.3.0]: https://github.com/owainlewis/neo/compare/v0.2.2...v0.3.0
[v0.2.2]: https://github.com/owainlewis/neo/compare/v0.2.1...v0.2.2
[v0.2.1]: https://github.com/owainlewis/neo/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/owainlewis/neo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/owainlewis/neo/releases/tag/v0.1.0
