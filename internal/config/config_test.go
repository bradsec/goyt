package config

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.MaxConcurrentDownloads != 3 {
		t.Errorf("Expected MaxConcurrentDownloads to be 3, got %d", cfg.MaxConcurrentDownloads)
	}

	if cfg.Port != 3000 {
		t.Errorf("Expected Port to be 3000, got %d", cfg.Port)
	}

	if cfg.DefaultVideoFormat != "mp4" {
		t.Errorf("Expected DefaultVideoFormat to be 'mp4', got '%s'", cfg.DefaultVideoFormat)
	}

	if cfg.DefaultAudioFormat != "mp3" {
		t.Errorf("Expected DefaultAudioFormat to be 'mp3', got '%s'", cfg.DefaultAudioFormat)
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name    string
		config  Config
		wantErr bool
	}{
		{
			name:    "valid config",
			config:  *DefaultConfig(),
			wantErr: false,
		},
		{
			name: "empty download path",
			config: Config{
				DownloadPath:           "",
				MaxConcurrentDownloads: 3,
				Port:                   3000,
			},
			wantErr: true,
		},
		{
			name: "invalid concurrent downloads",
			config: Config{
				DownloadPath:           "/tmp/test",
				MaxConcurrentDownloads: 0,
				Port:                   3000,
			},
			wantErr: true,
		},
		{
			name: "invalid port",
			config: Config{
				DownloadPath:           "/tmp/test",
				MaxConcurrentDownloads: 3,
				Port:                   0,
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.config.Validate()
			if (err != nil) != tt.wantErr {
				t.Errorf("Config.Validate() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestConfigSaveLoad(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "test_config.json")

	originalConfig := DefaultConfig()
	originalConfig.Port = 9090
	originalConfig.MaxConcurrentDownloads = 5

	// Save config
	if err := originalConfig.Save(configPath); err != nil {
		t.Fatalf("Failed to save config: %v", err)
	}

	// Load config
	loadedConfig, err := Load(configPath)
	if err != nil {
		t.Fatalf("Failed to load config: %v", err)
	}

	if loadedConfig.Port != 9090 {
		t.Errorf("Expected Port to be 9090, got %d", loadedConfig.Port)
	}

	if loadedConfig.MaxConcurrentDownloads != 5 {
		t.Errorf("Expected MaxConcurrentDownloads to be 5, got %d", loadedConfig.MaxConcurrentDownloads)
	}
}

func TestAuthEnabled(t *testing.T) {
	t.Setenv("WEBUI_PASSWORD", "")
	cfg := DefaultConfig()
	if cfg.AuthEnabled() {
		t.Error("expected auth disabled with no hash and no env")
	}
	cfg.WebUIPasswordHash = "pbkdf2-sha256$600000$abc$def"
	if !cfg.AuthEnabled() {
		t.Error("expected auth enabled when hash set")
	}
	cfg.WebUIPasswordHash = ""
	t.Setenv("WEBUI_PASSWORD", "secret")
	if !cfg.AuthEnabled() {
		t.Error("expected auth enabled when WEBUI_PASSWORD set")
	}
}

func TestResolveSessionSecret(t *testing.T) {
	cfg := DefaultConfig()
	s1 := cfg.ResolveSessionSecret()
	if len(s1) != 32 {
		t.Fatalf("expected 32-byte secret, got %d", len(s1))
	}
	s1b := DefaultConfig().ResolveSessionSecret()
	if bytes.Equal(s1, s1b) {
		t.Error("expected two ephemeral secrets to differ")
	}
	cfg.SessionSecret = "AAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAA=" // 32 zero bytes, base64
	s2 := cfg.ResolveSessionSecret()
	if !bytes.Equal(s2, make([]byte, 32)) {
		t.Fatalf("expected persisted secret to equal 32 zero bytes, got %x", s2)
	}
}

func TestLoadNonExistentConfig(t *testing.T) {
	tempDir := t.TempDir()
	configPath := filepath.Join(tempDir, "nonexistent.json")

	config, err := Load(configPath)
	if err != nil {
		t.Fatalf("Expected Load to create default config, got error: %v", err)
	}

	if config.Port != 3000 {
		t.Errorf("Expected default port 3000, got %d", config.Port)
	}

	// Check that file was created
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Error("Expected config file to be created")
	}
}
