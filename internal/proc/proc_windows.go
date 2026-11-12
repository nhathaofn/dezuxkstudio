//go:build windows

package proc

import (
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

func isBatch(executable string) bool {
	ext := strings.ToLower(filepath.Ext(executable))
	return ext == ".cmd" || ext == ".bat"
}

func launcher(executable string, _ []string) string {
	if isBatch(executable) {
		return "cmd.exe"
	}
	return executable
}

func launchArgs(executable string, args []string) []string {
	if isBatch(executable) {
		return append([]string{"cmd.exe", "/c", executable}, args...)
	}
	return append([]string{executable}, args...)
}

func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: syscall.CREATE_NEW_PROCESS_GROUP,
	}
}

func killTree(cmd *exec.Cmd) {
	k := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	k.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = k.Run()
}
