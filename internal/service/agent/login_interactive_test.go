package agent

import (
	"context"
	"log/slog"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	domainagent "app/internal/domain/agent"
)

// Real output of `codex login --device-auth` and `claude auth login` (colours and hyperlinks included).
const (
	codexDeviceOutput = "\x1b[1mWelcome to Codex\x1b[0m\r\n\r\nFollow these steps to sign in:\r\n\r\n1. Open this link in your browser and sign in to your account\r\n   \x1b[94mhttps://auth.openai.com/codex/device\x1b[0m\r\n\r\n2. Enter this one-time code (expires in 15 minutes)\r\n   \x1b[94mEB40-Z9TLH\x1b[0m\r\n"
	claudeLoginOutput = "Opening browser to sign in…\r\nIf the browser didn't open, visit: \x1b]8;;https://claude.com/cai/oauth/authorize?code=true&client_id=abc&scope=user%3Aprofile\x07https://claude.com/cai/oauth/authorize?code=true&client_id=abc&scope=user%3Aprofile\x1b]8;;\x07\r\nPaste code here if prompted > "
)

func TestParseLoginPromptReadsPageAndCodeFromRealOutput(t *testing.T) {
	url, code := parseLoginPrompt(codexDeviceOutput)
	if url != "https://auth.openai.com/codex/device" || code != "EB40-Z9TLH" {
		t.Fatalf("codex: url=%q code=%q", url, code)
	}
	url, code = parseLoginPrompt(claudeLoginOutput)
	if !strings.HasPrefix(url, "https://claude.com/cai/oauth/authorize?code=true&client_id=abc") || strings.ContainsAny(url, "\x1b\x07") {
		t.Fatalf("claude: url=%q", url)
	}
	if code != "" {
		t.Fatalf("claude shows no one-time code, got %q", code)
	}
	if u, c := parseLoginPrompt("Starting…\r\n"); u != "" || c != "" {
		t.Fatalf("no prompt expected: %q %q", u, c)
	}
}

// loginFake writes a CLI that prints `out`, optionally waits for a pasted code on stdin, and records it.
func loginFake(t *testing.T, dir string, out string, readCode bool, exit int) (path, record string) {
	t.Helper()
	record = filepath.Join(dir, "pasted.txt")
	if runtime.GOOS == "windows" {
		path = filepath.Join(dir, "fakecli.cmd")
		var b strings.Builder
		b.WriteString("@echo off\r\n")
		for _, line := range strings.Split(out, "|") {
			b.WriteString("echo " + line + "\r\n")
		}
		if readCode {
			b.WriteString("set /p CODE=\r\necho %CODE%> \"" + record + "\"\r\n")
		}
		b.WriteString("exit /b " + string(rune('0'+exit)) + "\r\n")
		if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
			t.Fatal(err)
		}
		return path, record
	}
	path = filepath.Join(dir, "fakecli")
	var b strings.Builder
	b.WriteString("#!/bin/sh\n")
	for _, line := range strings.Split(out, "|") {
		b.WriteString("echo '" + line + "'\n")
	}
	if readCode {
		b.WriteString("read CODE\necho \"$CODE\" > '" + record + "'\n")
	}
	b.WriteString("exit " + string(rune('0'+exit)) + "\n")
	if err := os.WriteFile(path, []byte(b.String()), 0o755); err != nil {
		t.Fatal(err)
	}
	return path, record
}

type loginEnv struct {
	svc *Service
	mu  sync.Mutex
	got []domainagent.LoginPrompt
}

func newLoginEnv(t *testing.T) *loginEnv {
	t.Helper()
	e := &loginEnv{}
	dir := t.TempDir()
	svc, err := New(Options{
		Repo: newFakeRepo(), DataDir: dir, WorkspaceDir: filepath.Join(dir, "ws"), MediaDir: filepath.Join(dir, "media"),
		ProfilesDir: filepath.Join(dir, "profiles"), Logger: slog.New(slog.DiscardHandler),
		OnLoginPrompt: func(p domainagent.LoginPrompt) { e.mu.Lock(); e.got = append(e.got, p); e.mu.Unlock() },
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = svc.Close() })
	e.svc = svc
	return e
}

func (e *loginEnv) last() (domainagent.LoginPrompt, bool) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(e.got) == 0 {
		return domainagent.LoginPrompt{}, false
	}
	return e.got[len(e.got)-1], true
}

