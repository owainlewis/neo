#!/usr/bin/env bash
# Neo install script
# Usage:
#   curl -fsSL https://raw.githubusercontent.com/owainlewis/neo/main/install.sh | bash
#   bash install.sh [--bin-dir <dir>] [--version <tag>] [--no-api-key-check]
set -euo pipefail

# ── Colour helpers ──────────────────────────────────────────────────────────
if [ -t 1 ] && command -v tput &>/dev/null && tput colors &>/dev/null; then
  BOLD=$(tput bold); DIM=$(tput dim); RESET=$(tput sgr0)
  RED=$(tput setaf 1); GREEN=$(tput setaf 2)
  YELLOW=$(tput setaf 3); CYAN=$(tput setaf 6)
else
  BOLD=""; DIM=""; RESET=""; RED=""; GREEN=""; YELLOW=""; CYAN=""
fi

info()    { printf "%s  %s%s\n"        "${CYAN}→${RESET}"  "$*" "${RESET}"; }
success() { printf "%s  %s%s\n"        "${GREEN}✓${RESET}" "$*" "${RESET}"; }
warn()    { printf "%s  %s%s\n"        "${YELLOW}⚠${RESET}" "$*" "${RESET}"; }
die()     { printf "%s  %s%s\n" >&2    "${RED}✗${RESET}"   "$*" "${RESET}"; exit 1; }
header()  { printf "\n%s%s%s\n\n"      "${BOLD}" "$*" "${RESET}"; }

# ── Defaults ────────────────────────────────────────────────────────────────
REPO="owainlewis/neo"
BIN_NAME="neo"
VERSION=""          # empty = latest release tag
BIN_DIR=""          # empty = auto-detect
CHECK_API_KEY=true

# ── Argument parsing ─────────────────────────────────────────────────────────
while [[ $# -gt 0 ]]; do
  case "$1" in
    --bin-dir)        BIN_DIR="$2";     shift 2 ;;
    --version)        VERSION="$2";     shift 2 ;;
    --no-api-key-check) CHECK_API_KEY=false; shift ;;
    -h|--help)
      cat <<EOF
Usage: install.sh [options]

Options:
  --bin-dir <dir>       Directory to install neo into  (default: ~/.local/bin)
  --version <tag>       Release tag to install         (default: latest)
  --no-api-key-check    Skip ANTHROPIC_API_KEY reminder
  -h, --help            Show this help

EOF
      exit 0 ;;
    *) die "Unknown option: $1" ;;
  esac
done

# ── Platform detection ───────────────────────────────────────────────────────
detect_platform() {
  local os arch

  case "$(uname -s)" in
    Linux)  os="linux"  ;;
    Darwin) os="darwin" ;;
    *) die "Unsupported OS: $(uname -s)" ;;
  esac

  case "$(uname -m)" in
    x86_64|amd64)  arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) die "Unsupported architecture: $(uname -m)" ;;
  esac

  echo "${os}_${arch}"
}

# ── Resolve install directory ─────────────────────────────────────────────────
resolve_bin_dir() {
  if [[ -n "$BIN_DIR" ]]; then
    echo "$BIN_DIR"
    return
  fi

  # Prefer a writable directory already on PATH
  local candidates=("$HOME/.local/bin" "$HOME/bin" "/usr/local/bin")
  for dir in "${candidates[@]}"; do
    if [[ -d "$dir" && -w "$dir" ]]; then
      echo "$dir"
      return
    fi
  done

  # Fall back to ~/.local/bin and create it
  echo "$HOME/.local/bin"
}

