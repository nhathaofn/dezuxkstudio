package runner

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"

	"app/internal/progress"
)

type pctParser struct{}

func (pctParser) ParseLine(line string) (float64, string, bool) {
	v, err := strconv.ParseFloat(strings.TrimSpace(strings.TrimPrefix(line, "PCT ")), 64)
	return v, "step", err == nil && strings.HasPrefix(line, "PCT ")
}

// Without an explicit OnProgress, parsed progress goes to the context's sink, so a tool run
// re-arms the action timeout exactly like a download does. Also runs a .cmd script through
// the shared proc package (used to fail before runner and agent shared it).
func TestParsedProgressReachesContextSink(t *testing.T) {
	dir := t.TempDir()
	var exe string
	if runtime.GOOS == "windows" {
		exe = filepath.Join(dir, "tool.cmd")
		_ = os.WriteFile(exe, []byte("@echo off\r\necho PCT 25\r\necho PCT 75\r\n"), 0o755)
	} else {
		exe = filepath.Join(dir, "tool")
		_ = os.WriteFile(exe, []byte("#!/bin/sh\necho 'PCT 25'\necho 'PCT 75'\n"), 0o755)
	}
	var got []float64
	ctx := progress.With(context.Background(), progress.Sink{Report: func(u progress.Update) { got = append(got, u.Percent) }})
	res, err := RunWithOptions(ctx, exe, nil, RunOptions{ProgressParser: pctParser{}})
	if err != nil || res.ExitCode != 0 {
		t.Fatalf("run: %v %+v", err, res)
	}
	if len(got) != 2 || got[0] != 25 || got[1] != 75 {
		t.Fatalf("progress %v", got)
	}
}
