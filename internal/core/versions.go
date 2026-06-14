package core

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	"goyt/internal/utils"
)

// resolveBinPath makes config-relative binary paths absolute so
// ValidateExecutablePath (which requires absolute paths) accepts them.
// Bare command names are left for PATH lookup.
func resolveBinPath(path string) string {
	if path == "" || filepath.Base(path) == path {
		return path
	}
	if abs, err := filepath.Abs(path); err == nil {
		return abs
	}
	return path
}

type VersionInfo struct {
	YtDlpVersion  string `json:"yt_dlp"`
	FfmpegVersion string `json:"ffmpeg"`
}

func GetVersionInfo(ytdlpPath, ffmpegPath string) *VersionInfo {
	versions := &VersionInfo{}

	// Get yt-dlp version
	if ytdlpVersion := getYtDlpVersion(ytdlpPath); ytdlpVersion != "" {
		versions.YtDlpVersion = ytdlpVersion
	}

	// Get ffmpeg version
	if ffmpegVersion := getFfmpegVersion(ffmpegPath); ffmpegVersion != "" {
		versions.FfmpegVersion = ffmpegVersion
	}

	return versions
}

func getYtDlpVersion(path string) string {
	// Try the configured path first
	if version := tryGetYtDlpVersion(path); version != "" {
		return version
	}

	// Try system PATH
	if version := tryGetYtDlpVersion("yt-dlp"); version != "" {
		return version
	}

	return ""
}

func tryGetYtDlpVersion(path string) string {
	path = resolveBinPath(path)
	// Validate path before executing (fixes G204)
	if path != "yt-dlp" && utils.ValidateExecutablePath(path) != nil {
		return ""
	}

	cmd := exec.Command(path, "--version")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	return strings.TrimSpace(string(output))
}

func getFfmpegVersion(path string) string {
	// Try the configured path first
	if version := tryGetFfmpegVersion(path); version != "" {
		return version
	}

	// Try system PATH
	if version := tryGetFfmpegVersion("ffmpeg"); version != "" {
		return version
	}

	return ""
}

func tryGetFfmpegVersion(path string) string {
	path = resolveBinPath(path)
	// Validate path before executing (fixes G204)
	if path != "ffmpeg" && utils.ValidateExecutablePath(path) != nil {
		return ""
	}

	cmd := exec.Command(path, "-version")
	output, err := cmd.Output()
	if err != nil {
		return ""
	}

	// Extract version from ffmpeg output
	re := regexp.MustCompile(`ffmpeg version ([^\s]+)`)
	matches := re.FindStringSubmatch(string(output))
	if len(matches) > 1 {
		return matches[1]
	}

	return ""
}

func CheckFfmpegAvailable(path string) bool {
	// Try the configured path first
	if isAvailable(path) {
		return true
	}

	// Try system PATH
	return isAvailable("ffmpeg")
}

// CheckYtDlpAvailable reports whether a yt-dlp binary can be found. A bare
// command name is looked up on PATH; an explicit path is checked on disk.
// Falls back to a PATH lookup for "yt-dlp" so a misconfigured path still
// works if the binary is installed system-wide.
func CheckYtDlpAvailable(path string) bool {
	if path != "" {
		if filepath.Base(path) == path {
			if _, err := exec.LookPath(path); err == nil {
				return true
			}
		} else if _, err := os.Stat(path); err == nil {
			return true
		}
	}
	_, err := exec.LookPath("yt-dlp")
	return err == nil
}

// IsCommandAvailable checks if a specific command is available
func IsCommandAvailable(command string) bool {
	// Validate command before executing (fixes G204)
	// Allow common system commands by name, validate full paths
	commonCommands := []string{"ffmpeg", "ffprobe", "yt-dlp"}
	isCommon := false
	for _, common := range commonCommands {
		if command == common {
			isCommon = true
			break
		}
	}

	command = resolveBinPath(command)
	if !isCommon && utils.ValidateExecutablePath(command) != nil {
		return false
	}

	cmd := exec.Command(command, "-version")
	err := cmd.Run()
	return err == nil
}

func isAvailable(command string) bool {
	return IsCommandAvailable(command)
}
