#!/bin/bash
# Build script for goyt

set -e

echo "Building goyt..."

# Clean previous builds
echo "Cleaning previous builds..."
rm -f goyt goyt.exe

# Format code
echo "Formatting Go code..."
go fmt ./...

# Run linters (if available)
if command -v golangci-lint &> /dev/null; then
    echo "Running linters..."
    golangci-lint run
fi

# Run tests
echo "Running tests..."
go test -v -race -coverprofile=coverage.out ./...

# Determine version from git tag, falling back to package.json
VERSION=$(git describe --tags --exact-match 2>/dev/null || node -e "process.stdout.write(require('./package.json').version)" 2>/dev/null || echo "dev")
echo "Version: ${VERSION}"
LDFLAGS="-s -w -X goyt/internal/api.Version=${VERSION}"

# Build for current platform
echo "Building for current platform..."
go build -ldflags="${LDFLAGS}" -o goyt ./cmd/goyt

# Cross-compile for common platforms
echo "Cross-compiling for common platforms..."
GOOS=windows GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o goyt.exe ./cmd/goyt
GOOS=linux GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o goyt-linux ./cmd/goyt
GOOS=darwin GOARCH=amd64 go build -ldflags="${LDFLAGS}" -o goyt-darwin ./cmd/goyt

echo "Build complete!"
echo "Binaries created:"
ls -la goyt*