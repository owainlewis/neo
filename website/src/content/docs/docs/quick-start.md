---
title: Quick start
description: Get from a fresh install to your first Neo chat.
editUrl: false
---

Follow this once and you should reach your first chat.

## 1. Choose a backend

Neo defaults to Anthropic. Use OpenAI only when you set `provider: openai`.

| Backend | What you need | Config | Extra step |
|------|------|------|------|
| Anthropic | `ANTHROPIC_API_KEY` | No config required | None |
| OpenAI API key | `OPENAI_API_KEY` | `provider: openai` | None |
| OpenAI subscription | ChatGPT/Codex subscription | `provider: openai` and `openai_auth: subscription` | Run `neo login` once |

If you are using OpenAI with an API key, you do not need `neo login`. `neo login` is only for the
device-code subscription flow.

## 2. Set credentials

Anthropic:

```bash
export ANTHROPIC_API_KEY="sk-ant-..."
```

OpenAI API key:

```bash
export OPENAI_API_KEY="sk-..."
```

OpenAI subscription:

```bash
neo login
```

`neo login` prints a device-code URL and one-time code, then stores the subscription credentials
in `~/.neo/auth.json`.

## 3. Create `neo.yaml` only if you need OpenAI

Anthropic users can skip this step because `provider: anthropic` is the default.

```yaml
provider: openai
openai_auth: api_key
```

Neo reads the first config file it finds in this order:

1. `./neo.yaml`
2. `~/.neo/config.yaml`
3. Embedded defaults

See the [configuration reference](/docs/config/) for the full set of options.

## 4. Start your first chat

```bash
neo
```

`neo` and `neo chat` open the same interactive terminal UI. Once it starts, try a first prompt
like:

```text
Summarize this repository and suggest a good first change.
```

If you built Neo locally but did not install it onto your `PATH`, run `./neo` instead.

## Common commands

| Command | What it does |
|------|------|
| `neo` | Open interactive chat mode |
| `neo chat` | Open interactive chat mode explicitly |
| `neo sessions` | List saved chats |
| `neo doctor` | Check local config, credentials, sessions, git, and workspace |
| `neo sessions search <query>` | Search saved chat transcripts |
| `neo update` | Install the latest stable release |
| `neo resume <id>` | Resume a saved chat |
| `neo login` / `neo logout` | Set up or remove OpenAI subscription auth |

See the full [CLI reference](/docs/cli/) for every command and flag.
