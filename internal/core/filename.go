package core

import (
	"path/filepath"
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
	if name, potentialExt, found := strings.CutLast(filename, "."); found {
		potentialExt = "." + potentialExt
		// Only treat it as an extension if it's a common file extension
		// and is plain ASCII alphanumeric: titles are remote input that becomes
		// the yt-dlp output template, so "/" or "%(" must never survive here.
		if len(potentialExt) > 1 && len(potentialExt) <= 6 && isASCIIAlnum(potentialExt[1:]) {
			ext = potentialExt
			filename = name
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

func isASCIIAlnum(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII || !(unicode.IsLetter(r) || unicode.IsDigit(r)) {
			return false
		}
	}
	return true
}

// qualitySuffixRe matches a suffix written by QualityFilename, so a later
// rename (after Convert) replaces it instead of stacking a second one.
var qualitySuffixRe = regexp.MustCompile(`^(.*) \[(?:(?:8K|4K|\d{3,4}p|HDR|mono|stereo|5\.1|7\.1|\d+ channels|\d+kbps) ?)+\]$`)

// QualityFilename returns path with the measured quality appended before the
// extension, e.g. "dino [4K HDR 5.1 384kbps].mp4". Labels come from ffprobe,
// so the suffix only ever holds letters, digits, spaces and the dot in "5.1".
// It returns path unchanged when nothing is known.
func QualityFilename(path string, info MediaInfo) string {
	var parts []string
	if info.Resolution != "" {
		parts = append(parts, info.Resolution)
	}
	if info.HDR {
		parts = append(parts, "HDR")
	}
	switch info.AudioChannels {
	case "Stereo", "Mono":
		parts = append(parts, strings.ToLower(info.AudioChannels))
	case "":
	default:
		parts = append(parts, info.AudioChannels)
	}
	if info.AudioBitrate != "" {
		parts = append(parts, strings.ReplaceAll(info.AudioBitrate, " ", ""))
	}
	if len(parts) == 0 {
		return path
	}
	ext := filepath.Ext(path)
	base := strings.TrimSuffix(path, ext)
	if m := qualitySuffixRe.FindStringSubmatch(base); m != nil {
		base = m[1]
	}
	return base + " [" + strings.Join(parts, " ") + "]" + ext
}
