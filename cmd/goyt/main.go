package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	"goyt/internal/api"
	"goyt/internal/auth"
	"goyt/internal/config"
	"goyt/internal/core"
	"goyt/internal/manager"
	"goyt/internal/ui"
	"goyt/internal/utils"
)

func main() {
	if len(os.Args) > 1 {
		switch os.Args[1] {
		case "setpass":
			runSetPass(os.Args[2:])
			return
		case "clearpass":
			runClearPass(os.Args[2:])
			return
		}
	}

	var port int
	var bindAddress string
	var configPath string
	flag.IntVar(&port, "port", 0, "Port to run the server on (overrides config file)")
	flag.StringVar(&bindAddress, "bind", "", "Address to bind to, e.g. 0.0.0.0 for all interfaces (overrides config file)")
	flag.StringVar(&configPath, "config", "config.json", "Path to configuration file")
	flag.Parse()

	printBanner()

	cfg, err := config.Load(configPath)
	if err != nil {
		log.Fatalf("Failed to load configuration: %v", err)
	}

	// Precedence: CLI flag > environment variable > config file.
	if envPort := os.Getenv("GOYT_PORT"); envPort != "" {
		if parsedPort, err := strconv.Atoi(envPort); err == nil && parsedPort > 0 {
			cfg.Port = parsedPort
		}
	}
	if port > 0 {
		cfg.Port = port
	}
	if envBind := os.Getenv("GOYT_BIND"); envBind != "" {
		cfg.BindAddress = envBind
	}
	if bindAddress != "" {
		cfg.BindAddress = bindAddress
	}
	// Default to loopback so the server is not exposed on all interfaces
	// without an explicit opt-in (e.g. "0.0.0.0").
	if cfg.BindAddress == "" {
		cfg.BindAddress = "127.0.0.1"
	}

	if err := cfg.Validate(); err != nil {
		log.Fatalf("Invalid configuration: %v", err)
	}
	if err := cfg.EnsureDirs(); err != nil {
		log.Fatalf("Failed to create directories: %v", err)
	}

	utils.SetVerboseLogging(cfg.VerboseLogging)

	updater := core.NewYtDlpUpdater(cfg.YtDlpPath, filepath.Dir(cfg.YtDlpPath))

	ensureYtDlp(cfg, updater)
	// Resolve ffmpeg before building the downloader so a freshly downloaded
	// ffmpeg path is picked up. Exits the process if ffmpeg is unavailable.
	ensureFfmpeg(cfg, configPath)

	downloader := core.NewDownloader(cfg.YtDlpPath, cfg.FfmpegPath, cfg.CookiesFilePath, cfg.EnableHardwareAccel, cfg.OptimizeForLowPower)
	downloader.SetTimeouts(
		time.Duration(cfg.DownloadStartTimeoutSeconds)*time.Second,
		time.Duration(cfg.PlaylistLoadTimeoutSeconds)*time.Second)
	downloadManager := manager.NewDownloadManager(downloader, cfg.MaxConcurrentDownloads, cfg.DownloadPath, cfg)

	apiHandler := api.NewHandler(cfg, configPath, downloadManager, updater)
	sessionSecret := apiHandler.SessionSecret()
	uiHandler := ui.NewTemplateHandler(cfg)

	mainMux := http.NewServeMux()
	apiRouter := api.SetupRoutes(apiHandler, ui.Assets)
	mainMux.Handle("/api/", apiRouter)
	mainMux.Handle("/health", apiRouter)
	mainMux.Handle("/assets/", apiRouter)
	mainMux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		if !cfg.AuthEnabled() || hasValidSession(sessionSecret, r) {
			http.Redirect(w, r, "/", http.StatusFound)
			return
		}
		uiHandler.ServeLogin(w, r)
	})
	mainMux.HandleFunc("GET /{$}", func(w http.ResponseWriter, r *http.Request) {
		if cfg.AuthEnabled() && !hasValidSession(sessionSecret, r) {
			http.Redirect(w, r, "/login", http.StatusFound)
			return
		}
		uiHandler.ServeIndex(w, r)
	})
	mainMux.HandleFunc("GET /favicon.ico", uiHandler.ServeFavicon)

	addr := net.JoinHostPort(cfg.BindAddress, strconv.Itoa(cfg.Port))
	server := &http.Server{
		Addr:              addr,
		Handler:           mainMux,
		ReadHeaderTimeout: 30 * time.Second,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		log.Printf("Failed to start server on %s: %v", addr, err)
		log.Printf("To change host/port: edit %s, use -bind/-port, or set GOYT_BIND/GOYT_PORT", configPath)
		os.Exit(1)
	}

	displayHost := cfg.BindAddress
	if displayHost == "0.0.0.0" || displayHost == "::" {
		displayHost = "localhost"
	}
	log.Printf("goyt %s listening on http://%s", api.Version, net.JoinHostPort(displayHost, strconv.Itoa(cfg.Port)))
	log.Printf("Download path: %s | yt-dlp: %s | ffmpeg: %s | workers: %d",
		cfg.DownloadPath, cfg.YtDlpPath, cfg.FfmpegPath, cfg.MaxConcurrentDownloads)

	serverErrChan := make(chan error, 1)
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			serverErrChan <- err
		}
	}()

	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	select {
	case sig := <-sigChan:
		log.Printf("Received %s, shutting down gracefully...", sig)
	case err := <-serverErrChan:
		log.Printf("Server error: %v, shutting down...", err)
	}

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer shutdownCancel()

	downloadManager.Shutdown()
	if err := server.Shutdown(shutdownCtx); err != nil {
		log.Printf("Error during server shutdown: %v", err)
	}
	log.Printf("Shutdown complete")
}

