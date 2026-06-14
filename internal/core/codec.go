package core

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"goyt/internal/utils"
)

// resolveExecutable turns a configured binary reference into a validated
// absolute path. Bare command names (e.g. "ffmpeg", "ffprobe") are resolved on
// PATH via exec.LookPath; explicit paths are used as-is. The result is checked
// with utils.ValidateExecutablePath, which requires an absolute, existing,
// executable file. This lets PATH-based ffmpeg/ffprobe (the common default)
// work while keeping the absolute-path safety guarantee.
func resolveExecutable(name string) (string, error) {
	if name == "" {
		return "", fmt.Errorf("executable path cannot be empty")
	}
	resolved := name
	if !strings.ContainsRune(name, os.PathSeparator) && !filepath.IsAbs(name) {
		p, err := exec.LookPath(name)
		if err != nil {
			return "", fmt.Errorf("executable %q not found on PATH: %w", name, err)
		}
		resolved = p
	}
	if !filepath.IsAbs(resolved) {
		abs, err := filepath.Abs(resolved)
		if err != nil {
			return "", err
		}
		resolved = abs
	}
	if err := utils.ValidateExecutablePath(resolved); err != nil {
		return "", err
	}
	return resolved, nil
}

// parseCodecOutput reads ffprobe CSV output ("codec_type,codec_name" per line)
// and returns the first video and first audio codec names, lowercased. Missing
// streams yield empty strings.
func parseCodecOutput(out string) (video, audio string) {
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ",", 2)
		if len(parts) != 2 {
			continue
		}
		// ffprobe's csv field order is not guaranteed to match the -show_entries
		// order (it emits "h264,video", not "video,h264"), so identify the
		// codec_type field by value rather than position.
		a := strings.ToLower(strings.TrimSpace(parts[0]))
		b := strings.ToLower(strings.TrimSpace(parts[1]))
		var kind, name string
		switch {
		case a == "video" || a == "audio":
			kind, name = a, b
		case b == "video" || b == "audio":
			kind, name = b, a
		default:
			continue
		}
		switch kind {
		case "video":
			if video == "" {
				video = name
			}
		case "audio":
			if audio == "" {
				audio = name
			}
		}
	}
	return video, audio
}

// ffprobePath resolves the ffprobe binary. When ffmpegPath points at a real
// file (e.g. the bundled Windows ffmpeg.exe), ffprobe is its sibling; otherwise
// fall back to "ffprobe" on PATH.
func (d *Downloader) ffprobePath() string {
	p := d.ffmpegPath
	if p == "" || p == "ffmpeg" || p == "ffmpeg.exe" {
		return "ffprobe"
	}
	dir := filepath.Dir(p)
	name := "ffprobe"
	if strings.EqualFold(filepath.Ext(p), ".exe") {
		name = "ffprobe.exe"
	}
	return filepath.Join(dir, name)
}

