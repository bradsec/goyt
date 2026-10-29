package core

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"goyt/internal/utils"
)

type DownloadType string

const (
	VideoDownload DownloadType = "video"
	AudioDownload DownloadType = "audio"
)

// Format constants
const (
	FormatMP4  = "mp4"
	FormatMKV  = "mkv"
	FormatWEBM = "webm"
	StatusNA   = "N/A"
)

type DownloadStatus string

const (
	StatusQueued         DownloadStatus = "queued"
	StatusDownloading    DownloadStatus = "downloading"
	StatusPostProcessing DownloadStatus = "post-processing"
	StatusPaused         DownloadStatus = "paused"
	StatusCompleted      DownloadStatus = "completed"
	StatusFailed         DownloadStatus = "failed"
	StatusCanceled       DownloadStatus = "canceled"
	StatusAlreadyExists  DownloadStatus = "already_exists"
	StatusConverting     DownloadStatus = "converting"
)

type DownloadRequest struct {
	URL                      string       `json:"url"`
	Type                     DownloadType `json:"type"`
	Quality                  string       `json:"quality"`
	Format                   string       `json:"format"`
	Audio                    string       `json:"audio"`
	Range                    string       `json:"range"`
	OutputDir                string       `json:"output_dir"`
	ReencodeForCompatibility bool         `json:"-"`
}

type DownloadProgress struct {
	Percentage float64 `json:"percentage"`
	Speed      string  `json:"speed"`
	ETA        string  `json:"eta"`
	Size       string  `json:"size"`
	// Enhanced FFMPEG feedback
	Phase          string `json:"phase"`           // "downloading", "processing", "converting"
	FFmpegProgress string `json:"ffmpeg_progress"` // Detailed FFMPEG progress info
	CurrentFrame   int64  `json:"current_frame"`   // Current frame being processed
	TotalFrames    int64  `json:"total_frames"`    // Total frames (if known)
	ProcessingTime string `json:"processing_time"` // Time spent in current phase
	VideoCodec     string `json:"video_codec"`     // Video codec being used
	AudioCodec     string `json:"audio_codec"`     // Audio codec being used
	Bitrate        string `json:"bitrate"`         // Current bitrate
	Resolution     string `json:"resolution"`      // Video resolution
	FPS            string `json:"fps"`             // Frames per second
}

type TitleUpdateCallback func(id, title string)
type QualityUpdateCallback func(id string, quality MediaInfo)
type StatusUpdateCallback func(id string, status DownloadStatus)

type Download struct {
	ID               string           `json:"id"`
	URL              string           `json:"url"`
	Type             DownloadType     `json:"type"`
	Quality          string           `json:"quality"`
	Format           string           `json:"format"`
	Audio            string           `json:"audio,omitempty"`
	Range            string           `json:"range,omitempty"`
	Status           DownloadStatus   `json:"status"`
	Progress         DownloadProgress `json:"progress"`
	Title            string           `json:"title"`
	Filename         string           `json:"filename"`
	OutputPath       string           `json:"output_path"`
	FileSize         int64            `json:"file_size,omitempty"` // Final size in bytes, set on completion
	CreatedAt        time.Time        `json:"created_at"`
	CompletedAt      *time.Time       `json:"completed_at,omitempty"`
	Error            string           `json:"error,omitempty"`
	VideoCodec       string           `json:"video_codec,omitempty"`
	AudioCodec       string           `json:"audio_codec,omitempty"`
	Resolution       string           `json:"resolution,omitempty"`
	Width            int              `json:"-"`
	Height           int              `json:"-"`
	CompatibilityCap int              `json:"-"`
	HDR              bool             `json:"hdr,omitempty"`
	AudioChannels    string           `json:"audio_channels,omitempty"`
	AudioBitrate     string           `json:"audio_bitrate,omitempty"`
	Converted        bool             `json:"converted,omitempty"`
	StatusMessage    string           `json:"status_message,omitempty"`
	ProgressFile     string           `json:"-"` // Path to FFMPEG progress file
}

// Default network timeouts used when SetTimeouts is not called (e.g. tests).
const (
	defaultVideoInfoTimeout = 30 * time.Second
	defaultPlaylistTimeout  = 120 * time.Second
)

type Downloader struct {
	ytDlpPath           string
	ffmpegPath          string
	cookiesFile         string
	enableHardwareAccel bool
	optimizeForLowPower bool
	videoInfoTimeout    time.Duration
	playlistTimeout     time.Duration
}

func NewDownloader(ytDlpPath, ffmpegPath, cookiesFile string, enableHardwareAccel, optimizeForLowPower bool) *Downloader {
	return &Downloader{
		ytDlpPath:           ytDlpPath,
		ffmpegPath:          ffmpegPath,
		cookiesFile:         cookiesFile,
		enableHardwareAccel: enableHardwareAccel,
		optimizeForLowPower: optimizeForLowPower,
		videoInfoTimeout:    defaultVideoInfoTimeout,
		playlistTimeout:     defaultPlaylistTimeout,
	}
}

// SetTimeouts overrides the network operation timeouts. Non-positive values are
// ignored so a caller can pass only the timeouts it wants to change.
func (d *Downloader) SetTimeouts(videoInfo, playlist time.Duration) {
	if videoInfo > 0 {
		d.videoInfoTimeout = videoInfo
	}
	if playlist > 0 {
		d.playlistTimeout = playlist
	}
}

// cookieArgs returns yt-dlp cookie flags when a readable cookies file is
// configured. Sites behind logins or bot checks need browser cookies in
// Netscape format.
func (d *Downloader) cookieArgs() []string {
	if d.cookiesFile == "" {
		return nil
	}
	// Resolve to an absolute path. yt-dlp runs with cmd.Dir set to the download
	// output directory, so a relative cookies path (e.g. the managed
	// "cookies.txt" stored when no explicit path is set) would be looked up
	// relative to the output dir and silently not found, while goyt's own stat
	// resolves it against the process working directory.
	cookiesPath := d.cookiesFile
	if abs, err := filepath.Abs(cookiesPath); err == nil {
		cookiesPath = abs
	}
	if _, err := os.Stat(cookiesPath); err != nil {
		log.Printf("[DOWNLOAD] Cookies file %s not readable, ignoring: %v", cookiesPath, err)
		return nil
	}
	return []string{"--cookies", cookiesPath}
}

// jsRuntimeArgs tells yt-dlp which JavaScript runtime to use for solving
// YouTube's nsig/n challenges. Without a runtime yt-dlp 2025.xx+ cannot derive
// video URLs and reports "Requested format is not available" with only image
// (storyboard) formats. Deno is yt-dlp's default and needs no flag; Node must
// be opted into explicitly. We pick Deno when present, otherwise enable Node if
// it is on PATH (and new enough). With neither installed we add nothing and let
// yt-dlp emit its own guidance.
func (d *Downloader) jsRuntimeArgs() []string {
	if _, err := exec.LookPath("deno"); err == nil {
		return nil
	}
	if _, err := exec.LookPath("node"); err == nil {
		return []string{"--js-runtimes", "node"}
	}
	return nil
}

