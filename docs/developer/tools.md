# Tools

Neo exposes a small built-in tool surface to the model.

| Tool | Surface | Description |
| --- | --- | --- |
| `agent` | Interactive chat | Spawn a fresh subagent with a self-contained prompt. `mode: "work"` is writable and serial; `mode: "inspect"` is read-only and parallel-safe. |
| `bash` | Chat and headless | Run a shell command via `/bin/bash -c`. Returns bounded combined stdout and stderr, retaining the start and end when truncated. |
| `edit_file` | Chat and headless | Replace exactly one occurrence of `old_string` with `new_string`. Fails if the old text is missing, appears more than once, or the file changed since the agent last read it. `old_string` matches the file's raw text, so the line-number gutter `read_file` adds must be stripped first. |
| `glob` | Chat and headless | Find files under the workspace root using a glob pattern. Supports `**` for recursive matches and returns structured JSON. |
| `grep` | Chat and headless | Search complete text lines up to 4 MiB under the workspace with a regular expression and return structured JSON matches. Longer lines return an explicit error. Very long match and context text is returned as a bounded excerpt. |
| `read_file` | Chat and headless | Read a file from disk, prefixing each line with its 1-indexed number and a tab so the numbers match what `grep` reports. Returns up to `tools.MaxOutputBytes` (64 KiB); use `offset` and `limit` to page through larger files. |
| `workflow` | Interactive chat | Create or update the visible workflow checklist. Neo attaches tool and subagent activity to the active item automatically. |
| `write_file` | Chat and headless | Write content to a file, creating parent directories. Overwrites the file if it exists. |

Independent inspect calls issued in one model response can run concurrently.
Inspect children receive only `read_file`, `grep`, and `glob`.

## Output size

`tools.MaxOutputBytes` (64 KiB, roughly 16k tokens) is the single limit on what
one tool call contributes to the transcript. `bash` truncates its own output
head-and-tail at that size so a failing command's trailing error survives;
`read_file` refuses above it and asks the model to page; the agent applies the
same cap as a backstop for any tool that does not bound itself. A single line
longer than the limit cannot be read by `read_file`, since pagination cuts on
line boundaries; use `bash` for that.

## Stale edits

`read_file` records each file's modification time and size. `edit_file` refuses
when they no longer match and tells the model to read the file again, which
catches a change the model could not observe: the user saving in an editor, a
`git checkout`, or a concurrent `work`-mode subagent. A file the agent has never
read is not stale and edits normally — the guard is for invisible changes, not
for model error. `write_file` and `edit_file` re-record after writing so the
agent's own writes are never mistaken for external ones.

`tools.NewFileTools` constructs `read_file`, `write_file`, and `edit_file`
sharing one record. Build a fresh set per agent so a subagent's reads never
satisfy the coordinator's edits. The check is a stat comparison, not a lock: a
writer racing between the check and the write still wins.

Recursive `grep` discovery and file reads use a rooted filesystem handle anchored
to the resolved workspace root. A discovered symlink that escapes that root is
rejected with an explicit error, including when an ordinary file is replaced by
an escaping symlink between discovery and reading. Relative and absolute
symlinks whose targets remain inside the workspace are searched under the link's
displayed path.

## Execution and confirmations

Neo relies on its VM or sandbox for security boundaries. Interactive users can
set `tool_approvals` to confirm exact tool names or Bash command prefixes. The
list is empty by default and is not applied to headless or child agents.
