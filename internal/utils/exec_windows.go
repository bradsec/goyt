//go:build windows

package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// checkExecutable verifies the path is a regular file with a Windows executable
// extension. Windows has no Unix execute permission bit, so checking
// Perm()&0o111 (as the Unix build does) would reject every binary, including
// the downloaded yt-dlp.exe.
func checkExecutable(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file")
	}
	switch strings.ToLower(filepath.Ext(path)) {
	case ".exe", ".com", ".bat", ".cmd":
		return nil
	default:
		return fmt.Errorf("file %q is not an executable type", path)
	}
}
