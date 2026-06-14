package core

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeTempFile(t *testing.T, content []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "bin")
	if err := os.WriteFile(path, content, 0o644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}
	return path
}

func TestVerifyChecksumMatch(t *testing.T) {
	content := []byte("yt-dlp-binary-bytes")
	path := writeTempFile(t, content)
	sum := sha256.Sum256(content)

	if err := verifyChecksum(path, hex.EncodeToString(sum[:])); err != nil {
		t.Fatalf("expected match, got %v", err)
	}
	// Hash comparison must be case-insensitive.
	upper := fmt.Sprintf("%X", sum)
	if err := verifyChecksum(path, upper); err != nil {
		t.Fatalf("expected case-insensitive match, got %v", err)
	}
}

func TestVerifyChecksumMismatch(t *testing.T) {
	path := writeTempFile(t, []byte("real-bytes"))
	wrong := hex.EncodeToString(sha256.New().Sum(nil))
	if err := verifyChecksum(path, wrong); err == nil {
		t.Fatal("expected mismatch error, got nil")
	}
}

func TestVerifyChecksumEmptyExpected(t *testing.T) {
	path := writeTempFile(t, []byte("bytes"))
	if err := verifyChecksum(path, ""); err == nil {
		t.Fatal("expected error for empty checksum, got nil")
	}
}

func TestParseChecksumManifest(t *testing.T) {
	content := []byte("binary")
	sum := sha256.Sum256(content)
	manifest := fmt.Sprintf("%s  yt-dlp\n%s  yt-dlp.exe\n",
		hex.EncodeToString(sum[:]), hex.EncodeToString(sha256.New().Sum(nil)))

	got, err := parseChecksumManifest(strings.NewReader(manifest), "yt-dlp")
	if err != nil {
		t.Fatalf("parseChecksumManifest: %v", err)
	}
	if got != hex.EncodeToString(sum[:]) {
		t.Errorf("expected %s, got %s", hex.EncodeToString(sum[:]), got)
	}
}

func TestParseChecksumManifestMissingEntry(t *testing.T) {
	if _, err := parseChecksumManifest(strings.NewReader("abc123  some-other-file\n"), "yt-dlp"); err == nil {
		t.Fatal("expected error for missing entry, got nil")
	}
}

func TestInstallBinaryAtomicAndExecutable(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "yt-dlp")
	u := NewYtDlpUpdater(binPath, dir)

	content := []byte("#!/bin/sh\necho new\n")
	src := writeTempFile(t, content)
	if err := u.installBinary(src); err != nil {
		t.Fatalf("installBinary: %v", err)
	}

	got, err := os.ReadFile(u.binPath)
	if err != nil {
		t.Fatalf("read installed binary: %v", err)
	}
	if string(got) != string(content) {
		t.Errorf("installed content mismatch")
	}
	info, err := os.Stat(u.binPath)
	if err != nil {
		t.Fatalf("stat installed binary: %v", err)
	}
	if info.Mode().Perm()&0o111 == 0 {
		t.Errorf("installed binary is not executable: mode %v", info.Mode().Perm())
	}
	// No staging or backup files should be left behind.
	entries, _ := os.ReadDir(u.assetsDir)
	for _, e := range entries {
		if strings.Contains(e.Name(), "staged") || strings.HasSuffix(e.Name(), ".backup") {
			t.Errorf("leftover install artifact: %s", e.Name())
		}
	}
}

func TestInstallBinaryReplacesExisting(t *testing.T) {
	dir := t.TempDir()
	binPath := filepath.Join(dir, "yt-dlp")
	if err := os.WriteFile(binPath, []byte("old"), 0o755); err != nil {
		t.Fatalf("seed existing binary: %v", err)
	}
	u := NewYtDlpUpdater(binPath, dir)

	src := writeTempFile(t, []byte("updated"))
	if err := u.installBinary(src); err != nil {
		t.Fatalf("installBinary: %v", err)
	}
	got, _ := os.ReadFile(u.binPath)
	if string(got) != "updated" {
		t.Errorf("expected replaced content 'updated', got %q", got)
	}
	if _, err := os.Stat(binPath + ".backup"); !os.IsNotExist(err) {
		t.Errorf("backup file was not cleaned up")
	}
}