func (e *loginEnv) waitPrompt(t *testing.T) domainagent.LoginPrompt {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		if p, ok := e.last(); ok {
			return p
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("the UI was never told what to do")
	return domainagent.LoginPrompt{}
}

func TestDeviceCodeLoginShowsPageAndCodeThenSucceeds(t *testing.T) {
	e := newLoginEnv(t)
	exe, _ := loginFake(t, t.TempDir(), "Open https://auth.example.test/device|Enter this one-time code|AB12-CD34EF", false, 0)
	if err := e.svc.interactiveLogin(context.Background(), domainagent.ProviderCodex, exe, os.Environ()); err != nil {
		t.Fatal(err)
	}
	p := e.waitPrompt(t)
	if p.URL != "https://auth.example.test/device" || p.Code != "AB12-CD34EF" || p.PasteCode || !p.OpenURL || p.ProviderID != domainagent.ProviderCodex {
		t.Fatalf("prompt = %+v (codex shows a code, needs no paste, and the app opens the page)", p)
	}
}

func TestPasteCodeLoginWaitsForTheCodeAndWritesItToTheCLI(t *testing.T) {
	e := newLoginEnv(t)
	exe, record := loginFake(t, t.TempDir(), "Visit https://claude.example.test/authorize?x=1", true, 0)
	done := make(chan error, 1)
	go func() {
		done <- e.svc.interactiveLogin(context.Background(), domainagent.ProviderClaude, exe, os.Environ())
	}()

	p := e.waitPrompt(t)
	if !p.PasteCode || p.OpenURL || p.URL != "https://claude.example.test/authorize?x=1" {
		t.Fatalf("prompt = %+v (claude opens the page itself and wants the code pasted)", p)
	}
	select {
	case err := <-done:
		t.Fatalf("must keep waiting for the code, returned %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	if err := e.svc.SubmitLoginCode(context.Background(), domainagent.ProviderClaude, "  my-auth-code\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("sign-in did not finish after the code was pasted")
	}
	got, _ := os.ReadFile(record)
	if strings.TrimSpace(string(got)) != "my-auth-code" {
		t.Fatalf("the CLI received %q", got)
	}
}

func TestSubmitCodeRejectsEmptyOversizedAndWithoutASession(t *testing.T) {
	e := newLoginEnv(t)
	ctx := context.Background()
	if err := e.svc.SubmitLoginCode(ctx, domainagent.ProviderClaude, "code"); err == nil {
		t.Fatal("no sign-in is running: must fail")
	}
	if err := e.svc.SubmitLoginCode(ctx, domainagent.ProviderClaude, "  \r\n"); err == nil {
		t.Fatal("an empty code must be rejected")
	}
	if err := e.svc.SubmitLoginCode(ctx, domainagent.ProviderClaude, strings.Repeat("x", maxPastedCode+1)); err == nil {
		t.Fatal("an oversized code must be rejected")
	}
	if err := e.svc.SubmitLoginCode(ctx, "nobody", "code"); err == nil {
		t.Fatal("an unknown provider must be rejected")
	}
}

func TestCancelLoginStopsAWaitingSignIn(t *testing.T) {
	e := newLoginEnv(t)
	exe, _ := loginFake(t, t.TempDir(), "Visit https://claude.example.test/authorize", true, 0)
	done := make(chan error, 1)
	go func() {
		done <- e.svc.interactiveLogin(context.Background(), domainagent.ProviderClaude, exe, os.Environ())
	}()
	e.waitPrompt(t)
	e.svc.CancelLogin(context.Background(), domainagent.ProviderClaude)
	select {
	case err := <-done:
		if err == nil || !strings.Contains(err.Error(), "cancel") {
			t.Fatalf("expected a cancelled error, got %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("cancel did not stop the sign-in")
	}
}

func TestFailedLoginReportsTheCLIsLastLine(t *testing.T) {
	e := newLoginEnv(t)
	exe, _ := loginFake(t, t.TempDir(), "Open https://auth.example.test/device|Login was denied by the server", false, 1)
	err := e.svc.interactiveLogin(context.Background(), domainagent.ProviderCodex, exe, os.Environ())
	if err == nil || !strings.Contains(err.Error(), "Login was denied by the server") {
		t.Fatalf("got %v", err)
	}
}