func (d *Downloader) Download(
	ctx context.Context,
	req DownloadRequest,
	progressChan chan<- DownloadProgress,
	titleCallback TitleUpdateCallback,
	qualityCallback QualityUpdateCallback,
	statusCallback StatusUpdateCallback,
	downloadID string,
) (*Download, error) {
	cap := 0
	if req.ReencodeForCompatibility && req.Type == VideoDownload {
		req.Format = FormatMP4
		cap = compatibilityCap(req.Quality)
	}
	download := &Download{
		ID:               downloadID, // Use the ID from the manager
		URL:              req.URL,
		Type:             req.Type,
		Quality:          req.Quality,
		Format:           req.Format,
		CompatibilityCap: cap,
		Status:           StatusQueued,
		CreatedAt:        time.Now(),
	}

	utils.LogInfo("[DOWNLOAD] %s: Added to queue - %s (%s %s)", download.ID, req.URL, req.Type, req.Format)

	// Try to get video info first (non-blocking)
	title, quality, err := d.getSelectedInfo(req)
	if err != nil {
		if utils.VerboseLogging {
			utils.LogDebugf("[DOWNLOAD] %s: Could not get video info, will extract during download", download.ID)
		}
		// Don't fail the download - just use URL as fallback
		download.Title = req.URL
		download.Filename = fmt.Sprintf("download_%s", download.ID)
	} else {
		download.Title = title
		download.Filename = SanitizeFilename(title)
		download.VideoCodec, download.AudioCodec = quality.VideoCodec, quality.AudioCodec
		download.Resolution, download.HDR = quality.Resolution, quality.HDR
		download.AudioChannels, download.AudioBitrate = quality.AudioChannels, quality.AudioBitrate
		if qualityCallback != nil {
			qualityCallback(download.ID, quality)
		}
		utils.LogInfo("[DOWNLOAD] %s: Title identified - %s", download.ID, title)
	}

	// Update title in manager if callback is provided
	if titleCallback != nil {
		titleCallback(download.ID, download.Title)
	}

	// Send initial progress update with title information (with safe channel handling)
	func() {
		defer func() {
			if r := recover(); r != nil {
				// Channel was closed or there was another panic, ignore it
				log.Printf("[DOWNLOAD] %s: Recovered from progress channel panic: %v", download.ID, r)
			}
		}()
		select {
		case progressChan <- DownloadProgress{
			Percentage:     0,
			Speed:          "",
			ETA:            "",
			Size:           "",
			Phase:          "initializing",
			FFmpegProgress: "",
			CurrentFrame:   0,
			TotalFrames:    0,
			ProcessingTime: "",
			VideoCodec:     "",
			AudioCodec:     "",
			Bitrate:        "",
			Resolution:     "",
			FPS:            "",
		}:
		case <-ctx.Done():
			// Context canceled, don't send
		default:
			// Channel full, skip
		}
	}()

	// The sanitized title is a basename, including any dotted suffix.
	download.Filename += "." + req.Format

	download.OutputPath = filepath.Join(req.OutputDir, download.Filename)
	if utils.VerboseLogging {
		utils.LogDebugf("[DOWNLOAD] %s: Output path=%s", download.ID, download.OutputPath)
	}

	// Build yt-dlp command
	args := d.buildYtDlpArgs(req, download)

	download.Status = StatusDownloading
	utils.LogInfo("[DOWNLOAD] %s: Starting download", download.ID)
	if utils.VerboseLogging {
		utils.LogDebugf("[DOWNLOAD] %s: yt-dlp path: %s", download.ID, d.ytDlpPath)
	}

	// Resolve yt-dlp path to absolute path to handle working directory changes
	ytDlpAbsPath := d.ytDlpPath
	if !filepath.IsAbs(ytDlpAbsPath) {
		if wd, err := os.Getwd(); err == nil {
			ytDlpAbsPath = filepath.Join(wd, ytDlpAbsPath)
		}
	}

	// Validate yt-dlp path before executing (fixes G204)
	if err := utils.ValidateExecutablePath(ytDlpAbsPath); err != nil {
		return nil, fmt.Errorf("invalid yt-dlp path: %w", err)
	}

	// Check if yt-dlp binary exists and is executable
	if _, err := os.Stat(ytDlpAbsPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("yt-dlp binary not found at %s (resolved from %s)", ytDlpAbsPath, d.ytDlpPath)
	}

	if utils.VerboseLogging {
		utils.LogDebugf("[DOWNLOAD] %s: Using absolute yt-dlp path: %s", download.ID, ytDlpAbsPath)
		utils.LogDebugf("[DOWNLOAD] %s: yt-dlp command: %s %s", download.ID, ytDlpAbsPath, strings.Join(args, " "))
	}
	cmd := exec.CommandContext(ctx, ytDlpAbsPath, args...)
	cmd.Dir = req.OutputDir

	// Set up process group for proper child process cleanup (platform-specific)
	setupProcessGroup(cmd, download.ID)
	// Bound how long Wait keeps copying output after a cancel, in case a child
	// outside the killed process group still holds the pipe open.
	cmd.WaitDelay = 10 * time.Second

	if utils.VerboseLogging {
		utils.LogDebugf("[DOWNLOAD] %s: Working directory: %s", download.ID, req.OutputDir)
	}

	// Route output through io.Pipe rather than StdoutPipe/StderrPipe: with a
	// non-file writer, Wait returns only after all output has been copied, and
	// closing the writers afterwards gives the monitor a clean EOF. StdoutPipe
	// would require every read to finish before Wait, which this flow cannot
	// guarantee, and Wait closing the pipe early dropped yt-dlp's final ERROR.
	stdout, stdoutW := io.Pipe()
	stderr, stderrW := io.Pipe()
	cmd.Stdout = stdoutW
	cmd.Stderr = stderrW
	closeOutput := func() {
		stdoutW.Close()
		stderrW.Close()
	}

	if err := cmd.Start(); err != nil {
		closeOutput()
		utils.LogError("[DOWNLOAD] %s: Failed to start yt-dlp: %v", download.ID, err)
		download.Status = StatusFailed
		download.Error = fmt.Sprintf("Failed to start yt-dlp: %v", err)
		return download, err
	}

	if utils.VerboseLogging {
		utils.LogDebugf("[DOWNLOAD] %s: yt-dlp process started, PID: %d", download.ID, cmd.Process.Pid)
	}

	// Monitor progress. Keep a reference so a failed download can read the
	// yt-dlp ERROR lines captured from stderr.
	monitor := d.createProgressMonitor(download.ID, progressChan, statusCallback)
	monitorCtx, stopMonitors := context.WithCancel(ctx)
	var monitorWG sync.WaitGroup
	defer func() {
		stopMonitors()
		monitorWG.Wait()
	}()
	monitorWG.Go(func() { monitor.start(stdout, stderr) })

	// Start FFmpeg progress monitoring early to catch post-processing updates.
	// The monitor exits with ctx when the download finishes or is canceled.
	if download.ProgressFile != "" {
		const fallbackDuration = 300.0 // used only until the real duration is parsed
		monitorWG.Go(func() {
			d.monitorFFmpegProgress(monitorCtx, download.ProgressFile, progressChan, download.ID, fallbackDuration, false)
		})
	}

	// Wait for completion
	if utils.VerboseLogging {
		utils.LogDebugf("[DOWNLOAD] %s: Waiting for yt-dlp to complete...", download.ID)
	}
	err = cmd.Wait()
	closeOutput()

	if ctx.Err() == context.Canceled {
		utils.LogInfo("[DOWNLOAD] %s: Download canceled", download.ID)
		download.Status = StatusCanceled
		return download, nil
	}

	if err != nil {
		utils.LogError("[DOWNLOAD] %s: yt-dlp failed: %v", download.ID, err)
		download.Status = StatusFailed

		// Wait for stderr to drain, then prefer yt-dlp's own ERROR text (the real
		// cause) over the generic "exit status N". Fall back to categorizeError.
		monitor.waitStderr()
		errorMsg := d.categorizeError(err, download.URL)
		if tail := monitor.errorTail(); tail != "" {
			errorMsg = cleanYtDlpError(tail)
		}
		download.Error = errorMsg

		log.Printf("[DOWNLOAD] %s: Categorized error: %s", download.ID, errorMsg)
		return download, fmt.Errorf("download failed: %s", errorMsg)
	}

	log.Printf("[DOWNLOAD] %s: yt-dlp process completed", download.ID)

	// Verify only the output assigned to this download, never another directory entry.
	if d.findDownloadedFile(req.OutputDir, download.Filename) == "" {
		log.Printf("[DOWNLOAD] %s: Warning - could not locate downloaded file in %s", download.ID, req.OutputDir)
	}

	if download.OutputPath != "" {
		if info, statErr := os.Stat(download.OutputPath); statErr == nil {
			download.FileSize = info.Size()
		}
	}

	download.Status = StatusCompleted
	now := time.Now()
	download.CompletedAt = &now
	log.Printf("[DOWNLOAD] %s: Download completed successfully - %s", download.ID, download.Title)

	return download, nil
}

// findDownloadedFile checks the exact output filename assigned to a download.
func (d *Downloader) findDownloadedFile(outputDir, filename string) string {
	path := filepath.Join(outputDir, filename)
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() {
		return path
	}
	return ""
}

func (d *Downloader) buildYtDlpArgs(req DownloadRequest, download *Download) []string {
	args := []string{
		"--no-playlist",
		"--ignore-errors", // Continue on errors
		"--continue",      // Resume partial downloads if they exist
		// Resilience: retry transient network, fragment, and extractor failures
		// so a momentary hiccup (common on Facebook and large CDNs) does not fail
		// the whole download. Bounds are finite, so a genuinely unavailable video
		// still fails instead of hanging forever.
		"--retries", "10",
		"--fragment-retries", "10",
		"--extractor-retries", "3",
		"--file-access-retries", "5",
		"--retry-sleep", "http:exp=1:30",
	}
	args = append(args, d.cookieArgs()...)
	args = append(args, d.jsRuntimeArgs()...)

	// Always enable progress for UI updates, but control verbosity.
	// The progress template emits machine-readable byte counts on every tick so
	// the UI shows a determinate "X% of <size>" bar instead of a bare spinner,
	// regardless of fragmented/DASH formats where the human-readable line is
	// unreliable. Fields are pipe-delimited and prefixed for unambiguous parsing.
	args = append(args, "--progress", "--newline",
		"--progress-template",
		"download:[goyt-dl] %(progress.downloaded_bytes)s|%(progress.total_bytes)s|"+
			"%(progress.total_bytes_estimate)s|%(progress._speed_str)s|%(progress._eta_str)s")

	// Add verbose or quiet flags based on verbose logging setting
	if utils.VerboseLogging {
		args = append(args, "--verbose")
	} else {
		// Use --quiet instead of --no-warnings to ensure progress output still works
		// --quiet suppresses non-error output except for progress
		args = append(args, "--quiet")
	}

	// Add Reddit-specific options if it's a Reddit URL
	if strings.Contains(req.URL, "reddit.com") || strings.Contains(req.URL, "redd.it") {
		args = append(args, "--extractor-args", "reddit:sort=best")
	}

	// Add ffmpeg path if configured and not default
	if d.ffmpegPath != "" && d.ffmpegPath != "ffmpeg" && d.ffmpegPath != "ffmpeg.exe" {
		args = append(args, "--ffmpeg-location", d.ffmpegPath)
	}

	// Create temporary progress file for FFMPEG progress tracking
	progressFile := filepath.Join(os.TempDir(), fmt.Sprintf("goyt_progress_%s.txt", download.ID))
	download.ProgressFile = progressFile

	if req.Type == AudioDownload {
		args = append(args, "--extract-audio")
		args = append(args, "--audio-format", req.Format)

		// Let yt-dlp choose the best audio format naturally
		// This allows it to optimize and avoid unnecessary post-processing
		args = append(args, d.selectionArgs(req)...)

		// Add FFmpeg post-processing for audio to ensure compatibility and quality
		audioFFmpegArgs := d.buildAudioFFmpegArgs(req.Format, progressFile, req.Audio == "surround")
		if audioFFmpegArgs != "" {
			args = append(args, "--postprocessor-args", "ffmpeg:"+audioFFmpegArgs)
		}

		args = append(args, "--output", download.Filename)
	} else {
		// Video download - use natural format selection
		args = append(args, d.selectionArgs(req)...)

		// Always merge separate streams and remux into the chosen container by
		// stream copy, which is far faster than re-encoding. When "Re-encode
		// video for compatibility" is enabled, a conditional post-download step
		// (DownloadManager.maybeAutoReencode) transcodes to H.264 + AAC only when
		// the result is not already compatible, so already-compatible files are
		// never needlessly re-encoded.
		container := req.Format
		if req.ReencodeForCompatibility {
			container = FormatMP4
		}
		args = append(args, "--merge-output-format", container)
		args = append(args, "--remux-video", container)

		args = append(args, "--output", download.Filename)
	}

	// Add URL
	args = append(args, req.URL)

	return args
}