// ProbeCodecs returns the first video and audio codec names of a media file,
// lowercased (e.g. "h264","aac"). A probe failure is returned as an error; the
// caller decides whether to treat it as fatal.
func (d *Downloader) ProbeCodecs(filePath string) (video, audio string, err error) {
	probe, err := resolveExecutable(d.ffprobePath())
	if err != nil {
		return "", "", fmt.Errorf("invalid ffprobe path: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// #nosec G204 - probe path is validated above; filePath is an internal path.
	cmd := exec.CommandContext(ctx, probe,
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name",
		"-of", "csv=p=0",
		filePath)
	out, err := cmd.Output()
	if err != nil {
		return "", "", fmt.Errorf("ffprobe failed: %w", err)
	}
	v, a := parseCodecOutput(string(out))
	return v, a, nil
}

// probeDurationSeconds returns the media duration in seconds, or 0 if unknown.
// Used as the fallback duration for ffmpeg progress percentage during convert.
func (d *Downloader) probeDurationSeconds(filePath string) float64 {
	probe, err := resolveExecutable(d.ffprobePath())
	if err != nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// #nosec G204 - probe path is validated above; filePath is an internal path.
	cmd := exec.CommandContext(ctx, probe,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		filePath)
	out, err := cmd.Output()
	if err != nil {
		return 0
	}
	secs, err := strconv.ParseFloat(strings.TrimSpace(string(out)), 64)
	if err != nil {
		return 0
	}
	return secs
}

// buildConvertArgs builds ffmpeg arguments to transcode srcPath to H.264 + AAC
// MP4 at dstPath, writing progress to progressFile. Uses a hardware H.264
// encoder when available, otherwise libx264.
func (d *Downloader) buildConvertArgs(srcPath, dstPath, progressFile string) []string {
	args := []string{"-hide_banner", "-nostdin"}

	hwAccel := d.getHardwareAcceleration()
	if hwAccel != "" {
		args = append(args, strings.Fields(hwAccel)...)
	}
	args = append(args, "-i", srcPath)

	vEncoder := "libx264"
	if hwAccel != "" {
		vEncoder = d.getHardwareEncoder()
	}
	args = append(args,
		"-c:v", vEncoder, "-crf", "23", "-preset", "fast", "-profile:v", "main",
		"-pix_fmt", "yuv420p",
		"-c:a", "aac", "-b:a", "128k", "-ac", "2", "-ar", "44100",
		"-movflags", "+faststart",
		// Force the mp4 muxer: the temp output ends in ".mp4.tmp", so ffmpeg
		// cannot infer the format from the extension.
		"-f", "mp4",
		"-progress", progressFile, "-nostats", "-loglevel", "warning",
		"-y", dstPath,
	)
	return args
}

// ConvertToH264AAC transcodes download.OutputPath to H.264 + AAC MP4, replacing
// the original on success. The new path (always .mp4) is returned. Progress is
// streamed via the existing ffmpeg progress-file monitor. The original file is
// left untouched on failure or cancellation.
func (d *Downloader) ConvertToH264AAC(
	ctx context.Context,
	download *Download,
	progressChan chan DownloadProgress,
) (newPath string, err error) {
	src := download.OutputPath
	if src == "" {
		return "", fmt.Errorf("download has no output path")
	}
	ffmpeg, err := resolveExecutable(d.ffmpegPath)
	if err != nil {
		return "", fmt.Errorf("invalid ffmpeg path: %w", err)
	}

	dir := filepath.Dir(src)
	base := strings.TrimSuffix(filepath.Base(src), filepath.Ext(src))
	finalPath := filepath.Join(dir, base+".mp4")
	tmpPath := filepath.Join(dir, "."+base+".convert.mp4.tmp")
	_ = os.Remove(tmpPath)

	progressFile := filepath.Join(os.TempDir(), fmt.Sprintf("goyt_convert_%s.txt", download.ID))
	download.ProgressFile = progressFile
	defer os.Remove(progressFile)

	duration := d.probeDurationSeconds(src)
	go d.monitorFFmpegProgress(ctx, progressFile, progressChan, download.ID, duration)

	args := d.buildConvertArgs(src, tmpPath, progressFile)
	// #nosec G204 - ffmpeg path is resolved and validated above; other args are internal.
	cmd := exec.CommandContext(ctx, ffmpeg, args...)
	setupProcessGroup(cmd, download.ID)

	if err := cmd.Run(); err != nil {
		_ = os.Remove(tmpPath)
		if ctx.Err() == context.Canceled {
			return "", context.Canceled
		}
		return "", fmt.Errorf("ffmpeg convert failed: %w", err)
	}

	if err := os.Rename(tmpPath, finalPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", fmt.Errorf("failed to place converted file: %w", err)
	}
	if finalPath != src {
		if rmErr := os.Remove(src); rmErr != nil {
			log.Printf("[CONVERT] Could not remove original %s after convert: %v", src, rmErr)
		}
	}
	return finalPath, nil
}
