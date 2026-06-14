//go:build !windows

package core

import (
	"log"
	"os/exec"
	"syscall"
)

// setupProcessGroup configures the command for proper process group management on Unix systems
func setupProcessGroup(cmd *exec.Cmd, downloadID string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid: true, // Create new process group
	}

	// Custom cancel function to kill the entire process group
	cmd.Cancel = func() error {
		if cmd.Process != nil {
			log.Printf("[DOWNLOAD] %s: Terminating process group %d", downloadID, cmd.Process.Pid)
			// Kill the entire process group (negative PID kills the process group)
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
				// If SIGTERM fails, use SIGKILL as fallback
				log.Printf("[DOWNLOAD] %s: SIGTERM failed, using SIGKILL: %v", downloadID, err)
				return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			}
		}
		return nil
	}
}
