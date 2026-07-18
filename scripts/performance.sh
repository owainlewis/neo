#!/usr/bin/env bash
set -euo pipefail

repo_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
build_dir="$(mktemp -d "${TMPDIR:-/tmp}/neo-performance.XXXXXX")"
trap 'rm -rf "$build_dir"' EXIT

cd "$repo_dir"

goos="$(go env GOOS)"
goarch="$(go env GOARCH)"
goversion="$(go env GOVERSION)"
binary="$build_dir/neo"

CGO_ENABLED=0 go build \
  -trimpath \
  -ldflags "-s -w -X main.Version=benchmark" \
  -o "$binary" \
  ./cmd/neo

binary_bytes="$(wc -c < "$binary" | tr -d '[:space:]')"
binary_budget_bytes=$((20 * 1024 * 1024))

echo "Neo performance baseline"
echo "go: $goversion"
echo "target: $goos/$goarch"
echo "stripped_binary_bytes: $binary_bytes"
echo "stripped_binary_budget_bytes: $binary_budget_bytes"
echo

if (( binary_bytes > binary_budget_bytes )); then
  echo "error: stripped binary exceeds its size budget" >&2
  exit 1
fi

go test \
  -run '^$' \
  -bench 'Benchmark(ChatSystem|WorkflowRender|RegistrySpecs)$' \
  -benchmem \
  -count "${BENCH_COUNT:-5}" \
  ./cmd/neo ./internal/tui ./internal/tools
