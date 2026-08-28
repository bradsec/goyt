package core

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateFFmpegURL(t *testing.T) {
	cases := []struct {
		url string
		ok  bool
	}{
		{"https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip", true},
		{"http://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip", false},
		{"https://evil.example.com/ffmpeg.zip", false},
		{"https://www.gyan.dev.evil.com/x.zip", false},
	}
	for _, c := range cases {
		err := validateFFmpegURL(c.url)
		if (err == nil) != c.ok {
			t.Errorf("validateFFmpegURL(%q) ok=%v, want %v (err=%v)", c.url, err == nil, c.ok, err)
		}
	}
}

func TestExtractFFmpegBinaries(t *testing.T) {
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "ffmpeg.zip")

	f, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(f)
	entries := map[string]string{
		"ffmpeg-8.1.1-essentials_build/bin/ffmpeg.exe":  "FFMPEG-BYTES",
		"ffmpeg-8.1.1-essentials_build/bin/ffprobe.exe": "FFPROBE-BYTES",
		"ffmpeg-8.1.1-essentials_build/doc/readme.txt":  "ignore me",
	}
	for name, body := range entries {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	f.Close()

	dest := filepath.Join(dir, "out")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	ffmpegPath, err := extractFFmpegBinaries(zipPath, dest)
	if err != nil {
		t.Fatalf("extractFFmpegBinaries: %v", err)
	}
	if filepath.Base(ffmpegPath) != "ffmpeg.exe" {
		t.Errorf("expected ffmpeg.exe, got %s", ffmpegPath)
	}
	if got, _ := os.ReadFile(ffmpegPath); string(got) != "FFMPEG-BYTES" {
		t.Errorf("ffmpeg.exe content mismatch: %q", got)
	}
	if _, err := os.Stat(filepath.Join(dest, "ffprobe.exe")); err != nil {
		t.Errorf("ffprobe.exe not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "readme.txt")); !os.IsNotExist(err) {
		t.Errorf("non-bin file should not be extracted")
	}
}

func TestExtractZipEntryRejectsOversizedEntry(t *testing.T) {
	dir := t.TempDir()
	dst := filepath.Join(dir, "ffmpeg.exe")
	const original = "existing binary"
	if err := os.WriteFile(dst, []byte(original), 0o755); err != nil {
		t.Fatal(err)
	}

	f := &zip.File{FileHeader: zip.FileHeader{
		Name:               "build/bin/ffmpeg.exe",
		UncompressedSize64: ffmpegMaxExtractBytes + 1,
	}}
	err := extractZipEntry(f, dst)
	if err == nil {
		t.Fatal("extractZipEntry succeeded for oversized entry")
	}
	if !strings.Contains(err.Error(), "exceeds maximum extracted size") {
		t.Fatalf("extractZipEntry error = %q, want size limit error", err)
	}

	got, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != original {
		t.Errorf("destination content = %q, want %q", got, original)
	}
	temps, err := filepath.Glob(filepath.Join(dir, ".ffmpeg.exe.tmp-*"))
	if err != nil {
		t.Fatal(err)
	}
	if len(temps) != 0 {
		t.Errorf("temporary files left behind: %v", temps)
	}
}

func TestExtractZipEntryRejectsStreamBeyondLimit(t *testing.T) {
	var dst strings.Builder
	err := copyZipStream(&dst, strings.NewReader("too large"), "ffmpeg.exe", 4)
	if err == nil || !strings.Contains(err.Error(), "exceeds maximum extracted size") {
		t.Fatalf("copyZipStream error = %v, want size limit error", err)
	}
}
