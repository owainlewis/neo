# Tools

Neo exposes a small built-in tool surface to the model.

| Tool | Description |
| --- | --- |
| `agent` | Spawn a fresh subagent with a self-contained prompt. `mode: "work"` is writable and serial; `mode: "inspect"` is read-only and parallel-safe. |
| `bash` | Run a shell command via /bin/bash -c. Returns bounded combined stdout+stderr, retaining the start and end when truncated. Use for git, tests, builds, file inspection beyond Read. |
| `edit_file` | Replace exactly one occurrence of old_string with new_string in a file. Fails if old_string is missing or appears more than once. |
| `glob` | Find files under the workspace root using a glob pattern. Supports ** for recursive matches. Returns JSON: {matches:[path],truncated,count}. |
| `grep` | Search text files under the workspace with a regular expression. Returns JSON: {matches:[{path,line,text,context_before?,context_after?}],truncated,count}. |
| `read_file` | Read a file from disk. Returns up to ~256KB. Use offset/limit (1-indexed line numbers) to page through larger files. |
| `workflow` | Create or update the visible workflow checklist. Use for multi-step tasks; Neo attaches tool and subagent activity automatically. |
| `write_file` | Write content to a file, creating parent directories. Overwrites if exists. |

Independent inspect calls issued in one model response can run concurrently.
Inspect children receive only `read_file`, `grep`, and `glob`.
