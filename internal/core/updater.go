package core

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"goyt/internal/utils"
)

const (
	GitHubAPIURL   = "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest"
	UpdateCheckURL = "https://github.com/yt-dlp/yt-dlp/releases/latest"
	VersionUnknown = "unknown"
)

type GitHubRelease struct {
	TagName string `json:"tag_name"`
	Assets  []struct {
		Name               string `json:"name"`
		BrowserDownloadURL string `json:"browser_download_url"`
	} `json:"assets"`
}

type UpdateInfo struct {
	CurrentVersion  string    `json:"current_version"`
	LatestVersion   string    `json:"latest_version"`
	UpdateAvailable bool      `json:"update_available"`
	LastChecked     time.Time `json:"last_checked"`
}

type YtDlpUpdater struct {
	binPath      string
	assetsDir    string
	versionCache *UpdateInfo
}

func NewYtDlpUpdater(binPath, assetsDir string) *YtDlpUpdater {
	// ValidateExecutablePath requires absolute paths, and config defaults use
	// relative ones, so resolve here once.
	if abs, err := filepath.Abs(binPath); err == nil {
		binPath = abs
	}
	if abs, err := filepath.Abs(assetsDir); err == nil {
		assetsDir = abs
	}
	return &YtDlpUpdater{
		binPath:   binPath,
		assetsDir: assetsDir,
	}
}

// httpsOnlyRedirect refuses any redirect hop that is not HTTPS so a download
// cannot be downgraded to plain HTTP mid-flight, while still allowing GitHub's
// cross-host redirect to its asset CDN (finding UP-1).
func httpsOnlyRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("stopped after 10 redirects")
	}
	if req.URL.Scheme != "https" {
		return fmt.Errorf("refusing non-https redirect to %q", req.URL.Scheme)
	}
	return nil
}

// newHTTPClient returns an HTTP client with an explicit timeout and the
// HTTPS-only redirect policy (findings UP-1, UP-2).
func newHTTPClient(timeout time.Duration) *http.Client {
	return &http.Client{Timeout: timeout, CheckRedirect: httpsOnlyRedirect}
}

func (u *YtDlpUpdater) GetCurrentVersion() (string, error) {
	if _, err := os.Stat(u.binPath); os.IsNotExist(err) {
		return "", fmt.Errorf("yt-dlp binary not found at %s", u.binPath)
	}

	// Validate binPath before executing (fixes G204)
	if err := utils.ValidateExecutablePath(u.binPath); err != nil {
		return VersionUnknown, fmt.Errorf("invalid binary path: %w", err)
	}

	// Try to get version from yt-dlp --version
	// #nosec G204 - binPath is validated by ValidateExecutablePath above
	cmd := exec.Command(u.binPath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return VersionUnknown, fmt.Errorf("yt-dlp --version failed: %w", err)
	}

	version := strings.TrimSpace(string(output))
	return version, nil
}

