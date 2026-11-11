//go:build windows

package agent

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func buildCommand(ctx context.Context, executable string, args ...string) *exec.Cmd {
	ext := strings.ToLower(filepath.Ext(executable))
	var cmd *exec.Cmd
	if ext == ".cmd" || ext == ".bat" {
		cmdArgs := append([]string{"/c", executable}, args...)
		cmd = exec.CommandContext(ctx, "cmd.exe", cmdArgs...)
	} else {
		cmd = exec.CommandContext(ctx, executable, args...)
	}
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
	return cmd
}

func killCommandTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killCmd := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	killCmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = killCmd.Run()
	_ = cmd.Process.Kill()
}
