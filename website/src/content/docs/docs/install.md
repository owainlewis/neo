---
title: Install
description: Install Neo with the one-line installer, Homebrew, go install, or a manual build.
editUrl: false
---

Choose the path that fits your setup.

| Method | Best for | Command |
|------|------|------|
| One-line installer | Most users; downloads a release binary when available | `curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh \| bash` |
| Homebrew | macOS users already using Homebrew | `brew install --cask owainlewis/tap/neo` |
| `go install` | Go users who want Neo on their existing `$GOBIN` path | `go install github.com/owainlewis/neo/cmd/neo@latest` |
| Manual build | Contributors or anyone who wants a local checkout | `just build` or `go build -o neo ./cmd/neo` |

## One-line installer

```bash
curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
```

The script auto-detects your OS and architecture, downloads the matching pre-built release archive
from GitHub Releases, verifies its checksum when available, and installs it into the first
writable directory it finds from `~/.local/bin`, `~/bin`, or `/usr/local/bin`. If no pre-built
binary is available for your platform it falls back to `go install` (requires Go 1.25+).

```bash
# Pin a specific version
curl -fsSL .../install.sh | bash -s -- --version v1.2.3

# Install to a custom directory
curl -fsSL .../install.sh | bash -s -- --bin-dir /usr/local/bin
```

## Homebrew

```bash
brew install --cask owainlewis/tap/neo
```

## `go install`

```bash
go install github.com/owainlewis/neo/cmd/neo@latest
neo
```

## Manual build

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
just build                          # or: go build -o neo ./cmd/neo
```

`just build` stamps the current git description into the binary as the version shown on the
splash screen. Run `just print-version` to preview the stamped value.

Next: [Quick start](/docs/quick-start/).
