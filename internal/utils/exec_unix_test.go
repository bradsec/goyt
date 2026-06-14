//go:build !windows

package utils

import (
	"os"
	"path/filepath"
	"testing"
)

func TestValidateExecutablePathPermissions(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "tool")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o644); err != nil {
		t.Fatalf("write file: %v", err)
	}

	// Non-executable regular file is rejected.
	if err := ValidateExecutablePath(path); err == nil {
		t.Error("expected error for non-executable file, got nil")
	}

	// Executable bit set: accepted.
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("chmod: %v", err)
	}
	if err := ValidateExecutablePath(path); err != nil {
		t.Errorf("expected executable file to validate, got %v", err)
	}
}
