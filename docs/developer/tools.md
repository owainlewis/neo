# Tools

Neo exposes a small built-in tool surface to the model.

| Tool | Surface | Description |
| --- | --- | --- |
| `agent` | Interactive chat | Spawn a fresh subagent with a self-contained prompt. `mode: "work"` is writable and serial; `mode: "inspect"` is read-only and parallel-safe. |
| `bash` | Chat and headless | Run a shell command via `/bin/bash -c`. Returns bounded combined stdout and stderr, retaining the start and end when truncated. |
| `edit_file` | Chat and headless | Replace exactly one occurrence of `old_string` with `new_string`. Fails if the old text is missing, appears more than once, or the file changed since the agent last read it. `old_string` matches the file's raw text, so the line-number gutter `read_file` adds must be stripped first. |
| `glob` | Chat and headless | List workspace files matching a glob pattern, honouring `.gitignore`. Supports `**` for recursive matches. Returns one path per line. |
| `grep` | Chat and headless | Search the workspace with a regular expression, honouring `.gitignore`. Returns matching lines as `path:line:text`. |
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

## Search

`grep` and `glob` shell out to [ripgrep](https://github.com/BurntSushi/ripgrep),
which honours `.gitignore`, skips binaries, and is far faster than a hand-rolled
walk. `--no-require-git` is passed so ignore rules apply whether or not the
workspace is a git checkout.

If `rg` is not on `PATH`, both tools return an error telling the model to use
`bash` with `grep` or `find` instead. There is deliberately no Go fallback: a
second implementation would mean two sets of ignore rules and two output shapes.
`neo doctor` reports ripgrep as a warning, not a failure, for the same reason.

They stay separate tools rather than folding into `bash` because they are
classified parallel-safe, which `bash` cannot be without interpreting shell
commands, and because inspect-mode subagents need read-only search without a
shell.

Search applies no path confinement of its own, matching `read_file`,
`write_file`, and `edit_file`. The sandbox is the boundary.

Cancelling a search kills the ripgrep process and returns an error with no
partial output.

## Execution and confirmations

Neo relies on its VM or sandbox for security boundaries. Interactive users can
set `tool_approvals` to confirm exact tool names or Bash command prefixes. The
list is empty by default and is not applied to headless or child agents.
