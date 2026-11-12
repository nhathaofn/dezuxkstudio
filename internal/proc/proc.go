// Package proc is the single place that builds and kills external processes (agent CLIs,
// ffmpeg, yt-dlp...). Every caller gets the same OS behaviour: no console window, its own
// process group, batch-file support on Windows, and whole-tree termination.
package proc

import (
	"context"
	"os/exec"
)

// Command returns an *exec.Cmd for executable bound to ctx (cancelling ctx kills the direct
// child; use KillTree to also stop its descendants). On Windows, .cmd/.bat files run through
// cmd.exe and no console window is shown.
func Command(ctx context.Context, executable string, args ...string) *exec.Cmd {
	if ctx == nil {
		ctx = context.Background()
	}
	cmd := exec.CommandContext(ctx, launcher(executable, args))
	cmd.Args = launchArgs(executable, args)
	configure(cmd)
	return cmd
}

// KillTree force-terminates cmd's process and all of its descendants. Safe on nil or
// not-started commands.
func KillTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	killTree(cmd)
	_ = cmd.Process.Kill()
}
