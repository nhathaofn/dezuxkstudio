//go:build windows

package runner

import (
	"os/exec"
	"strconv"
	"syscall"
)

// prepareCmd configures OS-specific process attributes.
func prepareCmd(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

// killProcessTree terminates the process and any descendants on Windows.
func killProcessTree(cmd *exec.Cmd) error {
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	// Use taskkill to cleanly force-terminate the process tree (/T = tree, /F = force).
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = killCmd.Run()
	// Also invoke Process.Kill directly to ensure local handle clean-up.
	_ = cmd.Process.Kill()
	return nil
}
