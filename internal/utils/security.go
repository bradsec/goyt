package utils

import (
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// URL validation patterns
var (
	// Only allow HTTPS. ValidateURL is used for the yt-dlp updater, which only
	// ever fetches GitHub over TLS, so plain HTTP is rejected to avoid downgrade.
	validURLSchemes = regexp.MustCompile(`^https://`)
	// Whitelist of allowed domains for downloads
	allowedDomains = []string{
		"github.com",
		"api.github.com",
	}
)

// ValidateURL validates that a URL is safe for HTTP requests
func ValidateURL(rawURL string) error {
	if rawURL == "" {
		return fmt.Errorf("URL cannot be empty")
	}

	// Parse the URL
	parsedURL, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL format: %w", err)
	}

	// Check scheme
	if !validURLSchemes.MatchString(rawURL) {
		return fmt.Errorf("URL must use the HTTPS scheme")
	}

	// Check if domain is in allowlist
	hostname := strings.ToLower(parsedURL.Hostname())
	allowed := false
	for _, domain := range allowedDomains {
		if hostname == domain || strings.HasSuffix(hostname, "."+domain) {
			allowed = true
			break
		}
	}

	if !allowed {
		return fmt.Errorf("domain %s is not in allowlist", hostname)
	}

	return nil
}

// ValidateExecutablePath validates that a path is safe to execute
func ValidateExecutablePath(path string) error {
	if path == "" {
		return fmt.Errorf("executable path cannot be empty")
	}

	// Clean the path to remove any directory traversal attempts
	cleanPath := filepath.Clean(path)

	// Check if path contains any directory traversal patterns
	if strings.Contains(path, "..") || strings.Contains(path, "//") {
		return fmt.Errorf("path contains invalid characters or directory traversal: %s", path)
	}

	// Check if the path is absolute (required for security)
	if !filepath.IsAbs(cleanPath) {
		return fmt.Errorf("executable path must be absolute: %s", cleanPath)
	}

	// Check if file exists
	if _, err := os.Stat(cleanPath); os.IsNotExist(err) {
		return fmt.Errorf("executable does not exist: %s", cleanPath)
	}

	// Check if file is executable
	if err := checkExecutable(cleanPath); err != nil {
		return fmt.Errorf("file is not executable: %s", cleanPath)
	}

	return nil
}

// checkExecutable verifies that a file is executable. It is implemented per
// platform: Unix checks the execute permission bit, Windows checks the file
// extension because Windows files have no execute bit.
