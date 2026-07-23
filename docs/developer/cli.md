# CLI

## Commands

| Command | Description |
| --- | --- |
| `neo` | Open interactive chat mode. |
| `neo chat` | Open interactive chat mode explicitly. |
| `neo run [options] <prompt>` | Run one headless prompt and exit. Defaults to read-only permissions and no session persistence. |
| `neo sessions` | List saved chat sessions. |
| `neo doctor` | Check local config, credentials, sessions, git, and workspace readiness. |
| `neo sessions search <query>` | Search saved session transcripts locally. |
| `neo resume <id>` | Resume a saved chat session. |
| `neo login` | Log in to an OpenAI ChatGPT/Codex subscription with device-code auth. |
| `neo logout` | Remove stored OpenAI subscription credentials. |
| `neo help` | Print usage. |

## Environment

- `ANTHROPIC_API_KEY` is required when `provider: anthropic`.
- `OPENAI_API_KEY` is required when `provider: openai` uses `openai_auth: api_key`.
- `OPENROUTER_API_KEY` is required when `provider: openrouter`.
- `GOOGLE_API_KEY` is required when `provider: google`.
- `openai_auth: subscription` uses stored ChatGPT/Codex device-code credentials created by `neo login` instead of an API key.

## Runtime Notes

- `neo` with no subcommand defaults to chat.
- `neo run` executes one prompt without opening the TUI, prints the final answer, and exits. It is intended for scripts and eval harnesses.
- `neo run` defaults to `--permission readonly`, applies a `10m` timeout, does not create or update sessions, and supports `--json` for a machine-readable summary containing elapsed time and tool counts.
- `neo run` accepts prompt text as arguments and prepends piped stdin when present, e.g. `cat prompt.md | neo run --json`.
- `neo doctor` is local-first: it checks config, required credential presence, session store access, git availability, and whether the current directory is a git workspace without calling providers or printing secrets.
- `neo login` prints the OpenAI Codex device-code URL and one-time code, then stores refreshable subscription credentials in `~/.neo/auth.json` with file permissions intended to protect secrets.
- `neo logout` deletes the stored OpenAI subscription credential entry.
- Resuming a session attempts to change into the saved session cwd. If unavailable, Neo warns and stays in the current directory.
- Session saves happen after each user turn through the TUI `WithAfterSend` callback.
