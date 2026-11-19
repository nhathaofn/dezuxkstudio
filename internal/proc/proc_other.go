//go:build !windows

package proc

import "os/exec"
import "syscall"

func resolveLaunch(executable string, args []string) (string, []string) {
	return executable, append([]string{executable}, args...)
}

func configure(cmd *exec.Cmd, _ string, _ []string) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

func attachJob(*exec.Cmd)  {}
func releaseJob(*exec.Cmd) {}

func killTree(cmd *exec.Cmd) {
	if pgid, err := syscall.Getpgid(cmd.Process.Pid); err == nil {
		_ = syscall.Kill(-pgid, syscall.SIGKILL)
	}
}
