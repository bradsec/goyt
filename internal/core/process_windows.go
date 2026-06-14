//go:build windows

package core

import (
	"log"
	"os/exec"
	"syscall"
)

// setupProcessGroup configures the command for proper process management on Windows
func setupProcessGroup(cmd *exec.Cmd, downloadID string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}

	// Custom cancel function for Windows
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			log.Printf("[DOWNLOAD] %s: Terminating process %d", downloadID, cmd.Process.Pid)
			return cmd.Process.Kill()
		}
		return nil
	}
}
