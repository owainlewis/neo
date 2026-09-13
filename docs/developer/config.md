# Configuration

Neo loads the first available config:

1. `./neo.yaml`
2. `~/.neo/config.yaml`
3. Embedded defaults from `internal/config/defaults/neo.yaml`

First hit wins. Config files are not merged.

## Default Config

This is the complete configuration surface. Copy it to `~/.neo/config.yaml` or
`./neo.yaml` and change only the values you need.

```yaml
# LLM backend: anthropic, openai, openrouter, or google.
provider: anthropic

# OpenAI only: api_key uses OPENAI_API_KEY; subscription uses `neo login`.
# openai_auth: api_key

model: claude-opus-5

# Optional. When omitted, subagents follow the active provider and model.
# subagents:
#   provider: anthropic
#   model: <claude-model-id>

# Optional interactive confirmations. Empty by default.
# tool_approvals:
#   - git
#   - rm -rf
#   - write_file

# Transcripts compact once the provider reports a prompt at 70% of this size.
compaction:
  context_window_tokens: 200000

features:
  agents_file: true
  skills: true
  prompt_caching: true

output:
  verbose: false
```

The compaction window applies consistently to the coordinator and child
agents. Children that follow the coordinator use the active model after a
`/model` switch while retaining this configured window. Omitting the setting
keeps the built-in 200,000-token default for both. The trigger measures the
prompt size the provider reported for the previous request, so it accounts for
the system prompt and tool definitions rather than the transcript alone.

The embedded source, including annotated provider examples, is
[`internal/config/defaults/neo.yaml`](https://github.com/owainlewis/neo/blob/main/internal/config/defaults/neo.yaml).

## Provider Selection

| Config | Auth source | Provider adapter |
| --- | --- | --- |
| `provider: anthropic` | `ANTHROPIC_API_KEY` | `internal/llm/anthropic` |
| `provider: openai` with `openai_auth: api_key` | `OPENAI_API_KEY` | `internal/llm/openai.Client` |
| `provider: openai` with `openai_auth: subscription` | ChatGPT/Codex device-code credentials from `~/.neo/auth.json` | `internal/llm/openai.CodexClient` |
| `provider: openrouter` | `OPENROUTER_API_KEY` | `internal/llm/openrouter` |
| `provider: google` | `GOOGLE_API_KEY` | `internal/llm/google` |

Subscription credentials are created with `neo login` and removed with `neo logout`. The docs describe only where credentials live and which flow uses them; token values are never generated into developer docs.

The top-level `provider` selects the backend for a session. In the TUI, `/model` lists models for that provider and switches the model and compactor for the current session without rewriting configuration. Start a new session with a different `provider` value to change backends.

## Feature Flags

Each feature flag is tri-state in Go: absent means use the built-in default, while explicit `false` disables that capability.

| Flag | Default | Effect |
| --- | --- | --- |
| `agents_file` | `true` | Load AGENTS.md into the chat system prompt. Project files are read through a rooted workspace boundary. Relative and absolute project symlinks load only when their resolved targets stay inside the workspace; escaping files are skipped with a warning. Safe project files and the explicit user-global `~/.neo/AGENTS.md` remain loaded. |
| `skills` | `true` | Discover skills and expand $name references or /name slash invocations. |
| `prompt_caching` | `true` | Mark the stable system prompt prefix as cacheable when the provider supports it. |

## Output

`output.verbose` is tri-state, same as feature flags: absent or `false` means concise mode (the default).

| Setting | Default | Effect |
| --- | --- | --- |
| `output.verbose: false` | (default) | Show live in-flight activity and concise completed receipts (e.g. a file read or command run). Errors, failures, and direct `!` command output always render in full. |
| `output.verbose: true` | | Restore full tool call/result cards, including complete file contents and command output. |

## Skills

Skills are files, not config. Neo always provides `/design`, `/plan`,
`/build`, and `/review`; add or replace one with
`.neo/skills/<name>/SKILL.md` in the project or `~/.neo/skills/<name>/SKILL.md`
globally. The `features.skills` flag controls discovery of those files; the
built-ins stay available either way. A `phases` key in `neo.yaml` is rejected
with a message pointing at the skill layout. See [Skills](skills.md).

## Tool Approvals

`tool_approvals` is an optional top-level list. It is empty by default and
applies only to interactive coordinator calls and direct `!` commands.

```yaml
tool_approvals:
  - git
  - rm -rf
  - write_file
```

Each entry matches both an exact tool name and a Bash command prefix.
Matching is case-sensitive and requires whitespace or the end after the
prefix, so `git` matches `git status` but not `github`.

Bash commands are split at unquoted `&&`, `||`, `;`, `|`, `&`, and newlines.
Leading `VAR=value` assignments are stripped from each segment, and whitespace
between words is normalized before matching. Quotes and escapes keep argument
text together, so `echo "a; rm -rf b"` does not match `rm -rf`. Literal scripts
passed to `sh -c` and `bash -c` are inspected too. For example, a `git push`
entry matches `cd sub && git push`, `git  push`, `VAR=1 git push`, and
`sh -c "git push"`.

Entries are trimmed when loaded. Empty entries are rejected and exact
duplicates keep their first position. This is lexical matching, not full shell
parsing: other wrappers, aliases, variable expansion, script files, and
substitutions are not resolved.

This is optional user-interface friction, not a security boundary. It is not
passed to `neo run` or child agents. The VM or sandbox must control filesystem,
process, network, credentials, and external-service access.

The old `permissions:` section has been removed. Neo rejects it with migration
guidance instead of silently granting broader access.
