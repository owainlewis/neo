# Neo justfile

# List available recipes
default:
    @just --list

# Build the neo binary
build:
    go build -o neo ./cmd/neo

# Run neo (dev mode)
dev *args:
    go run ./cmd/neo {{ args }}

# Run all tests
test:
    go test ./...

# Run tests with verbose output
test-verbose:
    go test -v ./...

# Install neo onto $GOBIN
install:
    go install ./cmd/neo

# Remove the built binary
clean:
    rm -f neo

# Format all Go source files
fmt:
    gofmt -w .

# Run go vet
lint:
    go vet ./...
