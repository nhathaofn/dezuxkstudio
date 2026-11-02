package runner

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
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

	// Edge case: without speed parameter
	lineNoSpeed := "frame= 123 time=00:00:25.00 bitrate= 100kbits/s"
	pct2, msg2, ok2 := parser.ParseLine(lineNoSpeed)
	if !ok2 || pct2 != 25.0 {
		t.Fatalf("expected 25%% without speed, got pct=%f, ok=%v", pct2, ok2)
	}
	if len(msg2) == 0 {
		t.Fatalf("expected non-empty message for no-speed line")
	}

	// Edge case: zero total duration
	zeroParser := &FFmpegProgressParser{TotalDuration: 0}
	pctZero, _, okZero := zeroParser.ParseLine(line)
	if !okZero || pctZero != 0.0 {
		t.Fatalf("expected 0%% progress when TotalDuration is 0, got %f", pctZero)
	}

	// Edge case: time exceeding total duration caps at 100%
	shortParser := &FFmpegProgressParser{TotalDuration: 10 * time.Second}
	pctCapped, _, okCapped := shortParser.ParseLine("time=00:00:20.00")
	if !okCapped || pctCapped != 100.0 {
		t.Fatalf("expected capped 100%% progress, got %f", pctCapped)
	}

	// Edge case: non-matching line
	_, _, okNonMatch := parser.ParseLine("just some random stdout log without time")
	if okNonMatch {
		t.Fatalf("expected ok=false for non-matching line")
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

	// Edge case: non-matching line
	_, _, okNonMatch := parser.ParseLine("[info] Extracting URL...")
	if okNonMatch {
		t.Fatalf("expected ok=false for non-matching line")
	}
}

func TestRunnerLargeLine(t *testing.T) {
	if os.Getenv("RUNNER_TEST_HELPER") == "1" {
		const lineSize = 250 * 1024 // 250KB > default 64KB bufio.MaxScanTokenSize
		fmt.Println(strings.Repeat("A", lineSize))
		os.Exit(0)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	var receivedLines []string
	opts := RunOptions{
		Env: append(os.Environ(), "RUNNER_TEST_HELPER=1"),
		OnStdoutLine: func(line string) {
			receivedLines = append(receivedLines, line)
		},
	}

	res, err := RunWithOptions(ctx, os.Args[0], []string{"-test.run=^TestRunnerLargeLine$"}, opts)
	if err != nil {
		t.Fatalf("RunWithOptions failed on >64KB line: %v", err)
	}
	if res.ExitCode != 0 {
		t.Fatalf("expected exit code 0, got %d (stderr: %s)", res.ExitCode, res.Stderr)
	}

	expectedLen := 250 * 1024
	found := false
	for _, l := range receivedLines {
		if len(l) == expectedLen {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected to receive line of length %d without truncation, got %d lines received", expectedLen, len(receivedLines))
	}
	if !strings.Contains(res.Stdout, strings.Repeat("A", expectedLen)) {
		t.Fatalf("expected stdout to contain full 250KB line without truncation")
	}
}