func (d *Downloader) selectionArgs(req DownloadRequest) []string {
	if req.Type == AudioDownload {
		channelSort := "channels:2"
		if req.Audio == "surround" {
			channelSort = "channels"
		}
		return []string{"--format", "bestaudio/best", "--format-sort", channelSort}
	}
	videoFormat := d.getVideoFormat(req.Quality)
	if req.ReencodeForCompatibility {
		videoFormat = "bestvideo[vcodec^=avc1][dynamic_range=SDR]+bestaudio[acodec^=mp4a][audio_channels<=2]/bestvideo[dynamic_range=SDR]+bestaudio/best"
	}
	args := []string{"--format", videoFormat}
	if req.ReencodeForCompatibility {
		sort := fmt.Sprintf("res:%d,+hdr,+codec:avc:m4a", compatibilityCap(req.Quality))
		if strings.EqualFold(req.Quality, "worst") {
			sort = "+res,+hdr,+codec:avc:m4a"
		}
		return append(args, "--format-sort", sort)
	}
	if !strings.EqualFold(req.Quality, "worst") {
		rangeSort := "+hdr"
		if req.Range == "hdr" {
			rangeSort = "hdr"
		}
		channelSort := "channels:2"
		if req.Audio == "surround" {
			channelSort = "channels"
		}
		sort := "res," + rangeSort + "," + channelSort
		switch req.Format {
		case FormatMP4:
			sort += ",+codec:avc:m4a"
		case FormatWEBM:
			sort += ",+codec:vp9:opus"
			if req.Audio == "surround" {
				video, _, _ := strings.Cut(videoFormat, "+bestaudio")
				args[1] = video + "+bestaudio[acodec^=opus]/" + video + "+bestaudio[acodec=vorbis]/" + videoFormat
			}
		}
		return append(args, "--format-sort", sort)
	}
	return args
}

func parseSelectedInfo(out []byte) (string, MediaInfo, error) {
	var selected struct {
		Title        string  `json:"title"`
		Width        int     `json:"width"`
		Height       int     `json:"height"`
		DynamicRange string  `json:"dynamic_range"`
		VideoCodec   string  `json:"vcodec"`
		AudioCodec   string  `json:"acodec"`
		AudioRate    float64 `json:"abr"`
		Channels     int     `json:"audio_channels"`
	}
	if err := json.Unmarshal(out, &selected); err != nil {
		return "", MediaInfo{}, err
	}
	if selected.Title == "" {
		return "", MediaInfo{}, fmt.Errorf("yt-dlp returned no title")
	}
	codec := func(raw string) string {
		raw = strings.ToLower(raw)
		switch {
		case raw == "none":
			return ""
		case strings.HasPrefix(raw, "vp09"), strings.HasPrefix(raw, "vp9"):
			return "vp9"
		case strings.HasPrefix(raw, "avc1"):
			return "h264"
		case strings.HasPrefix(raw, "av01"):
			return "av1"
		case strings.HasPrefix(raw, "mp4a"):
			return "aac"
		default:
			return raw
		}
	}
	info := MediaInfo{
		VideoCodec: codec(selected.VideoCodec), AudioCodec: codec(selected.AudioCodec),
		Width: selected.Width, Height: selected.Height,
		HDR:           selected.DynamicRange != "" && !strings.EqualFold(selected.DynamicRange, "SDR"),
		AudioChannels: channelLabel(selected.Channels),
	}
	if info.VideoCodec != "" {
		info.Resolution = resolutionLabel(selected.Width, selected.Height)
	}
	if selected.AudioRate > 0 {
		info.AudioBitrate = fmt.Sprintf("%d kbps", int(math.Round(selected.AudioRate)))
	}
	return selected.Title, info, nil
}

func (d *Downloader) getSelectedInfo(req DownloadRequest) (string, MediaInfo, error) {
	ctx, cancel := context.WithTimeout(context.Background(), d.videoInfoTimeout)
	defer cancel()
	path := d.ytDlpPath
	if !filepath.IsAbs(path) {
		if wd, err := os.Getwd(); err == nil {
			path = filepath.Join(wd, path)
		}
	}
	if err := utils.ValidateExecutablePath(path); err != nil {
		return "", MediaInfo{}, fmt.Errorf("invalid yt-dlp path: %w", err)
	}
	args := append(d.cookieArgs(), d.jsRuntimeArgs()...)
	args = append(args, d.selectionArgs(req)...)
	args = append(args, "--dump-json", "--no-warnings", "--no-playlist", req.URL)
	out, err := exec.CommandContext(ctx, path, args...).Output()
	if err != nil {
		return "", MediaInfo{}, err
	}
	return parseSelectedInfo(out)
}

func compatibilityCap(quality string) int {
	switch strings.ToLower(quality) {
	case "720p":
		return 720
	case "480p":
		return 480
	case "360p":
		return 360
	default:
		return 1080
	}
}

// getHardwareAcceleration detects available hardware acceleration options
func (d *Downloader) getHardwareAcceleration() string {
	// Return empty if hardware acceleration is disabled
	if !d.enableHardwareAccel {
		return ""
	}

	// Check for NVIDIA NVENC support
	if d.checkHardwareSupport("h264_nvenc") {
		return "-hwaccel cuda"
	}

	// Check for Intel QuickSync support
	if d.checkHardwareSupport("h264_qsv") {
		return "-hwaccel qsv"
	}

	// Check for AMD/Intel VAAPI support (Linux)
	if d.checkHardwareSupport("h264_vaapi") {
		return "-hwaccel vaapi -hwaccel_device /dev/dri/renderD128"
	}

	// No hardware acceleration available
	return ""
}

// getHardwareEncoder returns the appropriate hardware encoder
func (d *Downloader) getHardwareEncoder() string {
	// Check for NVIDIA NVENC support
	if d.checkHardwareSupport("h264_nvenc") {
		return "h264_nvenc"
	}

	// Check for Intel QuickSync support
	if d.checkHardwareSupport("h264_qsv") {
		return "h264_qsv"
	}

	// Check for AMD/Intel VAAPI support (Linux)
	if d.checkHardwareSupport("h264_vaapi") {
		return "h264_vaapi"
	}

	// Fallback to software encoder
	return "libx264"
}

// checkHardwareSupport checks if a hardware encoder is available
func (d *Downloader) checkHardwareSupport(encoder string) bool {
	// Validate ffmpegPath before executing (fixes G204)
	if err := utils.ValidateExecutablePath(d.ffmpegPath); err != nil {
		log.Printf("Invalid ffmpeg path: %v", err)
		return false
	}

	// #nosec G204 - ffmpegPath is validated by ValidateExecutablePath above
	cmd := exec.Command(d.ffmpegPath, "-encoders")
	output, err := cmd.Output()
	if err != nil {
		return false
	}

	return strings.Contains(string(output), encoder)
}

// buildAudioFFmpegArgs creates optimized FFmpeg arguments for audio processing
// RequiresFfmpeg checks if the given download type and format require ffmpeg for post-processing
func RequiresFfmpeg(downloadType DownloadType, format string) bool {
	switch downloadType {
	case AudioDownload:
		// All audio formats require ffmpeg for post-processing
		return true
	case VideoDownload:
		// Video formats that require ffmpeg for post-processing/merging
		switch format {
		case FormatMP4, FormatMKV, FormatWEBM, "avi":
			return true
		default:
			return false
		}
	default:
		return false
	}
}

// buildAudioFFmpegArgs returns the audio encode settings. surround keeps the
// source channel layout for containers that can hold it; MP3 is limited to
// two channels and always downmixes.
func (d *Downloader) buildAudioFFmpegArgs(format string, progressFile string, surround bool) string {
	var baseArgs string
	if utils.VerboseLogging {
		baseArgs = fmt.Sprintf("-progress %s -stats -loglevel info", progressFile)
	} else {
		// Use -loglevel warning instead of error to allow progress output
		// -nostats hides stats but progress file still works
		baseArgs = fmt.Sprintf("-progress %s -nostats -loglevel warning", progressFile)
	}

	channels, aacRate := "-ac 2 ", "128k"
	if surround {
		// AAC needs more bitrate for six channels than for two.
		channels, aacRate = "", "384k"
	}

	switch format {
	case "mp3":
		// Optimized MP3 - good quality, faster encoding
		// VBR quality 2 (very good quality), standard sample rate
		return fmt.Sprintf("-c:a libmp3lame -q:a 2 -ac 2 -ar 44100 %s", baseArgs)

	case "m4a":
		// Optimized AAC - good quality, faster encoding
		// Lower bitrate but still good quality, standard sample rate
		return fmt.Sprintf("-c:a aac -b:a %s %s-ar 44100 %s", aacRate, channels, baseArgs)

	case "wav":
		// Standard WAV - no unnecessary processing
		// 16-bit PCM, standard sample rate
		return fmt.Sprintf("-c:a pcm_s16le %s-ar 44100 %s", channels, baseArgs)

	case "flac":
		// Faster FLAC encoding - lower compression for speed
		// Standard sample rate, compression level 3 (faster)
		return fmt.Sprintf("-c:a flac -compression_level 3 %s-ar 44100 %s", channels, baseArgs)

	default:
		// Fallback to MP3 for unknown formats
		return fmt.Sprintf("-c:a libmp3lame -q:a 2 -ac 2 -ar 44100 %s", baseArgs)
	}
}

func (d *Downloader) getVideoFormat(quality string) string {
	heights := map[string]int{
		"4k": 2160, "2160p": 2160,
		"2k": 1440, "1440p": 1440,
		"1080p": 1080, "720p": 720, "480p": 480, "360p": 360,
	}

	q := strings.ToLower(quality)
	switch q {
	case "best", "":
		return "bestvideo+bestaudio/best"
	case "worst":
		return "worstvideo+worstaudio/worst"
	}

	h, ok := heights[q]
	if !ok {
		if n, err := strconv.Atoi(strings.TrimSuffix(q, "p")); err == nil && n > 0 {
			h = n
		} else {
			h = 1080
		}
	}
	// End every chain with an unrestricted "/best" so a height cap degrades to
	// the best available format instead of failing with "Requested format is
	// not available" when no stream satisfies the cap (common on Facebook).
	return fmt.Sprintf("bestvideo[height<=%d]+bestaudio/best[height<=%d]/best", h, h)
}

