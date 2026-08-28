# CLI

## Commands

| Command | Description |
| --- | --- |
| `neo` | Open interactive chat mode. |
| `neo chat` | Open interactive chat mode explicitly. |
| `neo run [options] <prompt>` | Run one headless prompt and exit without session persistence. |
| `neo agents` | List discovered agent prompts and where each came from. |
| `neo sessions` | List saved chat sessions. |
| `neo doctor` | Check local config, credentials, sessions, git, and workspace readiness. |
| `neo sessions search <query>` | Search saved session transcripts locally. |
| `neo resume <id>` | Resume a saved chat session. |
| `neo login` | Log in to an OpenAI ChatGPT/Codex subscription with device-code auth. |
| `neo logout` | Remove stored OpenAI subscription credentials. |
| `neo version` | Print the build-time Neo version. Also available as `neo -v` and `neo --version`. |
| `neo help` | Print usage. |

## Global flags

| Flag | Description |
| --- | --- |
| `--agent <name>` | Replace the built-in system prompt with `<name>.md` from `~/.neo/agents/` or `.neo/agents/`. Accepted anywhere in the arguments, before or after a subcommand, and in `--agent=name` or `--agent name` form. |

`--agent` is extracted before subcommand dispatch, so it works with `neo`,
`neo chat`, `neo run`, and `neo resume`. An unknown name exits non-zero and
lists what exists rather than falling back to the coding prompt. Sessions record
the agent they were started with, so `neo resume` keeps it unless a different
`--agent` is given.

## Environment

- `ANTHROPIC_API_KEY` is required when `provider: anthropic`.
- `OPENAI_API_KEY` is required when `provider: openai` uses `openai_auth: api_key`.
- `OPENROUTER_API_KEY` is required when `provider: openrouter`.
- `GOOGLE_API_KEY` is required when `provider: google`.
- `openai_auth: subscription` uses stored ChatGPT/Codex device-code credentials created by `neo login` instead of an API key.

## Runtime Notes

- `neo` with no subcommand defaults to chat.
- `neo run` executes one prompt without opening the TUI, prints the final answer, and exits. It is intended for scripts and eval harnesses.
- `neo run` applies a `10m` timeout, does not create or update sessions, and supports `--json` for a machine-readable summary containing elapsed time, tool counts, and token usage.
- Headless runs receive the standard tool registry and do not use interactive `tool_approvals`. Run Neo inside a VM or sandbox that provides the required filesystem, process, network, and credential boundaries.
- The removed `--permission` option returns migration guidance instead of being silently accepted.
- `neo run` accepts prompt text as arguments and prepends piped stdin when present, e.g. `cat prompt.md | neo run --json`.
- `neo doctor` is local-first: it checks config, required credential presence, session store access, git availability, and whether the current directory is a git workspace without calling providers or printing secrets.
- The interactive `@` file picker indexes files under Neo's effective startup working directory and inserts paths relative to that directory.
- `neo login` prints the OpenAI Codex device-code URL and one-time code, then stores refreshable subscription credentials in `~/.neo/auth.json` with file permissions intended to protect secrets.
- `neo logout` deletes the stored OpenAI subscription credential entry.
- Resuming a session attempts to change into the saved session cwd. If unavailable, Neo warns and stays in the current directory.
- Session saves happen after each user turn through the TUI `WithAfterSend` callback. When an interactive quit interrupts an active turn, Neo cancels the turn and waits for this save before exiting. If the save fails, Neo keeps the TUI open and lets the user retry by quitting again. A second interrupt while cancellation or a retry is pending forces an immediate exit.

## Headless JSON output

`neo run --json` prints one JSON object to stdout instead of the plain-text
answer. The object always includes a `usage` field, even when the run fails
before any model call happens:

```json
{
  "ok": true,
  "elapsed_ms": 4213,
  "provider": "anthropic",
  "model": "claude-sonnet-4-5",
  "tool_calls": 2,
  "tool_errors": 0,
  "usage": {
    "input_tokens": 812,
    "cache_creation_input_tokens": 1500,
    "cache_read_input_tokens": 3200,
    "output_tokens": 96,
    "total_tokens": 5608
  },
  "final": "OK"
}
```

`usage` accounts for every model call the headless run made, including any
`internal/compact` compaction call triggered mid-turn, and reflects usage
from calls made before a later failure — a failed run can still report
non-zero usage. `input_tokens` is uncached prompt input; cache creation and
cache read are reported separately because they are billed at different
rates. `total_tokens` is always `input_tokens + cache_creation_input_tokens +
cache_read_input_tokens + output_tokens`, computed by Neo rather than copied
from any provider-reported total. If config loading, provider construction,
or `--agent` profile loading fails before an agent exists, `usage` is still
present with all five fields `0`.

## Process Boundary

`cmd/neo.run(args, streams)` is the command dispatcher. It installs signal
handling, writes through the supplied stdin, stdout, and stderr streams, and
returns the command's exit code. Command helpers return status codes rather
than terminating the process.

The top-level `main` function contains the only `os.Exit` call. Before reaching
that boundary, `execute` initializes logging, defers logging cleanup, and calls
`run`. This guarantees cleanup completes before both successful and failed
commands terminate.
