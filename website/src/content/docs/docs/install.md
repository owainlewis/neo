---
title: Install
description: Install Neo from a verified GitHub release with one command.
editUrl: false
---

Neo has one supported installation path:

```bash
curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
```

The script detects macOS or Linux on AMD64 or ARM64, downloads the matching archive
from GitHub Releases, and verifies its SHA-256 checksum before installing it. The
installation stops if the archive or checksum cannot be downloaded, the checksum
entry is missing, no SHA-256 tool is available, or verification fails.

It installs into the first writable directory it finds from `~/.local/bin`,
`~/bin`, or `/usr/local/bin`.

```bash
# Pin a specific version
curl -fsSL .../install.sh | bash -s -- --version v1.2.3

# Install to a custom directory
curl -fsSL .../install.sh | bash -s -- --bin-dir /usr/local/bin
```

## Build for development

```bash
git clone https://github.com/owainlewis/neo.git
cd neo
just build
```

`just build` stamps the current git description into the binary as the version shown on the
splash screen. Run `just print-version` to preview the stamped value.

## Updating

Use the same channel you installed with: rerun the one-line installer, run
`brew upgrade --cask owainlewis/tap/neo`, or repeat the `go install` command.

Next: [Quick start](/docs/quick-start/).