// runSetPass prompts for a new web UI password (hidden input, confirmed) and
// writes its hash plus a session secret to the config file.
func runSetPass(argv []string) {
	fs := flag.NewFlagSet("setpass", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "Path to configuration file")
	_ = fs.Parse(argv)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}

	pw, err := readPasswordTwice()
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}

	hash, err := auth.Hash(pw)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to hash password: %v\n", err)
		os.Exit(1)
	}
	cfg.WebUIPasswordHash = hash
	if cfg.SessionSecret == "" {
		secret, err := config.GenerateSessionSecret()
		if err != nil {
			fmt.Fprintf(os.Stderr, "Failed to generate session secret: %v\n", err)
			os.Exit(1)
		}
		cfg.SessionSecret = secret
	}
	if err := cfg.Save(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Web UI password set. Restart goyt for it to take effect.")
}

// runClearPass removes the stored password hash, disabling auth unless the
// WEBUI_PASSWORD environment variable is set.
func runClearPass(argv []string) {
	fs := flag.NewFlagSet("clearpass", flag.ExitOnError)
	configPath := fs.String("config", "config.json", "Path to configuration file")
	_ = fs.Parse(argv)

	cfg, err := config.Load(*configPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to load config: %v\n", err)
		os.Exit(1)
	}
	cfg.WebUIPasswordHash = ""
	if err := cfg.Save(*configPath); err != nil {
		fmt.Fprintf(os.Stderr, "Failed to save config: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Web UI password cleared. Restart goyt for it to take effect.")
	if os.Getenv("WEBUI_PASSWORD") != "" {
		fmt.Fprintln(os.Stderr, "Warning: WEBUI_PASSWORD env var is set; auth remains enabled.")
	}
}

// readPasswordTwice reads a password and its confirmation without echoing,
// falling back to plain stdin when not attached to a terminal (e.g. piped).
func readPasswordTwice() (string, error) {
	if term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Print("New password: ")
		first, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("failed to read password: %w", err)
		}
		fmt.Print("Confirm password: ")
		second, err := term.ReadPassword(int(os.Stdin.Fd()))
		fmt.Println()
		if err != nil {
			return "", fmt.Errorf("failed to read password: %w", err)
		}
		if string(first) != string(second) {
			return "", errors.New("passwords do not match")
		}
		if len(first) == 0 {
			return "", errors.New("password cannot be empty")
		}
		return string(first), nil
	}

	scanner := bufio.NewScanner(os.Stdin)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return "", fmt.Errorf("failed to read password from stdin: %w", err)
		}
		return "", errors.New("no password provided on stdin")
	}
	pw := scanner.Text()
	if pw == "" {
		return "", errors.New("password cannot be empty")
	}
	return pw, nil
}

// hasValidSession reports whether the request carries a valid session cookie.
func hasValidSession(secret []byte, r *http.Request) bool {
	cookie, err := r.Cookie("goyt_session")
	if err != nil {
		return false
	}
	return auth.Validate(secret, cookie.Value)
}

