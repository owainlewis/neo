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

# Install neo onto $GOBIN (stamps Version)
install:
    go install -ldflags "-X main.Version={{version}}" ./cmd/neo

# Validate the install script with bash -n (syntax check)
check-install-script:
    bash -n install.sh && echo "install.sh syntax OK"

# Remove the built binary
clean:
    rm -f neo

# Format all Go source files
fmt:
    gofmt -w .

# Run go vet
lint:
    go vet ./...

# Generate developer docs
docs:
    go run ./cmd/neo-docs

# Check generated developer docs are current
docs-check:
    go run ./cmd/neo-docs --check

# Print the version that would be stamped (for debugging)
print-version:
    @echo {{version}}
