# Agent Loop

## The Simple Idea

The agent loop is Neo's heartbeat. A user sends a message, the model answers, Neo runs any requested tools, then the model continues until it has a final answer.

Think of it like:

1. User asks for work.
2. Model decides what to do.
3. Neo runs tools the model asked for.
4. Tool results go back to the model.
5. Repeat until the model is done.

## The Problem

Coding agents need more than one model response. A model might first need to inspect files, then run a command, then edit a file, then run tests. That means one user turn can contain several model/tool steps.

The tricky part is preserving the transcript correctly. If the model asks for a tool, the next provider request must include the matching tool result. Splitting those apart can break provider APIs and confuse the model.

## How Neo Solves It

The core loop lives in `internal/agent`. It is deliberately policy-free:

- It stores messages.
- It calls an `llm.Provider`.
- It emits events for the TUI.
- It runs injected tools.
- It appends tool results before continuing.

It does not know about coding style, AGENTS.md, skills, sandbox policy, or the
terminal UI. Those are layered around it. It accepts a small optional callback
that marks interactive tool calls requiring confirmation so they remain serial.

## The Important Invariant

Every assistant `tool_use` must be followed by a matching user `tool_result` before the transcript is sent back to the provider.

Neo builds the assistant message and tool results together before committing them to the transcript. That keeps the conversation valid even when a tool fails.

Oversized tool output is capped at the agent boundary before it enters the transcript or session payload. The capped content includes a visible truncation marker with the original byte size and line count.

## Who Owns A Message

The transcript is the agent's, and nothing outside it holds a reference into it.
Every crossing is a deep copy of the JSON-like values a tool schema can produce:

- a tool receives its own copy of its input, so mutating it cannot rewrite the
  assistant `tool_use` that is already in the transcript;
- `Transcript()` returns a copy that can be mutated freely;
- event `Args` and `ToolCallRef.Args` are copies, so a UI consumer cannot reach
  back into agent state.

Nested maps and slices, `Raw` replay bytes, and `ImageSource` pointers are all
copied — a struct copy alone leaves the last two shared. The copy is a small
recursive walk rather than a JSON round trip: it allocates only what the payload
contains and cannot fail.

The `Tool` interface does not forbid mutating its input, and it should not have
to. Relying on every current and future tool and event consumer to be careful
would make the transcript invariant implicit; copying at the boundary makes it
structural.

## Stop Reasons Fail Closed

The loop only continues on a stop reason it knows how to act on. Anything else
ends the turn with a typed error rather than re-asking the provider:

| Stop reason | Behaviour |
| --- | --- |
| `end_turn`, `stop_sequence`, `""` | Turn complete. |
| `tool_use` with tool calls | Run them, append results, continue. |
| `tool_use` with **no** tool call | `ErrMissingToolCall`. Nothing to run means nothing to reply with, so the next request would be identical and the loop would spin to `MaxTurns`. |
| `max_tokens` | `ErrMaxOutputTokens`, keeping the partial text. |
| `model_context_window_exceeded` | `ErrContextWindowExceeded`, keeping the partial text. |
| `refusal` | Turn complete, keeping the text. |
| `pause_turn` | Continue; the provider asked for another round. |
| anything unrecognised | `ErrUnexpectedStopReason`. |

Every terminating branch preserves the response's text but never its tool calls:
an unmatched `tool_use` in the transcript would make the next request invalid.

Adapter bugs and provider dialect differences are the reason for the strictness.
A provider-neutral loop cannot assume every backend reports stop reasons the
same way, so an unfamiliar one must cost a single call, not five hundred.

## How To Extend It

Add behavior around the loop, not inside it, unless the behavior is truly provider/tool-turn mechanics.

Good extensions:

- Add a new tool to `internal/tools` and inject it through the registry.
- Add a new provider that implements `llm.Provider`.
- Add prompt context before constructing the agent.
- Add a compactor through the `compact.Compactor` interface.

Risky extensions:

- Teaching the loop about project files.
- Putting UI behavior in the loop.
- Making the loop classify command risk or enforce sandbox policy.

## Where To Look

- `internal/agent/agent.go`: the loop and event model.
- `internal/llm/provider.go`: provider-neutral message and tool types.
- `internal/tools/`: built-in tools the loop can run.
