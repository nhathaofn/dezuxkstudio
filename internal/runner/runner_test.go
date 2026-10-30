package runner

import (
	"context"
	"runtime"
	"testing"
	"time"
)

func TestRunnerExecution(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	var res *RunResult
	var err error
	if runtime.GOOS == "windows" {
		res, err = Run(ctx, "cmd.exe", "/c", "echo hello world")
	} else {
		res, err = Run(ctx, "echo", "hello world")
	}
	if err != nil {
		t.Fatalf("run failed: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d", res.ExitCode)
	}
	if len(res.Stdout) == 0 {
		t.Fatalf("expected stdout output, got empty")
	}
}

func TestRunnerCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())

	go func() {
		time.Sleep(100 * time.Millisecond)
		cancel()
	}()

	var err error
	if runtime.GOOS == "windows" {
		_, err = Run(ctx, "cmd.exe", "/c", "ping -n 10 127.0.0.1 >nul")
	} else {
		_, err = Run(ctx, "sleep", "10")
	}

	if err == nil {
		t.Fatal("expected cancellation error, got nil")
	}
}

func TestFFmpegProgressParser(t *testing.T) {
	parser := &FFmpegProgressParser{TotalDuration: 100 * time.Second}
	line := "frame=  123 fps= 24 q=28.0 size=    1024kB time=00:00:50.00 bitrate= 167.8kbits/s speed=1.5x"
	pct, msg, ok := parser.ParseLine(line)
	if !ok {
		t.Fatalf("failed to parse progress line: %s", line)
	}
	if pct != 50.0 {
		t.Fatalf("expected 50%% progress, got %f", pct)
	}
	if len(msg) == 0 {
		t.Fatalf("expected non-empty message")
	}
}

func TestYtDlpProgressParser(t *testing.T) {
	parser := &YtDlpProgressParser{}
	line := "[download]  45.5% of ~10.00MiB at 2.50MiB/s ETA 00:02"
	pct, msg, ok := parser.ParseLine(line)
	if !ok {
		t.Fatalf("failed to parse progress line: %s", line)
	}
	if pct != 45.5 {
		t.Fatalf("expected 45.5%% progress, got %f", pct)
	}
	if len(msg) == 0 {
		t.Fatalf("expected non-empty message")
	}
}
