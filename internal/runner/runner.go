// Package runner manages the lifecycle, execution, and output streaming of external CLI processes.
package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strconv"
	"sync"
	"time"
)

// ProgressCallback receives percentage (0-100) and an optional status message.
type ProgressCallback func(percent float64, message string)

// LineParser parses a line from stdout/stderr and extracts progress if present.
type LineParser interface {
	ParseLine(line string) (percent float64, message string, ok bool)
}

// RunOptions configures process execution.
type RunOptions struct {
	Dir            string
	Env            []string
	OnStdoutLine   func(line string)
	OnStderrLine   func(line string)
	ProgressParser LineParser
	OnProgress     ProgressCallback
}

// RunResult holds the output and exit status of a command execution.
type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
}

// Run executes a command with default options and returns its output.
func Run(ctx context.Context, executable string, args ...string) (*RunResult, error) {
	return RunWithOptions(ctx, executable, args, RunOptions{})
}

// RunWithOptions executes a command with full lifecycle management, line streaming, and cancellation.
func RunWithOptions(ctx context.Context, executable string, args []string, opts RunOptions) (*RunResult, error) {
	cmd := exec.Command(executable, args...)
	if opts.Dir != "" {
		cmd.Dir = opts.Dir
	}
	if len(opts.Env) > 0 {
		cmd.Env = opts.Env
	}
	prepareCmd(cmd)

	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("runner: stdout pipe: %w", err)
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("runner: stderr pipe: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("runner: start %s: %w", executable, err)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	var wg sync.WaitGroup

	readLines := func(r io.Reader, buf *bytes.Buffer, onLine func(string), checkProgress bool) {
		defer wg.Done()
		scanner := bufio.NewScanner(r)
		// Custom split that handles both \n and \r (progress indicators like yt-dlp or ffmpeg often use \r)
		scanner.Split(scanLinesOrCarriageReturn)
		for scanner.Scan() {
			line := scanner.Text()
			buf.WriteString(line)
			buf.WriteByte('\n')

			if onLine != nil {
				onLine(line)
			}
			if checkProgress && opts.ProgressParser != nil && opts.OnProgress != nil {
				if pct, msg, ok := opts.ProgressParser.ParseLine(line); ok {
					opts.OnProgress(pct, msg)
				}
			}
		}
	}

	wg.Add(2)
	go readLines(stdoutPipe, &stdoutBuf, opts.OnStdoutLine, true)
	go readLines(stderrPipe, &stderrBuf, opts.OnStderrLine, true)

	// Wait in a goroutine so we can observe context cancellation
	done := make(chan error, 1)
	go func() {
		wg.Wait()
		done <- cmd.Wait()
	}()

	select {
	case <-ctx.Done():
		_ = killProcessTree(cmd)
		<-done // ensure wait finishes
		return &RunResult{
			Stdout:   stdoutBuf.String(),
			Stderr:   stderrBuf.String(),
			ExitCode: -1,
		}, ctx.Err()

	case err := <-done:
		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				exitCode = -1
			}
		}
		res := &RunResult{
			Stdout:   stdoutBuf.String(),
			Stderr:   stderrBuf.String(),
			ExitCode: exitCode,
		}
		if err != nil {
			return res, fmt.Errorf("runner: command failed with code %d: %w", exitCode, err)
		}
		return res, nil
	}
}

// scanLinesOrCarriageReturn splits on either \n or \r so carriage-return progress lines are yielded.
func scanLinesOrCarriageReturn(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	for i := 0; i < len(data); i++ {
		if data[i] == '\r' || data[i] == '\n' {
			advance = i + 1
			// Consume consecutive \r\n
			if data[i] == '\r' && i+1 < len(data) && data[i+1] == '\n' {
				advance = i + 2
			}
			return advance, data[0:i], nil
		}
	}
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil
}

// FFmpegProgressParser parses progress from FFmpeg stderr output.
type FFmpegProgressParser struct {
	TotalDuration time.Duration
}

var (
	ffmpegTimeRe  = regexp.MustCompile(`time=(\d{2}):(\d{2}):(\d{2}(?:\.\d+)?)`)
	ffmpegSpeedRe = regexp.MustCompile(`speed=\s*([^\s]+)`)
)

// ParseLine implements LineParser for FFmpeg.
func (p *FFmpegProgressParser) ParseLine(line string) (float64, string, bool) {
	matches := ffmpegTimeRe.FindStringSubmatch(line)
	if len(matches) < 4 {
		return 0, "", false
	}
	h, _ := strconv.Atoi(matches[1])
	m, _ := strconv.Atoi(matches[2])
	s, _ := strconv.ParseFloat(matches[3], 64)

	currentTime := time.Duration(h)*time.Hour + time.Duration(m)*time.Minute + time.Duration(s*float64(time.Second))
	speed := ""
	if sm := ffmpegSpeedRe.FindStringSubmatch(line); len(sm) > 1 {
		speed = sm[1]
	}

	pct := 0.0
	if p.TotalDuration > 0 {
		pct = (float64(currentTime) / float64(p.TotalDuration)) * 100
		if pct > 100 {
			pct = 100
		}
	}

	msg := fmt.Sprintf("Time: %02d:%02d:%02.0f", h, m, s)
	if speed != "" {
		msg += fmt.Sprintf(" (speed: %s)", speed)
	}
	return pct, msg, true
}

// YtDlpProgressParser parses progress from yt-dlp stdout output.
type YtDlpProgressParser struct{}

var ytDlpProgressRe = regexp.MustCompile(`\[download\]\s+(\d+\.?\d*)%\s+of\s+([^\s]+)\s+at\s+([^\s]+)`)

// ParseLine implements LineParser for yt-dlp.
func (p *YtDlpProgressParser) ParseLine(line string) (float64, string, bool) {
	matches := ytDlpProgressRe.FindStringSubmatch(line)
	if len(matches) < 4 {
		return 0, "", false
	}
	pct, err := strconv.ParseFloat(matches[1], 64)
	if err != nil {
		return 0, "", false
	}
	size := matches[2]
	speed := matches[3]
	msg := fmt.Sprintf("%s / %s at %s", matches[1]+"%", size, speed)
	return pct, msg, true
}