// cleanYtDlpError tidies captured yt-dlp ERROR lines for display: it drops the
// leading "ERROR:" token and extractor tags like "[youtube]" so the message
// reads as a plain sentence.
func cleanYtDlpError(tail string) string {
	out := tail
	// Strip a leading "ERROR:" (yt-dlp prefixes its error lines with this).
	if idx := strings.Index(strings.ToUpper(out), "ERROR:"); idx >= 0 {
		out = out[idx+len("ERROR:"):]
	}
	out = strings.TrimSpace(out)
	// Strip a leading "[extractor]" tag if present.
	if strings.HasPrefix(out, "[") {
		if end := strings.Index(out, "]"); end >= 0 {
			out = strings.TrimSpace(out[end+1:])
		}
	}
	if out == "" {
		return tail
	}
	return out
}

// categorizeError provides user-friendly error messages based on the error type
func (d *Downloader) categorizeError(err error, url string) string {
	errStr := strings.ToLower(err.Error())

	// Check for video access errors first
	if msg := d.checkVideoAccessErrors(errStr); msg != "" {
		return msg
	}

	// Check for network/connection errors
	if msg := d.checkNetworkErrors(errStr); msg != "" {
		return msg
	}

	// Check for system/file errors
	if msg := d.checkSystemErrors(errStr); msg != "" {
		return msg
	}

	// Check for processing errors
	if msg := d.checkProcessingErrors(errStr); msg != "" {
		return msg
	}

	// Check for HTTP errors
	if msg := d.checkHTTPErrors(errStr); msg != "" {
		return msg
	}

	// Check for cancellation/timeout errors
	if msg := d.checkCancellationErrors(errStr); msg != "" {
		return msg
	}

	// Default fallback
	return d.formatDefaultError(err, errStr)
}

// checkVideoAccessErrors checks for video availability and access issues
func (d *Downloader) checkVideoAccessErrors(errStr string) string {
	switch {
	case strings.Contains(errStr, "video unavailable"):
		return "Video is unavailable or has been removed"
	case strings.Contains(errStr, "private video"):
		return "Video is private and cannot be downloaded"
	case strings.Contains(errStr, "age-restricted"):
		return "Video is age-restricted and requires authentication"
	case strings.Contains(errStr, "region blocked") || strings.Contains(errStr, "not available in your country"):
		return "Video is not available in your region"
	case strings.Contains(errStr, "copyright"):
		return "Video is blocked due to copyright restrictions"
	case strings.Contains(errStr, "login") || strings.Contains(errStr, "authentication"):
		return "Video requires login or authentication"
	case strings.Contains(errStr, "unsupported url"):
		return "This website or URL format is not supported"
	case strings.Contains(errStr, "format not available"):
		return "Requested quality or format is not available for this video"
	default:
		return ""
	}
}

// checkNetworkErrors checks for network and connection issues
func (d *Downloader) checkNetworkErrors(errStr string) string {
	switch {
	case strings.Contains(errStr, "network") || strings.Contains(errStr, "connection"):
		return "Network connection issue - please check your internet connection"
	case strings.Contains(errStr, "timeout"):
		return "Download timed out - the server may be slow or overloaded"
	case strings.Contains(errStr, "quota exceeded"):
		return "API quota exceeded - please try again later"
	case strings.Contains(errStr, "too many requests"):
		return "Too many requests - please wait and try again"
	default:
		return ""
	}
}

// checkSystemErrors checks for file system and permission issues
func (d *Downloader) checkSystemErrors(errStr string) string {
	switch {
	case strings.Contains(errStr, "disk") || strings.Contains(errStr, "space"):
		return "Insufficient disk space to complete download"
	case strings.Contains(errStr, "permission denied"):
		return "Permission denied - check file/directory permissions"
	case strings.Contains(errStr, "file exists"):
		return "File already exists at destination"
	case strings.Contains(errStr, "no such file"):
		return "Required file or directory not found"
	case strings.Contains(errStr, "executable file not found"):
		return "yt-dlp or ffmpeg executable not found - please check installation"
	default:
		return ""
	}
}

// checkProcessingErrors checks for processing and conversion issues
func (d *Downloader) checkProcessingErrors(errStr string) string {
	switch {
	case strings.Contains(errStr, "extract") && strings.Contains(errStr, "info"):
		return "Failed to extract video information - video may be corrupted or unavailable"
	case strings.Contains(errStr, "ffmpeg"):
		return "FFmpeg processing failed - video conversion error"
	case strings.Contains(errStr, "postprocessing"):
		return "Post-processing failed - video downloaded but conversion failed"
	default:
		return ""
	}
}

// checkHTTPErrors checks for HTTP status code errors
func (d *Downloader) checkHTTPErrors(errStr string) string {
	switch {
	case strings.Contains(errStr, "http") && (strings.Contains(errStr, "404") || strings.Contains(errStr, "not found")):
		return "Video not found (404 error)"
	case strings.Contains(errStr, "http") && (strings.Contains(errStr, "403") || strings.Contains(errStr, "forbidden")):
		return "Access forbidden (403 error) - video may require authentication"
	case strings.Contains(errStr, "http") &&
		(strings.Contains(errStr, "500") || strings.Contains(errStr, "internal server")):
		return "Server error (500) - please try again later"
	case strings.Contains(errStr, "http") && strings.Contains(errStr, "503"):
		return "Service temporarily unavailable (503) - please try again later"
	default:
		return ""
	}
}

// checkCancellationErrors checks for cancellation and timeout issues
func (d *Downloader) checkCancellationErrors(errStr string) string {
	switch {
	case strings.Contains(errStr, "killed") || strings.Contains(errStr, "terminated"):
		return "Download was canceled or interrupted"
	case strings.Contains(errStr, "context canceled"):
		return "Download was canceled by user"
	case strings.Contains(errStr, "context deadline exceeded"):
		return "Download timed out"
	default:
		return ""
	}
}

// formatDefaultError formats the fallback error message
func (d *Downloader) formatDefaultError(err error, errStr string) string {
	if len(errStr) > 200 {
		return fmt.Sprintf("Download failed: %s...", errStr[:200])
	}
	return fmt.Sprintf("Download failed: %s", err.Error())
}

// Helper function to convert time string (HH:MM:SS.mmm) to seconds
func timeStringToSeconds(timeStr string) float64 {
	parts := strings.Split(timeStr, ":")
	if len(parts) != 3 {
		return 0
	}

	hours, _ := strconv.ParseFloat(parts[0], 64)
	minutes, _ := strconv.ParseFloat(parts[1], 64)
	seconds, _ := strconv.ParseFloat(parts[2], 64)

	return hours*3600 + minutes*60 + seconds
}

// progressMonitor holds the state for monitoring download progress
type progressMonitor struct {
	downloadID       string
	progressChan     chan<- DownloadProgress
	statusCallback   StatusUpdateCallback
	lastPercentage   float64
	isPostProcessing atomic.Bool

	// FFmpeg progress tracking variables (protected by mutex for thread safety)
	progressMutex                     sync.RWMutex
	ffmpegFrames                      int64
	ffmpegTotalFrames                 int64
	ffmpegDuration, ffmpegCurrentTime float64
	ffmpegBitrate                     string
	ffmpegSpeed                       string
	ffmpegFPS                         string
	videoCodec                        string
	audioCodec                        string
	resolution                        string
	currentPhase                      string
	phaseStartTime                    time.Time

	// Regex patterns
	progressRegexes            []*regexp.Regexp
	postProcessRegexes         []*regexp.Regexp
	ffmpegProgressRegex        *regexp.Regexp
	ffmpegProgressFrameRegex   *regexp.Regexp
	ffmpegProgressTimeRegex    *regexp.Regexp
	ffmpegProgressBitrateRegex *regexp.Regexp
	ffmpegProgressSpeedRegex   *regexp.Regexp
	ffmpegProgressFpsRegex     *regexp.Regexp
	durationRegex              *regexp.Regexp
	videoStreamRegex           *regexp.Regexp
	audioStreamRegex           *regexp.Regexp
	codecRegex                 *regexp.Regexp

	// Captured yt-dlp ERROR lines from stderr, kept so a failed download can
	// report the real cause instead of a bare "exit status 1".
	stderrMu   sync.Mutex
	stderrErrs []string
	stderrWG   sync.WaitGroup
}

// maxCapturedErrLines bounds how many recent yt-dlp ERROR lines are retained.
const maxCapturedErrLines = 8

// recordErrorLine keeps yt-dlp ERROR lines (most recent maxCapturedErrLines).
func (m *progressMonitor) recordErrorLine(line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || !strings.Contains(strings.ToUpper(trimmed), "ERROR") {
		return
	}
	m.stderrMu.Lock()
	defer m.stderrMu.Unlock()
	m.stderrErrs = append(m.stderrErrs, trimmed)
	if len(m.stderrErrs) > maxCapturedErrLines {
		m.stderrErrs = m.stderrErrs[len(m.stderrErrs)-maxCapturedErrLines:]
	}
}

// errorTail returns the captured yt-dlp ERROR lines as a single string, or "".
func (m *progressMonitor) errorTail() string {
	m.stderrMu.Lock()
	defer m.stderrMu.Unlock()
	return strings.Join(m.stderrErrs, "; ")
}

// waitStderr blocks until the stderr reader goroutine has fully drained, so the
// captured error lines are complete before they are read.
func (m *progressMonitor) waitStderr() {
	m.stderrWG.Wait()
}

// createProgressMonitor creates a new progress monitor with initialized regex patterns
func (d *Downloader) createProgressMonitor(
	downloadID string,
	progressChan chan<- DownloadProgress,
	statusCallback StatusUpdateCallback,
) *progressMonitor {
	m := &progressMonitor{
		downloadID:     downloadID,
		progressChan:   progressChan,
		statusCallback: statusCallback,
		lastPercentage: -1.0,
		currentPhase:   "initializing",
		phaseStartTime: time.Now(),
	}

	m.initializeRegexPatterns()
	return m
}

