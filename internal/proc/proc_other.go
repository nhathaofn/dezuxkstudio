//go:build !windows

package proc

import (
	"os/exec"
	"syscall"
)

func launcher(executable string, _ []string) string { return executable }

func launchArgs(executable string, args []string) []string {
	return append([]string{executable}, args...)
}

func configure(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func killTree(cmd *exec.Cmd) {
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
