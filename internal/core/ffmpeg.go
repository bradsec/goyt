package core

import (
	"archive/zip"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// ffmpeg is auto-downloaded on Windows only. ffmpeg.org does not host binaries
// itself; gyan.dev is the Windows build it links, and it publishes a .sha256
// sidecar so the archive can be integrity-checked like the yt-dlp binary.
const (
	ffmpegWindowsURL    = "https://www.gyan.dev/ffmpeg/builds/ffmpeg-release-essentials.zip"
	ffmpegWindowsSHAURL = ffmpegWindowsURL + ".sha256"
	ffmpegDownloadHost  = "www.gyan.dev"

	// Generous per-file cap when extracting, as a zip-bomb guard. The static
	// ffmpeg.exe is large (~150 MB), so keep headroom.
	ffmpegMaxExtractBytes = 600 << 20
)

// FFmpegSourceURL returns the URL ffmpeg would be downloaded from, for display
// in the consent prompt.
func FFmpegSourceURL() string { return ffmpegWindowsURL }

// FFmpegAutoDownloadSupported reports whether automatic ffmpeg download is
// available for the current OS. Only Windows is supported; other platforms
// install ffmpeg through their package manager.
func FFmpegAutoDownloadSupported() bool {
	return runtime.GOOS == "windows"
}

// DownloadFFmpeg downloads the gyan.dev Windows release build, verifies its
// SHA-256, extracts ffmpeg.exe and ffprobe.exe into destDir, and returns the
// path to the installed ffmpeg.exe.
func DownloadFFmpeg(destDir string) (string, error) {
	if !FFmpegAutoDownloadSupported() {
		return "", fmt.Errorf("automatic ffmpeg download is only supported on Windows")
	}
	if err := validateFFmpegURL(ffmpegWindowsURL); err != nil {
		return "", err
	}
	if err := validateFFmpegURL(ffmpegWindowsSHAURL); err != nil {
		return "", err
	}

	expectedSum, err := fetchFFmpegChecksum(ffmpegWindowsSHAURL)
	if err != nil {
		return "", fmt.Errorf("failed to read ffmpeg checksum: %w", err)
	}

	zipPath, err := downloadToTemp(ffmpegWindowsURL)
	if err != nil {
		return "", fmt.Errorf("failed to download ffmpeg: %w", err)
	}
	defer os.Remove(zipPath)

	if err := verifyChecksum(zipPath, expectedSum); err != nil {
		return "", fmt.Errorf("ffmpeg checksum verification failed: %w", err)
	}

	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("failed to create ffmpeg directory: %w", err)
	}

	ffmpegPath, err := extractFFmpegBinaries(zipPath, destDir)
	if err != nil {
		return "", fmt.Errorf("failed to extract ffmpeg: %w", err)
	}
	return ffmpegPath, nil
}

// validateFFmpegURL pins the ffmpeg download to HTTPS on the expected host.
func validateFFmpegURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid ffmpeg URL: %w", err)
	}
	if u.Scheme != "https" {
		return fmt.Errorf("ffmpeg URL must use HTTPS")
	}
	if u.Hostname() != ffmpegDownloadHost {
		return fmt.Errorf("unexpected ffmpeg download host %q", u.Hostname())
	}
	return nil
}

// fetchFFmpegChecksum downloads the .sha256 sidecar and returns the lowercase
// hex digest. The file is "<hash>  <filename>" or just the hash.
func fetchFFmpegChecksum(url string) (string, error) {
	resp, err := newHTTPClient(30 * time.Second).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum fetch failed with status %d", resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", err
	}
	fields := strings.Fields(string(body))
	if len(fields) == 0 || len(fields[0]) != 64 {
		return "", fmt.Errorf("unexpected checksum format")
	}
	return strings.ToLower(fields[0]), nil
}

// downloadToTemp streams a URL to a temp file and returns its path.
func downloadToTemp(url string) (string, error) {
	resp, err := newHTTPClient(15 * time.Minute).Get(url)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	tmp, err := os.CreateTemp("", "ffmpeg-download-*.zip")
	if err != nil {
		return "", err
	}
	name := tmp.Name()
	written, err := io.Copy(tmp, resp.Body)
	if err != nil {
		tmp.Close()
		os.Remove(name)
		return "", err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return "", err
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		os.Remove(name)
		return "", fmt.Errorf("incomplete download: got %d bytes, expected %d", written, resp.ContentLength)
	}
	return name, nil
}

// extractFFmpegBinaries pulls ffmpeg.exe and ffprobe.exe out of the archive's
// bin/ directory into destDir and returns the installed ffmpeg.exe path. Output
// names are fixed (no archive-controlled paths) so there is no zip-slip risk.
func extractFFmpegBinaries(zipPath, destDir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", err
	}
	defer zr.Close()

	wanted := map[string]bool{"ffmpeg.exe": true, "ffprobe.exe": true}
	found := map[string]string{}

	for _, f := range zr.File {
		name := strings.ToLower(path.Base(f.Name))
		if !wanted[name] || !strings.Contains(strings.ToLower(f.Name), "/bin/") {
			continue
		}
		if _, done := found[name]; done {
			continue
		}
		dst := filepath.Join(destDir, name)
		if err := extractZipEntry(f, dst); err != nil {
			return "", err
		}
		found[name] = dst
	}

	if found["ffmpeg.exe"] == "" {
		return "", fmt.Errorf("ffmpeg.exe not found in archive")
	}
	return found["ffmpeg.exe"], nil
}

// extractZipEntry writes a single zip entry to dst via a temp file and rename.
func extractZipEntry(f *zip.File, dst string) error {
	rc, err := f.Open()
	if err != nil {
		return err
	}
	defer rc.Close()

	tmp, err := os.CreateTemp(filepath.Dir(dst), "."+filepath.Base(dst)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()

	if _, err := io.Copy(tmp, io.LimitReader(rc, ffmpegMaxExtractBytes)); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, dst); err != nil {
		os.Remove(tmpName)
		return err
	}
	return nil
}
