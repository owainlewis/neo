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

It uses the first existing writable directory from `~/.local/bin`, `~/bin`, or
`/usr/local/bin`. If none qualifies, it creates and uses `~/.local/bin`. The
installer warns when the selected directory is not on `PATH`.

## Optional: ripgrep

Neo is a single binary with no required runtime. The `grep` and `glob` tools do
need [ripgrep](https://github.com/BurntSushi/ripgrep) (`rg`) on your `PATH`;
without it they return an error and the agent falls back to `bash`. Run
`neo doctor` to check.

```bash
brew install ripgrep      # macOS
apt install ripgrep       # Debian, Ubuntu
```

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

Rerun the one-line installer. It resolves the latest stable GitHub release and
replaces the installed binary after checksum verification. Use `--version` to
install or return to a specific release.

Next: [Quick start](/docs/quick-start/).