// ensureYtDlp downloads yt-dlp if missing, otherwise checks for updates.
// Failures are reported but never fatal; the UI exposes a manual update.
func ensureYtDlp(cfg *config.Config, updater *core.YtDlpUpdater) {
	if _, err := os.Stat(cfg.YtDlpPath); os.IsNotExist(err) {
		log.Printf("yt-dlp not found at %s, downloading...", cfg.YtDlpPath)
		if err := updater.Update(); err != nil {
			log.Printf("Warning: failed to download yt-dlp: %v", err)
			log.Printf("Download it manually from https://github.com/yt-dlp/yt-dlp and place it at %s", cfg.YtDlpPath)
		} else {
			log.Printf("yt-dlp downloaded to %s", cfg.YtDlpPath)
		}
		return
	}

	updateInfo, err := updater.CheckForUpdates()
	if err != nil {
		log.Printf("Could not check for yt-dlp updates: %v", err)
		return
	}
	if updateInfo.UpdateAvailable {
		log.Printf("Updating yt-dlp %s -> %s...", updateInfo.CurrentVersion, updateInfo.LatestVersion)
		if err := updater.Update(); err != nil {
			log.Printf("Warning: failed to update yt-dlp: %v", err)
		} else {
			log.Printf("yt-dlp updated to %s", updateInfo.LatestVersion)
		}
	} else {
		log.Printf("yt-dlp is up to date (%s)", updateInfo.CurrentVersion)
	}
}

// ensureFfmpeg verifies ffmpeg is available. If not, on Windows it offers an
// integrity-checked automatic download (after explicit consent showing the
// source); on every other OS, and if the download is declined or unavailable,
// it prints install guidance and terminates so the user installs ffmpeg and
// restarts. ffmpeg is required for downloads that convert or merge streams.
func ensureFfmpeg(cfg *config.Config, configPath string) {
	if core.CheckFfmpegAvailable(cfg.FfmpegPath) {
		if v := core.GetVersionInfo(cfg.YtDlpPath, cfg.FfmpegPath).FfmpegVersion; v != "" {
			log.Printf("ffmpeg available (%s)", v)
		} else {
			log.Printf("ffmpeg available")
		}
		return
	}

	log.Printf("ffmpeg not found at %q or on PATH. ffmpeg is required to convert and merge downloads.", cfg.FfmpegPath)

	if core.FFmpegAutoDownloadSupported() && term.IsTerminal(int(os.Stdin.Fd())) {
		fmt.Printf("\nffmpeg can be downloaded automatically from gyan.dev (the Windows build linked from ffmpeg.org):\n  %s\nThe archive is verified against its published SHA-256 before use (~100 MB).\n", core.FFmpegSourceURL())
		if promptYesNo("Download ffmpeg now? [y/N]: ") {
			destDir := filepath.Join("assets", "ffmpeg")
			log.Printf("Downloading ffmpeg from %s ...", core.FFmpegSourceURL())
			ffmpegPath, err := core.DownloadFFmpeg(destDir)
			if err != nil {
				log.Printf("ffmpeg download failed: %v", err)
				ffmpegInstallExit(cfg)
			}
			cfg.FfmpegPath = ffmpegPath
			if err := cfg.Save(configPath); err != nil {
				log.Printf("Warning: ffmpeg installed but config could not be saved: %v", err)
			}
			log.Printf("ffmpeg installed at %s", ffmpegPath)
			return
		}
	}

	ffmpegInstallExit(cfg)
}

// ffmpegInstallExit prints platform-specific install guidance and terminates.
func ffmpegInstallExit(cfg *config.Config) {
	switch runtime.GOOS {
	case "darwin":
		log.Printf("Install ffmpeg, then restart goyt: brew install ffmpeg")
	case "windows":
		log.Printf("Install ffmpeg from https://ffmpeg.org/download.html (or run goyt in a terminal to download it automatically), then restart goyt.")
	default:
		log.Printf("Install ffmpeg with your package manager (e.g. 'sudo apt install ffmpeg' or 'sudo dnf install ffmpeg'), then restart goyt.")
	}
	log.Printf("You can also set ffmpeg_path in your config to an existing ffmpeg binary.")
	os.Exit(1)
}

// promptYesNo reads a single line from stdin and reports whether it is yes.
func promptYesNo(prompt string) bool {
	fmt.Print(prompt)
	reader := bufio.NewReader(os.Stdin)
	line, err := reader.ReadString('\n')
	if err != nil {
		return false
	}
	answer := strings.ToLower(strings.TrimSpace(line))
	return answer == "y" || answer == "yes"
}
