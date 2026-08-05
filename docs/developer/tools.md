# Tools

Neo exposes a small built-in tool surface to the model.

| Tool | Surface | Description |
| --- | --- | --- |
| `agent` | Interactive chat | Spawn a fresh subagent with a self-contained prompt. `mode: "work"` is writable and serial; `mode: "inspect"` is read-only and parallel-safe. |
| `bash` | Chat and headless | Run a shell command via `/bin/bash -c`. Returns bounded combined stdout and stderr, retaining the start and end when truncated. |
| `edit_file` | Chat and headless | Replace exactly one occurrence of `old_string` with `new_string`. Fails if the old text is missing or appears more than once. |
| `glob` | Chat and headless | Find files under the workspace root using a glob pattern. Supports `**` for recursive matches and returns structured JSON. |
| `grep` | Chat and headless | Search complete text lines up to 4 MiB under the workspace with a regular expression and return structured JSON matches. Longer lines return an explicit error. Very long match and context text is returned as a bounded excerpt. |
| `read_file` | Chat and headless | Read a file from disk. Returns up to about 256 KiB. Use `offset` and `limit` with 1-indexed line numbers to page through larger files. |
| `workflow` | Interactive chat | Create or update the visible workflow checklist. Neo attaches tool and subagent activity to the active item automatically. |
| `write_file` | Chat and headless | Write content to a file, creating parent directories. Overwrites the file if it exists. |

Independent inspect calls issued in one model response can run concurrently.
Inspect children receive only `read_file`, `grep`, and `glob`.

## Execution and confirmations

Neo relies on its VM or sandbox for security boundaries. Interactive users can
set `tool_approvals` to confirm exact tool names or Bash command prefixes. The
list is empty by default and is not applied to headless or child agents.
