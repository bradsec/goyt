package core

import (
	"regexp"
	"strings"
	"unicode"
)

// SanitizeFilename cleans a filename by keeping only ASCII alphanumeric
// characters and spaces. Emojis, symbols, punctuation, and non-Latin scripts
// (which render as mojibake on some filesystems) are removed, runs of
// whitespace are collapsed to a single space, and the result is lowercased.
func SanitizeFilename(filename string) string {
	// Remove file extension temporarily (only if it's a real extension)
	ext := ""
	if lastDot := strings.LastIndex(filename, "."); lastDot != -1 {
		potentialExt := filename[lastDot:]
		// Only treat it as an extension if it's a common file extension
		// and doesn't contain spaces (real extensions don't have spaces)
		if !strings.Contains(potentialExt, " ") && len(potentialExt) <= 6 {
			ext = potentialExt
			filename = filename[:lastDot]
		}
	}

	// Keep only ASCII letters, digits, and spaces. Everything else (emojis,
	// symbols, punctuation, and non-ASCII letters) is dropped.
	var result strings.Builder
	for _, r := range filename {
		switch {
		case r == ' ':
			result.WriteRune(r)
		case r <= unicode.MaxASCII && (unicode.IsLetter(r) || unicode.IsDigit(r)):
			result.WriteRune(r)
		}
	}
	filename = result.String()

	// Replace multiple spaces with single space
	reg := regexp.MustCompile(`\s+`)
	filename = reg.ReplaceAllString(filename, " ")

	// Trim spaces from beginning and end
	filename = strings.TrimSpace(filename)

	// Normalize to lowercase for consistent on-disk names
	filename = strings.ToLower(filename)

	// Check for Windows reserved names
	filename = sanitizeWindowsReservedNames(filename)

	// Ensure filename doesn't exceed reasonable length limits
	if len(filename) > 200 { // Leave room for extension
		filename = filename[:200]
		// Make sure we don't end with a space after truncation
		filename = strings.TrimRight(filename, " ")
	}

	// If filename is empty after sanitization, use a default
	if filename == "" {
		filename = "download"
	}

	return filename + strings.ToLower(ext)
}

// sanitizeWindowsReservedNames handles Windows reserved filenames
func sanitizeWindowsReservedNames(filename string) string {
	// Windows reserved names (case-insensitive)
	windowsReservedNames := []string{
		"CON", "PRN", "AUX", "NUL",
		"COM1", "COM2", "COM3", "COM4", "COM5", "COM6", "COM7", "COM8", "COM9",
		"LPT1", "LPT2", "LPT3", "LPT4", "LPT5", "LPT6", "LPT7", "LPT8", "LPT9",
	}

	// Check if filename (without extension) matches any reserved name
	for _, reserved := range windowsReservedNames {
		if strings.EqualFold(filename, reserved) {
			return filename + " file"
		}
	}

	return filename
}