// initializeRegexPatterns sets up all the regex patterns for progress monitoring
func (m *progressMonitor) initializeRegexPatterns() {
	// Simplified and more robust regex patterns to match yt-dlp output formats
	m.progressRegexes = []*regexp.Regexp{
		// Main pattern: [download]   50.0% of   11.21MiB at    2.47MiB/s ETA 00:04
		regexp.MustCompile(`\[download\]\s+(\d+\.?\d*)%\s+of\s+~?\s*(\S+)\s+at\s+(\S+)\s+ETA\s+(\S+)`),
		// Completion pattern: [download] 100% of   11.21MiB in 00:04
		regexp.MustCompile(`\[download\]\s+(\d+\.?\d*)%\s+of\s+~?\s*(\S+)\s+in\s+(\S+)`),
		// Fallback pattern: any [download] line with percentage
		regexp.MustCompile(`\[download\].*?(\d+\.?\d*)%`),
	}

	// Post-processing regex patterns
	m.postProcessRegexes = []*regexp.Regexp{
		regexp.MustCompile(`\[ffmpeg\]`),
		regexp.MustCompile(`\[Merger\]`),
		regexp.MustCompile(`Merging formats into`),
		regexp.MustCompile(`\[post-processor\]`),
		// Enhanced patterns for common post-processing scenarios
		regexp.MustCompile(`\[ExtractAudio\]`),
		regexp.MustCompile(`\[VideoConvertor\]`),
		regexp.MustCompile(`Fixing`),
		regexp.MustCompile(`Post-processing`),
		regexp.MustCompile(`Extracting audio`),
		regexp.MustCompile(`Converting`),
		// Match any processing that happens after 100% download completion
		regexp.MustCompile(`Deleting original file`),
		regexp.MustCompile(`has already been downloaded`),
	}

	// Enhanced regex patterns for FFMPEG monitoring
	m.ffmpegProgressRegex = regexp.MustCompile(
		`frame=\s*(\d+).*?time=(\d{2}:\d{2}:\d{2}\.\d{2}).*?bitrate=\s*([\d\.]+)kbits/s`)
	m.ffmpegProgressFrameRegex = regexp.MustCompile(`^frame=(\d+)$`)
	m.ffmpegProgressTimeRegex = regexp.MustCompile(`^out_time_ms=(\d+)$`)
	m.ffmpegProgressBitrateRegex = regexp.MustCompile(`^bitrate=(\d+\.?\d*)kbits/s$`)
	m.ffmpegProgressSpeedRegex = regexp.MustCompile(`^speed=(\d+\.?\d*)x$`)
	m.ffmpegProgressFpsRegex = regexp.MustCompile(`^fps=(\d+\.?\d*)$`)

	// Enhanced FFMPEG information extraction
	m.durationRegex = regexp.MustCompile(`Duration:\s*([\d:\.]+)`)
	m.videoStreamRegex = regexp.MustCompile(`Stream.*Video:\s*([^,]+).*?(\d+x\d+).*?(\d+\.?\d*)\s*fps`)
	m.audioStreamRegex = regexp.MustCompile(`Stream.*Audio:\s*([^,]+)`)
	m.codecRegex = regexp.MustCompile(`Stream.*Video:\s*([^,\s]+)`)
}

// start begins monitoring progress from stdout and stderr
func (m *progressMonitor) start(stdout, stderr io.ReadCloser) {
	// Start stderr monitoring in a separate goroutine. The WaitGroup lets the
	// caller block until stderr is fully drained before reading captured errors.
	m.stderrWG.Go(func() {
		m.monitorStderr(stderr)
	})

	// Monitor stdout for main progress
	m.monitorStdout(stdout)
	m.stderrWG.Wait()
}

// updateFFmpegProgress safely updates FFmpeg progress data
func (m *progressMonitor) updateFFmpegProgress(
	frames, totalFrames int64,
	duration, currentTime float64,
	bitrate, speed, fps string,
) {
	m.progressMutex.Lock()
	defer m.progressMutex.Unlock()
	if frames > 0 {
		m.ffmpegFrames = frames
	}
	if totalFrames > 0 {
		m.ffmpegTotalFrames = totalFrames
	}
	if duration > 0 {
		m.ffmpegDuration = duration
	}
	if currentTime > 0 {
		m.ffmpegCurrentTime = currentTime
	}
	if bitrate != "" {
		m.ffmpegBitrate = bitrate
	}
	if speed != "" {
		m.ffmpegSpeed = speed
	}
	if fps != "" {
		m.ffmpegFPS = fps
	}
}

// updateMediaInfo safely updates media information
func (m *progressMonitor) updateMediaInfo(vCodec, aCodec, res string) {
	m.progressMutex.Lock()
	defer m.progressMutex.Unlock()
	if vCodec != "" {
		m.videoCodec = vCodec
	}
	if aCodec != "" {
		m.audioCodec = aCodec
	}
	if res != "" {
		m.resolution = res
	}
}

// updatePhase safely updates the current phase
func (m *progressMonitor) updatePhase(phase string) {
	m.progressMutex.Lock()
	defer m.progressMutex.Unlock()
	if m.currentPhase != phase {
		m.currentPhase = phase
		m.phaseStartTime = time.Now()
	}
}

// readFFmpegProgress safely reads all FFmpeg progress data
func (m *progressMonitor) readFFmpegProgress() (
	int64, int64, float64, float64,
	string, string, string, string, string, string, string,
	time.Duration,
) {
	m.progressMutex.RLock()
	defer m.progressMutex.RUnlock()
	processingTime := time.Since(m.phaseStartTime)
	return m.ffmpegFrames, m.ffmpegTotalFrames, m.ffmpegDuration, m.ffmpegCurrentTime,
		m.ffmpegBitrate, m.ffmpegSpeed, m.ffmpegFPS, m.videoCodec, m.audioCodec, m.resolution,
		m.currentPhase, processingTime
}

// monitorStderr monitors stderr stream for FFmpeg progress and error output
func (m *progressMonitor) monitorStderr(stderr io.ReadCloser) {
	defer stderr.Close()

	scanner := bufio.NewScanner(stderr)
	for scanner.Scan() {
		line := scanner.Text()
		m.recordErrorLine(line)
		m.processStderrLine(line)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[DOWNLOAD] %s: Error reading stderr: %v", m.downloadID, err)
		// Keep draining (e.g. after an over-long line) so yt-dlp never blocks
		// or dies writing to a pipe nobody reads.
		_, _ = io.Copy(io.Discard, stderr)
	}
}

// monitorStdout monitors stdout stream for download progress
func (m *progressMonitor) monitorStdout(stdout io.ReadCloser) {
	defer stdout.Close()

	scanner := bufio.NewScanner(stdout)
	for scanner.Scan() {
		line := scanner.Text()
		m.processStdoutLine(line)
	}

	if err := scanner.Err(); err != nil {
		log.Printf("[DOWNLOAD] %s: Error reading stdout: %v", m.downloadID, err)
		// Keep draining (e.g. after an over-long line) so yt-dlp never blocks
		// or dies writing to a pipe nobody reads.
		_, _ = io.Copy(io.Discard, stdout)
	}
}

// Additional methods for progressMonitor

// processStderrLine processes a single line from stderr
func (m *progressMonitor) processStderrLine(line string) {

	// Enhanced FFmpeg progress parsing from stderr
	if m.isPostProcessing.Load() {
		m.parseFFmpegStderrProgress(line)
	}
}

// parseFFmpegStderrProgress parses FFmpeg progress from stderr
func (m *progressMonitor) parseFFmpegStderrProgress(line string) {
	progressUpdated := false
	var frames int64
	var currentTime float64
	var bitrate, speed, fps string

	// Parse key=value format from -progress pipe:2
	if matches := m.ffmpegProgressFrameRegex.FindStringSubmatch(line); matches != nil {
		frames, _ = strconv.ParseInt(matches[1], 10, 64)
		m.updateFFmpegProgress(frames, 0, 0, 0, "", "", "")
		progressUpdated = true

	} else if matches := m.ffmpegProgressTimeRegex.FindStringSubmatch(line); matches != nil {
		// out_time_ms is in microseconds, convert to seconds
		timeMs, _ := strconv.ParseInt(matches[1], 10, 64)
		currentTime = float64(timeMs) / 1000000.0
		m.updateFFmpegProgress(0, 0, 0, currentTime, "", "", "")
		progressUpdated = true

	} else if matches := m.ffmpegProgressBitrateRegex.FindStringSubmatch(line); matches != nil {
		bitrate = matches[1] + "kbps"
		m.updateFFmpegProgress(0, 0, 0, 0, bitrate, "", "")
		progressUpdated = true

	} else if matches := m.ffmpegProgressSpeedRegex.FindStringSubmatch(line); matches != nil {
		speed = matches[1] + "x"
		m.updateFFmpegProgress(0, 0, 0, 0, "", speed, "")
		progressUpdated = true

	} else if matches := m.ffmpegProgressFpsRegex.FindStringSubmatch(line); matches != nil {
		fps = matches[1] + "fps"
		m.updateFFmpegProgress(0, 0, 0, 0, "", "", fps)
		progressUpdated = true

	} else if matches := m.ffmpegProgressRegex.FindStringSubmatch(line); matches != nil {
		// Fallback to standard FFmpeg output format
		frames, _ = strconv.ParseInt(matches[1], 10, 64)
		currentTime = timeStringToSeconds(matches[2])
		bitrate = matches[3] + "kbps"
		m.updateFFmpegProgress(frames, 0, 0, currentTime, bitrate, "", "")
		progressUpdated = true

	}

	// Calculate and send enhanced progress if updated
	if progressUpdated {
		m.sendFFmpegProgressUpdate()
	}
}