# ── Fetch latest tag from GitHub ──────────────────────────────────────────────
latest_version() {
  local tag
  if command -v curl &>/dev/null; then
    tag=$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" \
          | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\(.*\)".*/\1/')
  elif command -v wget &>/dev/null; then
    tag=$(wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" \
          | grep '"tag_name"' | head -1 | sed 's/.*"tag_name": *"\(.*\)".*/\1/')
  else
    die "Neither curl nor wget is available. Install one and retry."
  fi

  [[ -n "$tag" ]] || die "Could not determine latest release. Use --version to pin one."
  echo "$tag"
}

# ── Download helper ───────────────────────────────────────────────────────────
download() {
  local url="$1" dest="$2"
  if command -v curl &>/dev/null; then
    curl -fsSL --progress-bar "$url" -o "$dest"
  else
    wget -q --show-progress "$url" -O "$dest"
  fi
}

# ── Checksum helper ──────────────────────────────────────────────────────────
verify_checksum() {
  local file="$1" asset="$2" version="$3" tmp_dir="$4"
  local checksums="${tmp_dir}/checksums.txt"
  local url="https://github.com/${REPO}/releases/download/${version}/checksums.txt"

  download "$url" "$checksums" 2>/dev/null || \
    die "Could not download checksums.txt for ${version}; refusing to install an unverified binary"

  local expected
  expected=$(awk -v asset="$asset" '$2 == asset { print $1; exit }' "$checksums")
  [[ -n "$expected" ]] || \
    die "No checksum found for ${asset}; refusing to install an unverified binary"

  local actual
  if command -v sha256sum &>/dev/null; then
    actual=$(sha256sum "$file" | awk '{print $1}')
  elif command -v shasum &>/dev/null; then
    actual=$(shasum -a 256 "$file" | awk '{print $1}')
  else
    die "SHA-256 verification requires sha256sum or shasum"
  fi

  [[ "$actual" == "$expected" ]] || die "Checksum mismatch for ${asset}"
  success "Verified checksum"
}

# ── Install via pre-built release binary ─────────────────────────────────────
install_from_release() {
  local version="$1" platform="$2" bin_dir="$3"

  # Expected GoReleaser asset name pattern: neo_<os>_<arch>.tar.gz
  local asset="neo_${platform}.tar.gz"
  local url="https://github.com/${REPO}/releases/download/${version}/${asset}"
  local tmp_dir
  tmp_dir=$(mktemp -d)
  trap "rm -rf '$tmp_dir'" EXIT

  info "Downloading ${asset} (${version})…"
  download "$url" "${tmp_dir}/${asset}" 2>/dev/null || \
    die "Could not download release asset: ${url}"

  verify_checksum "${tmp_dir}/${asset}" "$asset" "$version" "$tmp_dir"

  command -v tar &>/dev/null || die "tar is required to extract ${asset}"
  tar -xzf "${tmp_dir}/${asset}" -C "$tmp_dir"

  local extracted="${tmp_dir}/${BIN_NAME}"
  if [[ ! -f "$extracted" ]]; then
    extracted=$(find "$tmp_dir" -type f -name "${BIN_NAME}" | head -n 1)
  fi
  [[ -n "$extracted" && -f "$extracted" ]] || die "Archive did not contain ${BIN_NAME}"

  mkdir -p "$bin_dir"
  install -m 0755 "$extracted" "${bin_dir}/${BIN_NAME}"
  success "Installed ${BIN_NAME} → ${bin_dir}/${BIN_NAME}"
}

# ── PATH nudge ────────────────────────────────────────────────────────────────
check_path() {
  local bin_dir="$1"
  if ! echo "$PATH" | tr ':' '\n' | grep -qx "$bin_dir"; then
    echo ""
    warn "${bin_dir} is not on your \$PATH."
    echo "    Add this line to your shell rc file:"
    echo ""
    printf "    %sexport PATH=\"%s:\$PATH\"%s\n" "${BOLD}" "$bin_dir" "${RESET}"
    echo ""
    echo "    Then reload your shell:  source ~/.bashrc  (or ~/.zshrc)"
  fi
}

# ── API key reminder ──────────────────────────────────────────────────────────
check_api_key() {
  if [[ "$CHECK_API_KEY" == false ]]; then return; fi

  if [[ -z "${ANTHROPIC_API_KEY:-}" ]]; then
    echo ""
    warn "ANTHROPIC_API_KEY is not set."
    echo "    Get a key at: https://console.anthropic.com/"
    echo "    Then export it:"
    echo ""
    printf "    %sexport ANTHROPIC_API_KEY=\"sk-ant-...\"%s\n" "${BOLD}" "${RESET}"
  else
    success "ANTHROPIC_API_KEY is set"
  fi
}

# ── Main ──────────────────────────────────────────────────────────────────────
main() {
  header "Installing Neo"

  local platform
  platform=$(detect_platform)
  info "Detected platform: ${platform}"

  local bin_dir
  bin_dir=$(resolve_bin_dir)

  if [[ -z "$VERSION" ]]; then
    info "Resolving latest release…"
    VERSION=$(latest_version)
  fi
  info "Version: ${VERSION}"

  install_from_release "$VERSION" "$platform" "$bin_dir"

  check_path "$bin_dir"
  check_api_key

  echo ""
  success "Done!  Run ${BOLD}neo${RESET}${GREEN} to start."
  echo ""
}

main "$@"
