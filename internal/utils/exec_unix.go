//go:build !windows

package utils

import (
	"fmt"
	"os"
)

// checkExecutable verifies the path is a regular file with an execute bit set.
func checkExecutable(path string) error {
	fileInfo, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !fileInfo.Mode().IsRegular() {
		return fmt.Errorf("path is not a regular file")
	}
	if fileInfo.Mode().Perm()&0o111 == 0 {
		return fmt.Errorf("file does not have execute permissions")
	}
	return nil
}
