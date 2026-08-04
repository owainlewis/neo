# Changelog

All notable changes to Neo will be documented in this file.

## Unreleased

## [v0.6.2] - 2026-08-04

### Security

- Redacted Google and OpenRouter API keys everywhere Neo sanitizes provider
  credentials, including verbose logs and nested payloads.
- Rejected project `AGENTS.md` symlinks that resolve outside the workspace while
  continuing to support symlinks whose targets remain inside it.

### Fixed

- Updated the embedded Anthropic default model to match the documented default.
- Preserved the OpenAI provider identity in saved sessions so resumed chats use
  the correct authentication mode.
- Centralized `/clear` conversation reset so it clears the full conversation,
  workflow, tool, image, and usage state consistently.

## [v0.6.1] - 2026-08-04

### Fixed

- Kept interactive `@` file references rooted at Neo's effective startup
  working directory, including resumed sessions, instead of walking up to an
  ancestor Git repository.

## [v0.6.0] - 2026-08-01

### Added

- Added visible, configurable named phases with built-in `/design`, `/plan`,
  `/build`, and `/review` prompts. Phases reuse the existing workflow UI and can
  be extended or overridden through `neo.yaml`. They activate only through
  explicit slash commands.

### Fixed

- Rejected unsupported workflow actions instead of reporting success for an
  update the UI could not apply.

## [v0.5.1] - 2026-07-31

### Changed

- Polished the workflow experience in the TUI: simpler splash and welcome,
  clearer activity and workflow hints, and tidier command and file pickers.

## [v0.5.0] - 2026-07-29

### Changed

- Replaced permission modes and command-risk heuristics with default-empty,
  interactive-only `tool_approvals`. Neo now treats its VM or sandbox as the
  security boundary. Existing `permissions:` config and `--permission` fail
  with migration guidance.

## [v0.4.1] - 2026-07-25

### Changed

- Default models updated to current generations: Anthropic now defaults to Claude Opus 5, OpenAI to GPT-5.6 Sol, and the OpenRouter fallback to Claude Sonnet 5. The `/model` picker lists the GPT-5.6 and Claude 5 families.

### Fixed

- Transcript scrolling in the TUI. The alt screen hides the terminal's own scrollback and mouse reporting was disabled, so the mouse wheel did nothing and history was unreachable. Wheel scrolling now works, with `shift+↑/↓` and `shift+home/end` as keyboard alternatives. Drag selection still works with shift held.

## [v0.4.0] - 2026-07-23

### Added

- Headless mode for running Neo non-interactively from the CLI.
- Parallel execution for safe tool calls and factory inspect subagents, with visible parallel execution groups in the TUI.

### Changed

- Trusted mode now skips approval prompts for dangerous bash commands too, so it auto-allows all bash; path-shaped file tools still cannot escape the workspace root.
- Simplified the release process to use the verified installer only.
- Removed low-value product complexity and unused tools/permissions commands from the TUI.

### Fixed

- Restored chat history scrolling in the TUI.

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

[v0.6.2]: https://github.com/owainlewis/neo/compare/v0.6.1...v0.6.2
[v0.6.1]: https://github.com/owainlewis/neo/compare/v0.6.0...v0.6.1
[v0.6.0]: https://github.com/owainlewis/neo/compare/v0.5.1...v0.6.0
[v0.5.1]: https://github.com/owainlewis/neo/compare/v0.5.0...v0.5.1
[v0.5.0]: https://github.com/owainlewis/neo/compare/v0.4.1...v0.5.0
[v0.4.1]: https://github.com/owainlewis/neo/compare/v0.4.0...v0.4.1
[v0.4.0]: https://github.com/owainlewis/neo/compare/v0.3.0...v0.4.0
[v0.3.0]: https://github.com/owainlewis/neo/compare/v0.2.2...v0.3.0
[v0.2.2]: https://github.com/owainlewis/neo/compare/v0.2.1...v0.2.2
[v0.2.1]: https://github.com/owainlewis/neo/compare/v0.2.0...v0.2.1
[v0.2.0]: https://github.com/owainlewis/neo/compare/v0.1.0...v0.2.0
[v0.1.0]: https://github.com/owainlewis/neo/releases/tag/v0.1.0
