package proc

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func script(t *testing.T, name, win, sh string) string {
	t.Helper()
	dir := t.TempDir()
	if runtime.GOOS == "windows" {
		p := filepath.Join(dir, name+".cmd")
		if err := os.WriteFile(p, []byte(win), 0o755); err != nil {
			t.Fatal(err)
		}
		return p
	}
	p := filepath.Join(dir, name)
	if err := os.WriteFile(p, []byte(sh), 0o755); err != nil {
		t.Fatal(err)
	}
	return p
}

// Batch files (npm shims such as claude.cmd, or a user-picked ffmpeg.cmd) run with arguments.
func TestCommandRunsScriptsWithArgs(t *testing.T) {
	exe := script(t, "tool", "@echo off\r\necho tool-version %1\r\n", "#!/bin/sh\necho tool-version $1\n")
	out, err := Command(context.Background(), exe, "-version").CombinedOutput()
	if err != nil {
		t.Fatalf("%v: %s", err, out)
	}
	if !strings.Contains(string(out), "tool-version -version") {
		t.Fatalf("output %q", out)
	}
}

// KillTree stops the whole tree: a script that starts a long-lived child must not leave it behind.
func TestKillTreeStopsDescendants(t *testing.T) {
	exe := script(t, "parent",
		"@echo off\r\nping -n 30 127.0.0.1 >nul\r\n",
		"#!/bin/sh\nsleep 30 &\nwait\n")
	cmd := Command(context.Background(), exe)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	start := time.Now()
	KillTree(cmd)
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("process tree still running after KillTree")
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("KillTree too slow")
	}
	KillTree(nil) // safe on nil
}
