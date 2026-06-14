package config

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Config struct {
	DownloadPath             string `json:"download_path"`
	MaxConcurrentDownloads   int    `json:"max_concurrent_downloads"`
	YtDlpPath                string `json:"yt_dlp_path"`
	FfmpegPath               string `json:"ffmpeg_path"`
	CookiesFilePath          string `json:"cookies_file_path"`
	BindAddress              string `json:"bind_address"`
	Port                     int    `json:"port"`
	DefaultVideoFormat       string `json:"default_video_format"`
	DefaultAudioFormat       string `json:"default_audio_format"`
	DefaultVideoQuality      string `json:"default_video_quality"`
	VerboseLogging           bool   `json:"verbose_logging"`
	CompletedFileExpiryHours int    `json:"completed_file_expiry_hours"`
	EnableHardwareAccel      bool   `json:"enable_hardware_acceleration"`
	OptimizeForLowPower      bool   `json:"optimize_for_low_power"`
	// ReencodeForCompatibility re-encodes video to H.264 + AAC for maximum
	// device compatibility. Off by default: video is remuxed into the chosen
	// container by stream copy, which is far faster, especially for several
	// concurrent downloads. Audio extraction is unaffected.
	ReencodeForCompatibility bool `json:"reencode_for_compatibility"`
	// Network operation timeouts in seconds. Slow networks or large playlists
	// may need these raised. PlaylistLoadTimeoutSeconds bounds playlist
	// enumeration; DownloadStartTimeoutSeconds bounds the video-info probe done
	// before a download is queued.
	PlaylistLoadTimeoutSeconds  int `json:"playlist_load_timeout_seconds"`
	DownloadStartTimeoutSeconds int `json:"download_start_timeout_seconds"`
	// Managed by the CLI (setpass/clearpass), not the settings UI. The API layer
	// strips these from GetConfig and preserves them across UpdateConfig saves.
	WebUIPasswordHash string `json:"webui_password_hash,omitempty"`
	SessionSecret     string `json:"session_secret,omitempty"`
}

func DefaultConfig() *Config {
	return &Config{
		DownloadPath:                "./downloads",
		MaxConcurrentDownloads:      3,
		YtDlpPath:                   DefaultYtDlpPath(),
		FfmpegPath:                  defaultFfmpegPath(),
		BindAddress:                 "127.0.0.1",
		Port:                        3000,
		DefaultVideoFormat:          "mp4",
		DefaultAudioFormat:          "mp3",
		DefaultVideoQuality:         "1080p",
		VerboseLogging:              false,
		CompletedFileExpiryHours:    72,
		EnableHardwareAccel:         false,
		OptimizeForLowPower:         false,
		ReencodeForCompatibility:    false,
		PlaylistLoadTimeoutSeconds:  180,
		DownloadStartTimeoutSeconds: 60,
	}
}

// Timeout bounds shared by config validation and the settings UI.
const (
	MinTimeoutSeconds = 10
	MaxTimeoutSeconds = 1800
)

// DefaultYtDlpPath returns the managed yt-dlp binary location. The binary is
// downloaded by the updater at startup, not here.
func DefaultYtDlpPath() string {
	name := "yt-dlp"
	if runtime.GOOS == "windows" {
		name = "yt-dlp.exe"
	}
	return filepath.Join("assets", "yt-dlp", name)
}

func defaultFfmpegPath() string {
	if runtime.GOOS == "windows" {
		return "ffmpeg.exe"
	}
	return "ffmpeg"
}

// Load reads the config file at configPath, creating it with defaults if it
// does not exist. Missing fields in an existing file fall back to defaults so
// older config files keep working after upgrades.
func Load(configPath string) (*Config, error) {
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		config := DefaultConfig()
		if err := config.Save(configPath); err != nil {
			return nil, fmt.Errorf("failed to create default config: %w", err)
		}
		return config, nil
	}

	data, err := os.ReadFile(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read config file: %w", err)
	}

	config := DefaultConfig()
	if err := json.Unmarshal(data, config); err != nil {
		return nil, fmt.Errorf("failed to parse config file: %w", err)
	}

	return config, nil
}

func (c *Config) Save(configPath string) error {
	data, err := json.MarshalIndent(c, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	dir := filepath.Dir(configPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create config directory: %w", err)
	}

	if err := os.WriteFile(configPath, data, 0600); err != nil {
		return fmt.Errorf("failed to write config file: %w", err)
	}

	return nil
}

// Validate checks the configuration for invalid values. It has no side
// effects; directory creation is handled by EnsureDirs.
func (c *Config) Validate() error {
	if c.DownloadPath == "" {
		return fmt.Errorf("download_path cannot be empty")
	}

	if c.MaxConcurrentDownloads <= 0 || c.MaxConcurrentDownloads > 10 {
		return fmt.Errorf("max_concurrent_downloads must be between 1 and 10")
	}

	if c.DefaultVideoFormat == "" {
		return fmt.Errorf("default_video_format cannot be empty")
	}

	if c.DefaultAudioFormat == "" {
		return fmt.Errorf("default_audio_format cannot be empty")
	}

	if c.Port <= 0 || c.Port > 65535 {
		return fmt.Errorf("port must be between 1 and 65535")
	}

	if c.CompletedFileExpiryHours < 0 {
		return fmt.Errorf("completed_file_expiry_hours cannot be negative")
	}

	if c.PlaylistLoadTimeoutSeconds < MinTimeoutSeconds || c.PlaylistLoadTimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("playlist_load_timeout_seconds must be between %d and %d", MinTimeoutSeconds, MaxTimeoutSeconds)
	}

	if c.DownloadStartTimeoutSeconds < MinTimeoutSeconds || c.DownloadStartTimeoutSeconds > MaxTimeoutSeconds {
		return fmt.Errorf("download_start_timeout_seconds must be between %d and %d", MinTimeoutSeconds, MaxTimeoutSeconds)
	}

	return nil
}

// AuthEnabled reports whether the web UI requires a login. Auth is enabled when
// a password hash is stored in config or the WEBUI_PASSWORD env var is set.
func (c *Config) AuthEnabled() bool {
	return c.WebUIPasswordHash != "" || os.Getenv("WEBUI_PASSWORD") != ""
}

// ResolveSessionSecret returns the secret used to sign session cookies. It
// decodes the persisted base64 secret when present; otherwise it returns a
// random ephemeral secret (process lifetime only), which is the case for
// env-only/headless setups where no secret was written to config. A persisted
// secret that is corrupt (not valid base64) or shorter than 32 bytes is
// silently discarded and an ephemeral secret is used instead; this invalidates
// any existing sessions on restart.
func (c *Config) ResolveSessionSecret() []byte {
	if c.SessionSecret != "" {
		if b, err := base64.StdEncoding.DecodeString(c.SessionSecret); err == nil && len(b) >= 32 {
			return b
		}
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic("config: failed to generate session secret: " + err.Error())
	}
	return b
}

// GenerateSessionSecret returns a new base64-encoded 32-byte secret suitable for
// persisting to SessionSecret. Used by the setpass CLI command.
func GenerateSessionSecret() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate session secret: %w", err)
	}
	return base64.StdEncoding.EncodeToString(b), nil
}

// EnsureDirs creates the directories the application needs at runtime.
func (c *Config) EnsureDirs() error {
	if err := os.MkdirAll(c.DownloadPath, 0755); err != nil {
		return fmt.Errorf("failed to create download directory: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(c.YtDlpPath), 0755); err != nil {
		return fmt.Errorf("failed to create yt-dlp directory: %w", err)
	}
	return nil
}