// sendFFmpegProgressUpdate calculates and sends FFmpeg progress updates
func (m *progressMonitor) sendFFmpegProgressUpdate() {
	frames, totalFrames, duration, currentTime, bitrate, speed, fps, vCodec, aCodec, res,
		_, processingTime := m.readFFmpegProgress()

	// Calculate progress percentage based on available data
	var progressPercent float64
	if duration > 0 && currentTime > 0 {
		progressPercent = (currentTime / duration) * 100
	} else if totalFrames > 0 && frames > 0 {
		progressPercent = (float64(frames) / float64(totalFrames)) * 100
	} else if frames > 0 {
		// Estimate progress based on frame count (rough estimate)
		progressPercent = math.Min(float64(frames)/3000.0*100, 99)
	} else {
		// Show some progress to indicate processing is happening
		progressPercent = math.Min(float64(processingTime.Seconds())/60.0*100, 99)
	}

	if progressPercent > 100 {
		progressPercent = 100
	}

	// Estimate remaining time
	eta := ""
	if progressPercent > 0 && progressPercent < 100 {
		if duration > 0 && currentTime > 0 {
			eta = conversionETA(duration-currentTime, speed)
		} else {
			eta = "Processing..."
		}
	}

	// Use speed if available, otherwise bitrate, otherwise show activity
	speedInfo := "Processing..."
	if speed != "" {
		speedInfo = speed
	} else if bitrate != "" {
		speedInfo = bitrate
	}

	// Create detailed FFMPEG progress string
	ffmpegProgressInfo := "Converting..."
	if frames > 0 {
		if totalFrames > 0 {
			ffmpegProgressInfo = fmt.Sprintf("Frame %d/%d", frames, totalFrames)
		} else {
			ffmpegProgressInfo = fmt.Sprintf("Frame %d", frames)
		}
	}
	if currentTime > 0 && duration > 0 {
		ffmpegProgressInfo += fmt.Sprintf(" (%.1fs/%.1fs)", currentTime, duration)
	}

	// Send enhanced FFmpeg progress update
	m.safeProgressSend(DownloadProgress{
		Percentage:     progressPercent,
		Speed:          speedInfo,
		ETA:            eta,
		Size:           fmt.Sprintf("Frame %d", frames),
		Phase:          "processing",
		FFmpegProgress: ffmpegProgressInfo,
		CurrentFrame:   frames,
		TotalFrames:    totalFrames,
		ProcessingTime: fmt.Sprintf("%.2fs", processingTime.Seconds()),
		VideoCodec:     vCodec,
		AudioCodec:     aCodec,
		Bitrate:        bitrate,
		Resolution:     res,
		FPS:            fps,
	})
}

// processStdoutLine processes a single line from stdout
func (m *progressMonitor) processStdoutLine(line string) {
	// Extract media information and duration from FFmpeg output
	if m.isPostProcessing.Load() {
		m.extractMediaInfo(line)
	}

	// Check for post-processing indicators
	if m.checkForPostProcessing(line) {
		return
	}

	// Handle FFmpeg processing status
	if m.isPostProcessing.Load() {
		m.handleFFmpegProcessing(line)
		return
	}

	// Process download progress
	m.processDownloadProgress(line)
}

// extractMediaInfo extracts media information from FFmpeg output
func (m *progressMonitor) extractMediaInfo(line string) {
	_, _, duration, _, _, _, _, vCodec, aCodec, res, _, _ := m.readFFmpegProgress()

	// Extract duration if not already detected
	if duration == 0 {
		if matches := m.durationRegex.FindStringSubmatch(line); matches != nil {
			detectedDuration := timeStringToSeconds(matches[1])
			m.updateFFmpegProgress(0, 0, detectedDuration, 0, "", "", "")
			m.updatePhase("processing")
			log.Printf("[DOWNLOAD] %s: Detected video duration: %.2fs", m.downloadID, detectedDuration)
		}
	}

	// Extract video stream information
	if vCodec == "" || res == "" {
		if matches := m.videoStreamRegex.FindStringSubmatch(line); matches != nil {
			codec := strings.Fields(matches[1])[0] // Get first word (codec name)
			resolution := matches[2]
			fpsValue := matches[3] + "fps"
			m.updateMediaInfo(codec, "", resolution)
			m.updateFFmpegProgress(0, 0, 0, 0, "", "", fpsValue)
			log.Printf("[DOWNLOAD] %s: Detected video: codec=%s, resolution=%s, fps=%s",
				m.downloadID, codec, resolution, fpsValue)
		} else if matches := m.codecRegex.FindStringSubmatch(line); matches != nil {
			codec := matches[1]
			m.updateMediaInfo(codec, "", "")
			log.Printf("[DOWNLOAD] %s: Detected video codec: %s", m.downloadID, codec)
		}
	}

	// Extract audio stream information
	if aCodec == "" {
		if matches := m.audioStreamRegex.FindStringSubmatch(line); matches != nil {
			codec := strings.Fields(matches[1])[0] // Get first word (codec name)
			m.updateMediaInfo("", codec, "")
			log.Printf("[DOWNLOAD] %s: Detected audio codec: %s", m.downloadID, codec)
		}
	}

	// Send updated progress with media info if we have new information
	m.sendMediaInfoUpdate(vCodec, aCodec, res)
}

// sendMediaInfoUpdate sends progress update when new media info is detected
func (m *progressMonitor) sendMediaInfoUpdate(oldVCodec, oldACodec, oldRes string) {
	_, _, newDuration, _, _, _, _, newVCodec, newACodec, newRes, phase, processingTime := m.readFFmpegProgress()
	if newDuration > 0 && (newVCodec != oldVCodec || newACodec != oldACodec || newRes != oldRes) {
		m.safeProgressSend(DownloadProgress{
			Percentage:     0,
			Speed:          "Analyzing media...",
			ETA:            fmt.Sprintf("Duration: %.0fs", newDuration),
			Size:           "Processing",
			Phase:          phase,
			FFmpegProgress: "Initializing conversion",
			CurrentFrame:   0,
			TotalFrames:    0,
			ProcessingTime: fmt.Sprintf("%.2fs", processingTime.Seconds()),
			VideoCodec:     newVCodec,
			AudioCodec:     newACodec,
			Bitrate:        "",
			Resolution:     newRes,
			FPS:            "",
		})
	}
}

// checkForPostProcessing checks if post-processing is starting
func (m *progressMonitor) checkForPostProcessing(line string) bool {
	for _, postRegex := range m.postProcessRegexes {
		if postRegex.MatchString(line) && !m.isPostProcessing.Load() {
			m.isPostProcessing.Store(true)
			m.updatePhase("processing")
			log.Printf("[DOWNLOAD] %s: Starting post-processing with ffmpeg", m.downloadID)

			if m.statusCallback != nil {
				m.statusCallback(m.downloadID, StatusPostProcessing)
			}

			m.sendPostProcessingStartUpdate()
			return true
		}
	}
	return false
}

// sendPostProcessingStartUpdate sends initial post-processing update
func (m *progressMonitor) sendPostProcessingStartUpdate() {
	// Send initial post-processing progress update - clear download info
	m.safeProgressSend(DownloadProgress{
		Percentage:     0,  // Reset percentage for processing phase
		Speed:          "", // Clear download speed
		ETA:            "", // Clear download ETA
		Size:           "", // Clear download size
		Phase:          "processing",
		FFmpegProgress: "Starting post-processing",
		CurrentFrame:   0,
		TotalFrames:    0,
		ProcessingTime: "0.00s",
		VideoCodec:     "",
		AudioCodec:     "",
		Bitrate:        "",
		Resolution:     "",
		FPS:            "",
	})

	// Send enhanced status update to indicate post-processing
	m.safeProgressSend(DownloadProgress{
		Percentage:     0,
		Speed:          "Converting with FFmpeg",
		ETA:            "Processing",
		Size:           "Converting",
		Phase:          "processing",
		FFmpegProgress: "Starting conversion",
		CurrentFrame:   0,
		TotalFrames:    0,
		ProcessingTime: "0s",
		VideoCodec:     "",
		AudioCodec:     "",
		Bitrate:        "",
		Resolution:     "",
		FPS:            "",
	})
}

// handleFFmpegProcessing handles FFmpeg processing output
func (m *progressMonitor) handleFFmpegProcessing(line string) {
	// FFmpeg activity is parsed from the progress file; nothing to do here.
	_ = line
}

// goytProgressPrefix marks the machine-readable progress line emitted by our
// --progress-template. Parsing this is preferred over the human-readable regexes.
const goytProgressPrefix = "[goyt-dl]"

// processDownloadProgress processes download progress from yt-dlp
func (m *progressMonitor) processDownloadProgress(line string) {
	trimmed := strings.TrimSpace(line)
	if progress, found := strings.CutPrefix(trimmed, goytProgressPrefix); found {
		m.handleTemplateProgress(strings.TrimSpace(progress))
		return
	}

	// Try each regex pattern for progress (fallback for older yt-dlp or when the
	// template field is unavailable).
	for i, progressRegex := range m.progressRegexes {
		if matches := progressRegex.FindStringSubmatch(line); matches != nil {
			if utils.VerboseLogging {
				utils.LogDebugf("[DOWNLOAD] %s: Progress regex %d matched: %s -> %v", m.downloadID, i, line, matches)
			}
			percentage, _ := strconv.ParseFloat(matches[1], 64)

			// Update phase to downloading if not already set
			if !m.isPostProcessing.Load() {
				m.updatePhase("downloading")
			}

			if percentage != m.lastPercentage {
				if utils.VerboseLogging {
					utils.LogDebugf("[DOWNLOAD] %s: Progress %.1f%%", m.downloadID, percentage)
				}
				m.lastPercentage = percentage
			}

			// Check if download just reached 100% completion
			if percentage >= 100.0 && !m.isPostProcessing.Load() {
				m.handleDownloadCompletion()
				return
			}

			// Create progress update
			progress := m.createDownloadProgress(percentage, matches, i)
			m.safeProgressSend(progress)
			return // Exit regex loop once we find a match
		}
	}

	if utils.VerboseLogging && !m.isPostProcessing.Load() && strings.Contains(line, "[download]") {
		utils.LogDebugf("[DOWNLOAD] %s: Unmatched download line: %s", m.downloadID, line)
	}
}

