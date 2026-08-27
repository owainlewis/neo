#!/usr/bin/env bash
# Start Codex in a Docker Sandbox (sbx) for this repo, with host skills mounted.
#
# Usage: scripts/sandbox.sh [-- CODEX_ARGS...]
#
# Creates the sandbox on first run, reuses it after that. Skill mounts do not
# survive a sandbox restart, so they are reapplied on every launch.
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
SANDBOX="codex-$(basename "$REPO_ROOT")"
SKILLS_MOUNT="$HOME/.codex/sandbox/mount.sh"

command -v sbx >/dev/null || { echo "error: sbx not found on PATH" >&2; exit 1; }

cd "$REPO_ROOT"

# Bring the sandbox up detached. Workspaces can only be set at creation time,
# so an existing sandbox is started by name instead.
if sbx ls 2>/dev/null | awk '{print $1}' | grep -qx "$SANDBOX"; then
  echo "==> Starting existing sandbox $SANDBOX"
  sbx run --name "$SANDBOX" -d >/dev/null
else
  echo "==> Creating sandbox $SANDBOX"
  sbx run codex . --name "$SANDBOX" -d >/dev/null
fi

# Reapply skill mounts. They are dropped whenever the sandbox restarts.
if [ -x "$SKILLS_MOUNT" ]; then
  echo "==> Mounting skills"
  "$SKILLS_MOUNT" "$SANDBOX"
else
  echo "note: $SKILLS_MOUNT not found, skipping skill mounts" >&2
fi

echo "==> Attaching to $SANDBOX"
exec sbx run --name "$SANDBOX" "$@"
