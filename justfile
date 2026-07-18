# Neo justfile

# Version stamped into the binary at build time. Uses the nearest git tag
# (with dirty suffix if there are uncommitted changes), falling back to the
# short commit SHA, falling back to "dev" outside a git checkout.
version := `git describe --tags --always --dirty 2>/dev/null || echo dev`

# List available recipes
default:
    @just --list

# Build the neo binary (stamps Version via -ldflags)
build:
    go build -ldflags "-X main.Version={{version}}" -o neo ./cmd/neo

# Run neo (dev mode)
dev *args:
    go run -ldflags "-X main.Version={{version}}" ./cmd/neo {{ args }}

# Run all tests
test:
    go test ./...

# Run tests with verbose output
test-verbose:
    go test -v ./...

# Build the release-shaped binary and run stable local microbenchmarks
performance:
    ./scripts/performance.sh

# Install neo into a runnable bin directory (stamps Version)
install:
    #!/usr/bin/env bash
    set -euo pipefail

    on_path() {
      case ":$PATH:" in
        *":$1:"*) return 0 ;;
        *) return 1 ;;
      esac
    }

    can_use_dir() {
      local dir="$1"
      mkdir -p "$dir" 2>/dev/null && [ -w "$dir" ]
    }

    bin_dir="${GOBIN:-$(go env GOBIN)}"
    if [ -z "$bin_dir" ]; then
      for candidate in "$HOME/.local/bin" "$HOME/bin" "$(go env GOPATH)/bin" "/usr/local/bin"; do
        if on_path "$candidate" && can_use_dir "$candidate"; then
          bin_dir="$candidate"
          break
        fi
      done
    fi
    if [ -z "$bin_dir" ]; then
      for candidate in "$HOME/.local/bin" "$HOME/bin" "$(go env GOPATH)/bin" "/usr/local/bin"; do
        if can_use_dir "$candidate"; then
          bin_dir="$candidate"
          break
        fi
      done
    fi
    if [ -z "$bin_dir" ]; then
      echo "error: could not find a writable install directory" >&2
      exit 1
    fi

    GOBIN="$bin_dir" go install -ldflags "-X main.Version={{version}}" ./cmd/neo
    echo "installed neo to $bin_dir/neo"
    if ! on_path "$bin_dir"; then
      echo "warning: $bin_dir is not on your PATH" >&2
      echo "add this to your shell rc: export PATH=\"$bin_dir:\$PATH\"" >&2
    fi

# Validate the install script with bash -n (syntax check)
check-install-script:
    bash -n install.sh && echo "install.sh syntax OK"

# Remove the built binary
clean:
    rm -f neo

# Format all Go source files
fmt:
    gofmt -w .

# Run static checks
lint:
    go vet ./...
    golangci-lint run

# Generate developer docs
docs:
    go run ./cmd/neo-docs

# Check generated developer docs are current
docs-check:
    go run ./cmd/neo-docs --check

# Print the version that would be stamped (for debugging)
print-version:
    @echo {{version}}
