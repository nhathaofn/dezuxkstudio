// Package proc is the single place that builds and kills external processes (agent CLIs,
// ffmpeg, yt-dlp...). Every caller gets the same OS behaviour: no console window, its own
// process group, batch-file support on Windows, and whole-tree termination.
package proc

import (
	"context"
	"os/exec"
)

// Command returns an *exec.Cmd for executable bound to ctx (cancelling ctx kills the whole
// process tree). On Windows, .cmd/.bat files resolve npm shims when possible or run through
// cmd.exe with escaped arguments and no console window.
func Command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	launchExe, launchArgv := resolveLaunch(executable, args)
	cmd := exec.CommandContext(ctx, launchExe)
	cmd.Args = launchArgv
	configure(cmd, executable, args)
	cmd.Cancel = func() error {
		KillTree(cmd)
		return nil
	}
	return cmd
}

// Start launches cmd and attaches it to an OS-level process-tree guard (on Windows,
// a Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE) so descendants are reaped even
// if the parent crashes. Call Release(cmd) after Wait() completes.
func Start(cmd *exec.Cmd) error {
	if cmd == nil {
		return nil
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	attachJob(cmd)
	return nil
}

// Release frees any OS process-tree guard attached to cmd by Start.
func Release(cmd *exec.Cmd) {
	if cmd == nil {
		return
	}
	releaseJob(cmd)
}

// KillTree force-terminates cmd's process and all of its descendants. Safe on nil or
// not-started commands.
func KillTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killTree(cmd)
	releaseJob(cmd)
	_ = cmd.Process.Kill()
}