// handleTemplateProgress parses the pipe-delimited payload emitted by our
// --progress-template: downloaded|total|estimate|speed|eta. It computes the
// percentage from exact byte counts and formats the total size for the UI.
func (m *progressMonitor) handleTemplateProgress(payload string) {
	parts := strings.Split(payload, "|")
	if len(parts) < 5 {
		return
	}

	downloaded := parseBytesField(parts[0])
	total := parseBytesField(parts[1])
	if total <= 0 {
		total = parseBytesField(parts[2]) // fall back to the estimate
	}
	speed := cleanTemplateField(parts[3])
	eta := cleanTemplateField(parts[4])

	if !m.isPostProcessing.Load() {
		m.updatePhase("downloading")
	}

	var percentage float64
	if total > 0 {
		percentage = float64(downloaded) / float64(total) * 100
		if percentage > 100 {
			percentage = 100
		}
	}

	if percentage >= 100.0 && downloaded > 0 && !m.isPostProcessing.Load() {
		m.handleDownloadCompletion()
		return
	}

	if percentage != m.lastPercentage {
		m.lastPercentage = percentage
	}

	size := ""
	if total > 0 {
		size = formatBytes(total)
	} else if downloaded > 0 {
		size = formatBytes(downloaded)
	}

	m.safeProgressSend(DownloadProgress{
		Percentage: percentage,
		Speed:      speed,
		ETA:        eta,
		Size:       size,
		Phase:      "downloading",
	})
}

// parseBytesField parses a yt-dlp progress byte field, treating "NA"/"None"/
// empty as unknown (returns -1).
func parseBytesField(s string) int64 {
	s = strings.TrimSpace(s)
	if s == "" || s == "NA" || s == "None" {
		return -1
	}
	v, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return -1
	}
	return int64(v)
}

// cleanTemplateField trims padding and normalizes unknown values to "".
func cleanTemplateField(s string) string {
	s = strings.TrimSpace(s)
	if s == "NA" || s == "None" {
		return ""
	}
	return s
}

// formatBytes renders a byte count as a human-readable size (e.g. "1.2 GB").
func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	units := []string{"KB", "MB", "GB", "TB", "PB"}
	if exp >= len(units) {
		exp = len(units) - 1
	}
	return fmt.Sprintf("%.1f %s", float64(b)/float64(div), units[exp])
}

// handleDownloadCompletion handles when download reaches 100%
func (m *progressMonitor) handleDownloadCompletion() {
	log.Printf("[DOWNLOAD] %s: Download reached 100%%, entering post-processing", m.downloadID)
	m.isPostProcessing.Store(true)
	m.updatePhase("processing")

	if m.statusCallback != nil {
		m.statusCallback(m.downloadID, StatusPostProcessing)
	}

	// Send immediate post-processing progress update
	m.safeProgressSend(DownloadProgress{
		Percentage:     0,  // Reset percentage for processing phase
		Speed:          "", // Clear download speed
		ETA:            "", // Clear download ETA
		Size:           "", // Clear download size
		Phase:          "processing",
		FFmpegProgress: "Starting post-processing",
		CurrentFrame:   0,
		TotalFrames:    0,
		ProcessingTime: "0.00s",
		VideoCodec:     "",
		AudioCodec:     "",
		Bitrate:        "",
		Resolution:     "",
		FPS:            "",
	})
}

// createDownloadProgress creates a DownloadProgress struct from regex matches
func (m *progressMonitor) createDownloadProgress(
	percentage float64, matches []string, regexIndex int,
) DownloadProgress {
	// Create progress update with proper phase separation
	var progress DownloadProgress
	if !m.isPostProcessing.Load() {
		// During download phase: ONLY show download progress
		progress = DownloadProgress{
			Percentage:     percentage,
			Size:           "",
			Phase:          "downloading",
			FFmpegProgress: "",
			CurrentFrame:   0,
			TotalFrames:    0,
			ProcessingTime: "",
			VideoCodec:     "",
			AudioCodec:     "",
			Bitrate:        "",
			Resolution:     "",
			FPS:            "",
			Speed:          "",
			ETA:            "",
		}
	} else {
		// During processing phase: show processing status without download info
		_, _, _, _, _, _, _, _, _, _, _, processingTime := m.readFFmpegProgress()
		progress = DownloadProgress{
			Percentage:     0, // Reset percentage for processing
			Size:           "",
			Phase:          "processing",
			FFmpegProgress: "Post-processing...",
			CurrentFrame:   0,
			TotalFrames:    0,
			ProcessingTime: fmt.Sprintf("%.2fs", processingTime.Seconds()),
			VideoCodec:     "",
			AudioCodec:     "",
			Bitrate:        "",
			Resolution:     "",
			FPS:            "",
			Speed:          "",
			ETA:            "",
		}
	}

	// Handle different match groups based on which regex matched
	if len(matches) >= 3 {
		progress.Size = matches[2] // Size or duration
	}
	if len(matches) >= 4 {
		if regexIndex == 0 { // Main pattern with speed and ETA
			progress.Speed = matches[3]
			if len(matches) >= 5 {
				progress.ETA = matches[4]
			}
		} else if regexIndex == 1 { // Completion pattern with time
			progress.ETA = "Complete"
			progress.Speed = matches[3] // This is actually completion time
		}
	}

	return progress
}

// safeProgressSend safely sends progress updates to the channel
func (m *progressMonitor) safeProgressSend(progress DownloadProgress) {
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[DOWNLOAD] %s: Progress channel closed, skipping update: %v", m.downloadID, r)
			}
		}()
		select {
		case m.progressChan <- progress:
			// Successfully sent progress update
		default:
			// Channel is full, skip this update
			log.Printf("[DOWNLOAD] %s: Progress channel full, skipping update", m.downloadID)
		}
	}()
}

type VideoInfo struct {
	Title    string
	Filename string
}

type PlaylistItem struct {
	ID       string `json:"id"`
	Title    string `json:"title"`
	URL      string `json:"url"`
	Duration string `json:"duration"`
}

// WatchURL returns the direct URL for a playlist item. flat-playlist output
// usually includes a url field; YouTube entries that omit it fall back to a
// watch URL built from the video ID.
func (p PlaylistItem) WatchURL() string {
	if p.URL != "" {
		return p.URL
	}
	return fmt.Sprintf("https://www.youtube.com/watch?v=%s", p.ID)
}

func (d *Downloader) GetVideoInfo(url string) (*VideoInfo, error) {
	// First validate the URL by trying to extract info with a timeout
	if utils.VerboseLogging {
		utils.LogDebugf("[INFO] Getting video info for URL: %s", url)
	}

	// Create a context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), d.videoInfoTimeout)
	defer cancel()

	// Resolve yt-dlp path to absolute path
	ytDlpAbsPath := d.ytDlpPath
	if !filepath.IsAbs(ytDlpAbsPath) {
		if wd, err := os.Getwd(); err == nil {
			ytDlpAbsPath = filepath.Join(wd, ytDlpAbsPath)
		}
	}

	// Validate yt-dlp path before executing (fixes G204)
	if err := utils.ValidateExecutablePath(ytDlpAbsPath); err != nil {
		log.Printf("[INFO] Invalid yt-dlp path: %v", err)
		return nil, fmt.Errorf("invalid yt-dlp path: %w", err)
	}

	infoArgs := append(d.cookieArgs(), d.jsRuntimeArgs()...)
	infoArgs = append(infoArgs, "--get-title", "--get-filename", "--no-warnings", "--no-playlist", url)
	cmd := exec.CommandContext(ctx, ytDlpAbsPath, infoArgs...)
	output, err := cmd.Output()
	if err != nil {
		log.Printf("[INFO] Failed to get video info for %s: %v", url, err)
		// Check if it's a timeout error
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout getting video info for URL: %s", url)
		}
		// A non-zero exit does not prove the URL is unsupported: it can also mean
		// the site needs cookies, is region-locked, rate-limited, or temporarily
		// unavailable. Report it as an unverified probe, not an invalid URL, so
		// callers treat it as advisory rather than a hard failure.
		if strings.Contains(err.Error(), "exit status") {
			return nil, fmt.Errorf("could not verify URL (site may need cookies, be region-locked, or be temporarily unavailable): %s", url)
		}
		return nil, fmt.Errorf("failed to get video info: %w", err)
	}

	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	if utils.VerboseLogging {
		utils.LogDebugf("[INFO] yt-dlp output lines: %v", lines)
	}
	if len(lines) < 2 {
		return nil, fmt.Errorf("unexpected output format from yt-dlp")
	}

	info := &VideoInfo{
		Title:    lines[0],
		Filename: lines[1],
	}
	if utils.VerboseLogging {
		utils.LogDebugf("[INFO] Video info retrieved: Title=%s, Filename=%s", info.Title, info.Filename)
	}
	return info, nil
}

// GenerateID returns a unique download ID. A random suffix is appended to
// the timestamp so IDs cannot collide when downloads are created in a tight
// loop (e.g. queuing a playlist).
func GenerateID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return fmt.Sprintf("%d-%x", time.Now().UnixNano(), b)
}

// IsPlaylistURL checks if a URL is a playlist
func (d *Downloader) IsPlaylistURL(url string) bool {
	// Check for common playlist URL patterns
	return strings.Contains(url, "list=") || strings.Contains(url, "playlist?")
}

