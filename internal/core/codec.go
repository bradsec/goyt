package core

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
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

// MediaInfo is what ffprobe reports about a finished file, reduced to the
// labels the UI shows.
type MediaInfo struct {
	VideoCodec    string
	AudioCodec    string
	Resolution    string
	HDR           bool
	AudioChannels string
	AudioBitrate  string
}

func parseMediaOutput(out []byte) (MediaInfo, error) {
	var probe struct {
		Streams []struct {
			CodecType     string `json:"codec_type"`
			CodecName     string `json:"codec_name"`
			Width         int    `json:"width"`
			Height        int    `json:"height"`
			ColorTransfer string `json:"color_transfer"`
			Channels      int    `json:"channels"`
			BitRate       string `json:"bit_rate"`
		} `json:"streams"`
		Format struct {
			BitRate string `json:"bit_rate"`
		} `json:"format"`
	}
	if err := json.Unmarshal(out, &probe); err != nil {
		return MediaInfo{}, err
	}
	var info MediaInfo
	for _, stream := range probe.Streams {
		switch stream.CodecType {
		case "video":
			if info.VideoCodec != "" {
				continue
			}
			info.VideoCodec = strings.ToLower(stream.CodecName)
			info.Resolution = resolutionLabel(stream.Width, stream.Height)
			// PQ and HLG transfer functions mark HDR; 10-bit alone (VP9 Profile 2)
			// is also used for SDR, so it is not a signal.
			info.HDR = stream.ColorTransfer == "smpte2084" || stream.ColorTransfer == "arib-std-b67"
		case "audio":
			if info.AudioCodec != "" {
				continue
			}
			info.AudioCodec = strings.ToLower(stream.CodecName)
			switch stream.Channels {
			case 1:
				info.AudioChannels = "Mono"
			case 2:
				info.AudioChannels = "Stereo"
			case 6:
				info.AudioChannels = "5.1"
			case 8:
				info.AudioChannels = "7.1"
			default:
				if stream.Channels > 0 {
					info.AudioChannels = fmt.Sprintf("%d channels", stream.Channels)
				}
			}
			info.AudioBitrate = bitrateLabel(stream.BitRate)
		}
	}
	if info.VideoCodec == "" && info.AudioBitrate == "" {
		info.AudioBitrate = bitrateLabel(probe.Format.BitRate)
	}
	return info, nil
}

// resolutionLabel names the quality tier the way video sites do. Either side
// may qualify a tier, so cinema crops (3840x1608) still read as 4K and
// portrait video (1080x1920) as 1080p.
func resolutionLabel(width, height int) string {
	short, long := min(width, height), max(width, height)
	if short <= 0 {
		return ""
	}
	tiers := []struct {
		short, long int
		label       string
	}{
		{4320, 7680, "8K"},
		{2160, 3840, "4K"},
		{1440, 2560, "1440p"},
		{1080, 1920, "1080p"},
		{720, 1280, "720p"},
	}
	for _, t := range tiers {
		if short >= t.short || long >= t.long {
			return t.label
		}
	}
	return fmt.Sprintf("%dp", short)
}

func bitrateLabel(raw string) string {
	bps, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || bps <= 0 {
		return ""
	}
	return fmt.Sprintf("%d kbps", (bps+500)/1000)
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
	info, err := d.ProbeMedia(filePath)
	return info.VideoCodec, info.AudioCodec, err
}

// ProbeMedia returns codec and final quality labels from the media file.
func (d *Downloader) ProbeMedia(filePath string) (MediaInfo, error) {
	probe, err := resolveExecutable(d.ffprobePath())
	if err != nil {
		return MediaInfo{}, fmt.Errorf("invalid ffprobe path: %w", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	// #nosec G204 - probe path is validated above; filePath is an internal path.
	cmd := exec.CommandContext(ctx, probe,
		"-v", "error",
		"-show_entries", "stream=codec_type,codec_name,width,height,color_transfer,channels,bit_rate:format=bit_rate",
		"-of", "json",
		filePath)
	out, err := cmd.Output()
	if err != nil {
		return MediaInfo{}, fmt.Errorf("ffprobe failed: %w", err)
	}
	return parseMediaOutput(out)
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
	monitorCtx, stopMonitor := context.WithCancel(ctx)
	var monitorWG sync.WaitGroup
	defer func() {
		stopMonitor()
		monitorWG.Wait()
		_ = os.Remove(progressFile)
	}()

	duration := d.probeDurationSeconds(src)
	monitorWG.Go(func() {
		d.monitorFFmpegProgress(monitorCtx, progressFile, progressChan, download.ID, duration)
	})

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