func (u *YtDlpUpdater) GetLatestVersion() (string, error) {
	// Validate URL before making HTTP request (fixes G107)
	if err := utils.ValidateURL(GitHubAPIURL); err != nil {
		return "", fmt.Errorf("invalid GitHub API URL: %w", err)
	}

	resp, err := newHTTPClient(30 * time.Second).Get(GitHubAPIURL)
	if err != nil {
		return "", fmt.Errorf("failed to fetch latest release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", fmt.Errorf("failed to parse GitHub API response: %w", err)
	}

	return release.TagName, nil
}

func (u *YtDlpUpdater) CheckForUpdates() (*UpdateInfo, error) {
	currentVersion, err := u.GetCurrentVersion()
	if err != nil {
		currentVersion = VersionUnknown
	}

	latestVersion, err := u.GetLatestVersion()
	if err != nil {
		return nil, fmt.Errorf("failed to get latest version: %w", err)
	}

	// An unreadable local version means the binary is missing or broken, so
	// treat it as needing an update rather than silently keeping it.
	updateInfo := &UpdateInfo{
		CurrentVersion:  currentVersion,
		LatestVersion:   latestVersion,
		UpdateAvailable: currentVersion == VersionUnknown || currentVersion != latestVersion,
		LastChecked:     time.Now(),
	}

	u.versionCache = updateInfo
	return updateInfo, nil
}

func (u *YtDlpUpdater) Update() error {
	// Validate URL before making HTTP request (fixes G107)
	if err := utils.ValidateURL(GitHubAPIURL); err != nil {
		return fmt.Errorf("invalid GitHub API URL: %w", err)
	}

	// Get latest release info
	resp, err := newHTTPClient(30 * time.Second).Get(GitHubAPIURL)
	if err != nil {
		return fmt.Errorf("failed to fetch latest release info: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GitHub API returned status %d", resp.StatusCode)
	}

	var release GitHubRelease
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse GitHub API response: %w", err)
	}

	// Find the appropriate binary for current OS
	targetName, downloadURL, err := u.getBinaryURL(release.Assets)
	if err != nil {
		return fmt.Errorf("failed to find binary for current OS: %w", err)
	}

	// Locate the published checksum manifest so the binary can be verified
	// before it is installed and executed.
	checksumURL, err := u.getChecksumURL(release.Assets)
	if err != nil {
		return fmt.Errorf("failed to find checksum manifest: %w", err)
	}
	expectedSum, err := u.fetchExpectedChecksum(checksumURL, targetName)
	if err != nil {
		return fmt.Errorf("failed to read expected checksum: %w", err)
	}

	// Download the binary
	tempFile, err := u.downloadBinary(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download binary: %w", err)
	}
	defer os.Remove(tempFile)

	// Verify integrity before install. A mismatch aborts the update so a
	// corrupted or tampered asset is never chmod+exec'd.
	if err := verifyChecksum(tempFile, expectedSum); err != nil {
		return fmt.Errorf("checksum verification failed: %w", err)
	}

	// Install the binary
	if err := u.installBinary(tempFile); err != nil {
		return fmt.Errorf("failed to install binary: %w", err)
	}

	return nil
}

func ytDlpAssetName() (string, error) {
	switch runtime.GOOS {
	case "windows":
		return "yt-dlp.exe", nil
	case "darwin":
		return "yt-dlp_macos", nil
	case "linux":
		return "yt-dlp", nil
	default:
		return "", fmt.Errorf("unsupported operating system: %s", runtime.GOOS)
	}
}

func (u *YtDlpUpdater) getBinaryURL(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}) (string, string, error) {
	targetName, err := ytDlpAssetName()
	if err != nil {
		return "", "", err
	}

	for _, asset := range assets {
		if asset.Name == targetName {
			return targetName, asset.BrowserDownloadURL, nil
		}
	}

	return "", "", fmt.Errorf("no binary found for OS: %s", runtime.GOOS)
}

func (u *YtDlpUpdater) getChecksumURL(assets []struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}) (string, error) {
	for _, asset := range assets {
		if asset.Name == "SHA2-256SUMS" {
			return asset.BrowserDownloadURL, nil
		}
	}
	return "", fmt.Errorf("checksum manifest SHA2-256SUMS not found in release")
}

// fetchExpectedChecksum downloads the SHA2-256SUMS manifest and returns the
// lowercase hex digest published for assetName. Each line is "<hash>  <name>".
func (u *YtDlpUpdater) fetchExpectedChecksum(url, assetName string) (string, error) {
	if err := utils.ValidateURL(url); err != nil {
		return "", fmt.Errorf("invalid checksum URL: %w", err)
	}
	resp, err := newHTTPClient(30 * time.Second).Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch checksum manifest: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("checksum manifest fetch failed with status %d", resp.StatusCode)
	}

	return parseChecksumManifest(resp.Body, assetName)
}

// parseChecksumManifest scans a SHA2-256SUMS body and returns the lowercase
// hex digest published for assetName. Each line is "<hash>  <name>".
func parseChecksumManifest(r io.Reader, assetName string) (string, error) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 2 && fields[1] == assetName {
			return strings.ToLower(fields[0]), nil
		}
	}
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("failed to read checksum manifest: %w", err)
	}
	return "", fmt.Errorf("no checksum entry for %q", assetName)
}

// verifyChecksum computes the SHA-256 of path and compares it to expectedHex.
func verifyChecksum(path, expectedHex string) error {
	if expectedHex == "" {
		return fmt.Errorf("empty expected checksum")
	}
	f, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("failed to open file for hashing: %w", err)
	}
	defer f.Close()

	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return fmt.Errorf("failed to hash file: %w", err)
	}
	got := hex.EncodeToString(h.Sum(nil))
	if !strings.EqualFold(got, expectedHex) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", expectedHex, got)
	}
	return nil
}

