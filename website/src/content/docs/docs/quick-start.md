---
title: Quick start
description: Get from a fresh install to your first Neo chat.
editUrl: false
---

Follow this once and you should reach your first chat.

## 1. Choose a backend

Neo defaults to Anthropic. Set `provider` when you want OpenAI, OpenRouter, or Google Gemini.

| Backend | What you need | Config | Extra step |
|------|------|------|------|
| Anthropic | `ANTHROPIC_API_KEY` | No config required | None |
| OpenAI API key | `OPENAI_API_KEY` | `provider: openai` | None |
| OpenAI subscription | ChatGPT/Codex subscription | `provider: openai` and `openai_auth: subscription` | Run `neo login` once |
| OpenRouter | `OPENROUTER_API_KEY` | `provider: openrouter` | None |
| Google Gemini | `GOOGLE_API_KEY` | `provider: google` | None |
| Custom endpoint | API key in an env var you name | `provider: custom` + `custom.base_url` and `model` | None |

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

OpenRouter:

```bash
export OPENROUTER_API_KEY="sk-or-..."
```

Google Gemini:

```bash
export GOOGLE_API_KEY="..."
```

Custom OpenAI-compatible endpoint (any gateway that speaks the Chat Completions API):

```bash
export MY_API_KEY="..."
```

The env var name is yours to choose; `custom.api_key_env` below tells Neo where to look, and
`CUSTOM_API_KEY` is the default when you leave it unset.

## 3. Create `neo.yaml` only if you need a non-default provider

Anthropic users can skip this step because `provider: anthropic` is the default.

```yaml
provider: openai
openai_auth: api_key
```

For an OpenAI subscription, use `openai_auth: subscription`. For OpenRouter or Gemini, set
`provider: openrouter` or `provider: google` respectively.

For any other OpenAI-compatible endpoint (a gateway, proxy, or alternative host that speaks the
Chat Completions API), use the `custom` provider. `model` is required — an arbitrary endpoint has
no default:

```yaml
provider: custom
model: your-model-id
custom:
  base_url: https://example.com/v1
  api_key_env: MY_API_KEY
```

Neo reads the first config file it finds in this order:

1. `./neo.yaml`
2. `~/.neo/config.yaml`
3. Embedded defaults

See the [configuration reference](/docs/reference/config/) for the full set of options.

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

## Built-in phases

Neo includes four named prompts that appear as slash commands and as the active
label beside normal workflow progress:

| Phase | What it does |
|------|------|
| `/design <goal>` | Design a product change, feature, or bug fix without implementing it |
| `/plan <goal>` | Break accepted work into small tasks with checks |
| `/build <goal>` | Implement, test, self-review, and verify the change |
| `/review [scope]` | Review and improve code, PR feedback, or CI results |

Phases activate only through these slash commands. Ordinary prose containing a
phase name is sent unchanged and does not activate one.

Add or override named prompts with the `phases` map in `neo.yaml`. The
[configuration reference](/docs/reference/config/) includes an example.

## Common commands

| Command | What it does |
|------|------|
| `neo` | Open interactive chat mode |
| `neo chat` | Open interactive chat mode explicitly |
| `neo sessions` | List saved chats |
| `neo doctor` | Check local config, credentials, sessions, git, and workspace |
| `neo sessions search <query>` | Search saved chat transcripts |
| `neo resume <id>` | Resume a saved chat |
| `neo login` / `neo logout` | Set up or remove OpenAI subscription auth |

See the full [CLI reference](/docs/reference/cli/) for every command and flag.
