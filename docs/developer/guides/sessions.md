# Sessions

## The Simple Idea

A session is a saved conversation. It lets Neo stop and later continue with the same transcript.

In Codex terms, it is close to a thread.

## The Problem

Coding work often takes more than one terminal run. Without sessions, every restart loses the transcript, tool results, model choice, cwd, and title.

Neo needs a durable place to store that conversation.

## How Neo Solves It

Neo stores sessions under `~/.neo/sessions/`:

- `index.json`: metadata for listing.
- `<session-id>.json`: full transcript and metadata.

The TUI saves after each user turn. Resuming a session restores the old messages into the agent.
Session metadata stores stable configuration provider IDs. In particular, both
OpenAI API-key and subscription sessions persist `openai`; older
`openai-codex` metadata is normalized to `openai` during resume.

## Ways To Resume

From the shell:

```bash
neo sessions
neo resume <session-id>
```

## Why CWD Matters

Tools are created for the workspace when the TUI starts. Resume sessions from
the shell with `neo resume <id>` so Neo can restore the saved cwd before
creating them.

## Where To Look

- `internal/session/session.go`: file-backed session store.
- `cmd/neo/main.go`: create, list, resume, and save wiring.