func (u *YtDlpUpdater) downloadBinary(url string) (string, error) {
	// Validate URL before making HTTP request (fixes G107)
	if err := utils.ValidateURL(url); err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}

	// The binary is ~35MB; allow time for slow links while still bounding the request.
	resp, err := newHTTPClient(5 * time.Minute).Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to download binary: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("download failed with status %d", resp.StatusCode)
	}

	// Create temporary file
	tempFile, err := os.CreateTemp("", "yt-dlp-update-*")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	tempName := tempFile.Name()

	// Copy downloaded content to temp file.
	written, err := io.Copy(tempFile, resp.Body)
	if err != nil {
		tempFile.Close()
		os.Remove(tempName)
		return "", fmt.Errorf("failed to write downloaded content: %w", err)
	}
	// Flush and surface a close error rather than trusting a possibly partial
	// buffered write, and reject a short body when the length is advertised
	// (finding UP-3). A short/truncated download is caught here as a download
	// error instead of surfacing later as a misleading checksum mismatch.
	if err := tempFile.Close(); err != nil {
		os.Remove(tempName)
		return "", fmt.Errorf("failed to finalize downloaded file: %w", err)
	}
	if resp.ContentLength >= 0 && written != resp.ContentLength {
		os.Remove(tempName)
		return "", fmt.Errorf("incomplete download: got %d bytes, expected %d", written, resp.ContentLength)
	}

	return tempName, nil
}

func (u *YtDlpUpdater) installBinary(tempFile string) error {
	// Ensure the assets directory exists
	if err := os.MkdirAll(u.assetsDir, 0755); err != nil {
		return fmt.Errorf("failed to create assets directory: %w", err)
	}

	// Stage the new binary inside the target directory, fsync and chmod it
	// there, then swap it into place with a single rename. This makes the
	// install atomic on the same filesystem and removes the window where
	// binPath existed but was not yet executable (finding UP-4).
	stagedPath, err := u.stageBinary(tempFile)
	if err != nil {
		return err
	}

	// Backup existing binary if it exists, so a failed swap can roll back
	// instead of leaving no working binary at binPath.
	backupPath := u.binPath + ".backup"
	hasBackup := false
	if _, err := os.Stat(u.binPath); err == nil {
		if err := os.Rename(u.binPath, backupPath); err != nil {
			os.Remove(stagedPath)
			return fmt.Errorf("failed to backup existing binary: %w", err)
		}
		hasBackup = true
	}

	if err := os.Rename(stagedPath, u.binPath); err != nil {
		os.Remove(stagedPath)
		if hasBackup {
			if rerr := os.Rename(backupPath, u.binPath); rerr != nil {
				return fmt.Errorf("failed to install binary (%w) and restore backup failed: %v", err, rerr)
			}
		}
		return fmt.Errorf("failed to install binary: %w", err)
	}

	// Install succeeded; drop the backup to avoid accumulating stale binaries.
	if hasBackup {
		_ = os.Remove(backupPath)
	}

	return nil
}

// stageBinary copies the verified temp file into the target directory, syncs
// and (on Unix) marks it executable, and returns the staged path ready to be
// renamed onto binPath. The caller owns removing it on a later failure.
func (u *YtDlpUpdater) stageBinary(tempFile string) (string, error) {
	src, err := os.Open(tempFile)
	if err != nil {
		return "", fmt.Errorf("failed to open downloaded binary: %w", err)
	}
	defer src.Close()

	staged, err := os.CreateTemp(u.assetsDir, "yt-dlp-staged-*")
	if err != nil {
		return "", fmt.Errorf("failed to create staging file: %w", err)
	}
	stagedPath := staged.Name()

	if _, err := io.Copy(staged, src); err != nil {
		staged.Close()
		os.Remove(stagedPath)
		return "", fmt.Errorf("failed to stage binary: %w", err)
	}
	if err := staged.Sync(); err != nil {
		staged.Close()
		os.Remove(stagedPath)
		return "", fmt.Errorf("failed to sync staged binary: %w", err)
	}
	if err := staged.Close(); err != nil {
		os.Remove(stagedPath)
		return "", fmt.Errorf("failed to finalize staged binary: %w", err)
	}
	if runtime.GOOS != "windows" {
		if err := os.Chmod(stagedPath, 0755); err != nil {
			os.Remove(stagedPath)
			return "", fmt.Errorf("failed to make staged binary executable: %w", err)
		}
	}
	return stagedPath, nil
}

func (u *YtDlpUpdater) GetCachedUpdateInfo() *UpdateInfo {
	return u.versionCache
}
