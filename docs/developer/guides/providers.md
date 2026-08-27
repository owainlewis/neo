# Providers

## The Simple Idea

A provider is the adapter between Neo and a model API. Neo speaks its own small internal language; providers translate that into Anthropic, OpenAI, OpenRouter, or Gemini requests.

## The Problem

Different model APIs use different request shapes, response shapes, tool-call formats, auth methods, retry behavior, and token accounting. OpenRouter uses an OpenAI-compatible Chat Completions shape while OpenAI itself uses the newer Responses API in Neo.

Neo should not bake any one provider into the agent loop.

## How Neo Solves It

Neo defines one provider interface:

```go
type Provider interface {
    Name() string
    Complete(ctx context.Context, req Request) (*Response, error)
}
```

The core loop sends an `llm.Request`. The provider returns an `llm.Response`. Everything provider-specific stays behind the adapter.

## Streaming

`Complete` is blocking by design and always returns a finished response, but the
Anthropic adapter uses the streaming endpoint underneath and reassembles the
events in `stream.go`. Nothing is emitted as it arrives: Neo renders completed
blocks, so streaming here is a transport decision, not a UI one.

Two reasons for it:

- No fixed client deadline. A `http.Client.Timeout` caps how long one generation
  may take regardless of progress. The Anthropic client sets no timeout and lets
  the caller's context bound the request; every caller has one (Ctrl-C in chat,
  `--timeout` headless).
- The Messages API requires streaming above a `max_tokens` threshold, so raising
  `defaultMaxTokens` will not need another transport change.

Adapters must propagate the error from reading a response body. Discarding it
turns a cancelled request into an apparently successful empty body, which then
surfaces as a decode failure rather than the cancellation that happened.

## Current Providers

| Provider config | Auth | Adapter |
| --- | --- | --- |
| `provider: anthropic` | `ANTHROPIC_API_KEY` | `internal/llm/anthropic` |
| `provider: openai` + `openai_auth: api_key` | `OPENAI_API_KEY` | `internal/llm/openai.Client` |
| `provider: openai` + `openai_auth: subscription` | `neo login` device-code credentials | `internal/llm/openai.CodexClient` |
| `provider: openrouter` | `OPENROUTER_API_KEY` | `internal/llm/openrouter` |
| `provider: google` | `GOOGLE_API_KEY` | `internal/llm/google` |

## How Models Are Chosen

The config `model` value is passed through to the provider. If omitted, Neo chooses a provider-aware default.

In the TUI, `/model` opens a model picker. It changes the active model for the current session and saves that session metadata.

## What To Be Careful About

- Provider adapters should translate, not decide product behavior.
- Retry and response parsing belong in provider packages.
- The core agent loop should not care whether a response came from Anthropic, OpenAI, OpenRouter, or Google.
- Subscription/Codex auth is experimental and should be documented carefully.

## Where To Look

- `internal/llm/provider.go`: provider-neutral types.
- `internal/llm/anthropic`: Anthropic adapter.
- `internal/llm/openai`: OpenAI adapters.
- `internal/llm/chatcompletions`: reusable OpenAI-compatible Chat Completions translation.
- `internal/llm/openrouter`: OpenRouter adapter wiring.
- `internal/llm/google`: Google Gemini adapter.
- `internal/auth`: subscription credential storage and refresh.
- `cmd/neo/main.go`: provider selection.