// GetPlaylistItemsWithLimit extracts a limited number of items from a playlist for validation
func (d *Downloader) GetPlaylistItemsWithLimit(url string, limit int) ([]PlaylistItem, error) {
	if utils.VerboseLogging {
		utils.LogDebugf("[PLAYLIST] Getting first %d playlist items for URL: %s", limit, url)
	}

	// Create a context with shorter timeout for validation
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	// Use yt-dlp to extract playlist info in JSON format with limit
	args := d.cookieArgs()
	args = append(args, d.jsRuntimeArgs()...)
	args = append(args,
		"--flat-playlist",
		"--dump-json",
		"--no-warnings",
		"--playlist-end", fmt.Sprintf("%d", limit),
		url,
	)

	// Resolve yt-dlp path to absolute path
	ytDlpAbsPath := d.ytDlpPath
	if !filepath.IsAbs(ytDlpAbsPath) {
		if wd, err := os.Getwd(); err == nil {
			ytDlpAbsPath = filepath.Join(wd, ytDlpAbsPath)
		}
	}

	// Validate yt-dlp path before executing (fixes G204)
	if err := utils.ValidateExecutablePath(ytDlpAbsPath); err != nil {
		return nil, fmt.Errorf("invalid yt-dlp path: %w", err)
	}

	cmd := exec.CommandContext(ctx, ytDlpAbsPath, args...)
	output, err := cmd.Output()
	if err != nil {
		utils.LogError("[PLAYLIST] Failed to get limited playlist items for %s: %v", url, err)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout getting playlist items for URL: %s", url)
		}
		return nil, fmt.Errorf("failed to get playlist items: %w", err)
	}

	var items []PlaylistItem
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var entry struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			URL      string `json:"url"`
			Duration string `json:"duration_string"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			if utils.VerboseLogging {
				utils.LogDebugf("[PLAYLIST] Failed to parse JSON line: %s, error: %v", line, err)
			}
			continue
		}

		items = append(items, PlaylistItem{
			ID:       entry.ID,
			Title:    entry.Title,
			URL:      entry.URL,
			Duration: entry.Duration,
		})
	}

	if utils.VerboseLogging {
		utils.LogDebugf("[PLAYLIST] Found %d items in limited playlist query", len(items))
	}
	return items, nil
}

// GetPlaylistItems extracts all items from a playlist
func (d *Downloader) GetPlaylistItems(url string) ([]PlaylistItem, error) {
	utils.LogInfo("[PLAYLIST] Getting playlist items for URL: %s", url)

	// Create a context with timeout to prevent hanging
	ctx, cancel := context.WithTimeout(context.Background(), d.playlistTimeout)
	defer cancel()

	// Resolve yt-dlp path to absolute path
	ytDlpAbsPath := d.ytDlpPath
	if !filepath.IsAbs(ytDlpAbsPath) {
		if wd, err := os.Getwd(); err == nil {
			ytDlpAbsPath = filepath.Join(wd, ytDlpAbsPath)
		}
	}

	// Validate yt-dlp path before executing (fixes G204)
	if err := utils.ValidateExecutablePath(ytDlpAbsPath); err != nil {
		return nil, fmt.Errorf("invalid yt-dlp path: %w", err)
	}

	// Use yt-dlp to extract playlist info in JSON format
	enumArgs := append(d.cookieArgs(), d.jsRuntimeArgs()...)
	enumArgs = append(enumArgs, "--flat-playlist", "--dump-json", "--no-warnings", url)
	cmd := exec.CommandContext(ctx, ytDlpAbsPath, enumArgs...)
	output, err := cmd.Output()
	if err != nil {
		utils.LogError("[PLAYLIST] Failed to get playlist items for %s: %v", url, err)
		if ctx.Err() == context.DeadlineExceeded {
			return nil, fmt.Errorf("timeout getting playlist items for URL: %s", url)
		}
		return nil, fmt.Errorf("failed to get playlist items: %w", err)
	}

	var items []PlaylistItem
	for line := range strings.SplitSeq(strings.TrimSpace(string(output)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}

		var entry struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			URL      string `json:"url"`
			Duration string `json:"duration_string"`
		}

		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			if utils.VerboseLogging {
				utils.LogDebugf("[PLAYLIST] Failed to parse JSON line: %s, error: %v", line, err)
			}
			continue
		}

		items = append(items, PlaylistItem{
			ID:       entry.ID,
			Title:    entry.Title,
			URL:      entry.URL,
			Duration: entry.Duration,
		})
	}

	utils.LogInfo("[PLAYLIST] Found %d items in playlist", len(items))
	return items, nil
}

// monitorFFmpegProgress monitors the FFMPEG progress file for real-time progress updates
// monitorFFmpegProgress follows ffmpeg's -progress file. announceStart sends a
// "starting" heartbeat until the first frame is encoded; the download path
// starts this monitor before yt-dlp hands off to ffmpeg, so it passes false
// to keep download progress intact.

// conversionETA converts remaining media seconds into wall-clock time using
// ffmpeg's speed ("0.405x" means 0.405 media seconds per second). Without a
// usable speed it falls back to the media time.
func conversionETA(remainingMedia float64, speed string) string {
	if remainingMedia <= 0 {
		return ""
	}
	if x, err := strconv.ParseFloat(strings.TrimSuffix(strings.TrimSpace(speed), "x"), 64); err == nil && x > 0 {
		remainingMedia /= x
	}
	return (time.Duration(remainingMedia * float64(time.Second))).Round(time.Second).String()
}

// elapsedLabel formats wall-clock processing time, e.g. "1m23s".
func elapsedLabel(d time.Duration) string {
	return d.Round(time.Second).String()
}

func (d *Downloader) monitorFFmpegProgress(
	ctx context.Context,
	progressFile string,
	progressChan chan<- DownloadProgress,
	downloadID string,
	duration float64,
	announceStart bool,
) {
	// Always remove the progress file when monitoring stops, regardless of
	// whether ffmpeg ever wrote to it.
	defer os.Remove(progressFile)

	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()

	var lastSize int64
	var ffmpegData = make(map[string]string)
	started := time.Now()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}

		// Check if file exists and has grown
		// Until the first frame is encoded ffmpeg reports nothing useful,
		// and decoding plus tone-mapping 4K HDR can take a while to get there.
		if f := ffmpegData["frame"]; announceStart && (f == "" || f == "0") {
			starting := DownloadProgress{
				Phase:          "starting",
				FFmpegProgress: "Starting encoder: decoding and filtering the first frames...",
				ProcessingTime: elapsedLabel(time.Since(started)),
			}
			select {
			case progressChan <- starting:
			default:
			}
		}

		fileInfo, err := os.Stat(progressFile)
		if err != nil {
			continue // File doesn't exist yet
		}

		currentSize := fileInfo.Size()
		if currentSize == lastSize {
			continue // No new data
		}

		// Read the entire file (FFMPEG overwrites it)
		content, err := os.ReadFile(progressFile)
		if err != nil {
			continue
		}

		// Parse key=value format
		for line := range strings.SplitSeq(string(content), "\n") {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}

			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				key := strings.TrimSpace(parts[0])
				value := strings.TrimSpace(parts[1])
				ffmpegData[key] = value
			}
		}

		// Parse progress data
		if f := ffmpegData["frame"]; len(ffmpegData) > 0 && f != "" && f != "0" {
			d.parseFFmpegProgressData(ffmpegData, progressChan, downloadID, duration, started)
		}

		lastSize = currentSize

		// Check if processing is complete
		if progress, exists := ffmpegData["progress"]; exists && progress == "end" {
			// Send final 100% progress before completing
			finalProgress := DownloadProgress{
				Percentage:     100.0,
				Phase:          "finalizing",
				FFmpegProgress: "Finalizing conversion...",
				ProcessingTime: elapsedLabel(time.Since(started)),
			}
			func() {
				defer func() {
					if r := recover(); r != nil {
						log.Printf("[DOWNLOAD] %s: Progress channel closed during final progress update: %v", downloadID, r)
					}
				}()
				select {
				case progressChan <- finalProgress:
				default:
					// Channel is full, skip
				}
			}()

			return
		}
	}
}

// parseFFmpegProgressData parses FFMPEG progress data and sends updates
func (d *Downloader) parseFFmpegProgressData(
	data map[string]string,
	progressChan chan<- DownloadProgress,
	downloadID string,
	duration float64,
	started time.Time,
) {
	var progress DownloadProgress

	// Parse current time in microseconds
	if outTimeUs, exists := data["out_time_us"]; exists && outTimeUs != StatusNA {
		if timeUs, err := strconv.ParseInt(outTimeUs, 10, 64); err == nil {
			currentTime := float64(timeUs) / 1000000.0 // Convert to seconds

			// Calculate percentage
			if duration > 0 {
				rawPercentage := (currentTime / duration) * 100
				// Once encoding reaches the end, ffmpeg still rewrites the file
				// for +faststart without reporting progress; say so.
				if rawPercentage >= 99 {
					progress.Phase = "finalizing"
					progress.FFmpegProgress = "Finalizing file..."
				}
				// Cap at 95% during processing to indicate there's still work remaining
				if rawPercentage > 95 {
					rawPercentage = 95
				}
				// Round to 1 decimal place to avoid floating point precision issues
				progress.Percentage = math.Round(rawPercentage*10) / 10
			}

			// Remaining media time runs at the encoder's speed, not real time.
			if progress.Percentage > 0 && progress.Percentage < 100 {
				progress.ETA = conversionETA(duration-currentTime, data["speed"])
			}

			progress.ProcessingTime = elapsedLabel(time.Since(started))
		}
	}

	// Parse frame count
	if frame, exists := data["frame"]; exists {
		if frameNum, err := strconv.ParseInt(frame, 10, 64); err == nil {
			progress.CurrentFrame = frameNum
		}
	}

	// Parse speed
	if speed, exists := data["speed"]; exists && speed != StatusNA {
		progress.Speed = speed
	}

	// Parse bitrate
	if bitrate, exists := data["bitrate"]; exists && bitrate != StatusNA {
		progress.Bitrate = bitrate
	}

	// Parse FPS
	if fps, exists := data["fps"]; exists && fps != StatusNA {
		progress.FPS = fps + "fps"
	}

	// Set phase
	progress.Phase = "processing"
	progress.FFmpegProgress = fmt.Sprintf("Frame %d (%.1f%%)", progress.CurrentFrame, progress.Percentage)

	// Send progress update
	func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[FFMPEG] %s: Progress channel closed during FFmpeg progress update: %v", downloadID, r)
			}
		}()
		select {
		case progressChan <- progress:
		default:
			// Channel is full, skip
		}
	}()
}
