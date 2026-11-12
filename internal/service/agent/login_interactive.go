package agent

import (
	"context"
	"fmt"
	"io"
	"regexp"
	"strings"
	"sync"

	"app/internal/apperr"
	domainagent "app/internal/domain/agent"
	"app/internal/proc"
	"app/internal/safego"
)

// Interactive sign-in. The CLI is run in the app's isolated profile; what it prints (the page to open,
// a one-time code) is passed to the UI as a LoginPrompt, and a code the user pastes back is written to
// the CLI's stdin. Nothing here knows a CLI by name: the command and whether it wants a pasted code come
// from the registry (ProviderSpec.Login).

var (
	// ANSI colour/cursor sequences and OSC-8 hyperlinks that terminals render but a UI must not show.
	reANSI = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)`)
	reURL  = regexp.MustCompile(`https://[^\s<>"']+`)
	// A one-time device code such as EB40-Z9TLH: groups of letters/digits joined by a dash.
	reCode = regexp.MustCompile(`\b[A-Z0-9]{4,}-[A-Z0-9]{4,}\b`)
)

// maxLoginOutput bounds how much of a CLI's sign-in output is kept for parsing.
const maxLoginOutput = 64 << 10

// maxPastedCode bounds a pasted authorization code.
const maxPastedCode = 2048

// loginSession is one running sign-in.
type loginSession struct {
	cancel context.CancelFunc
	mu     sync.Mutex
	stdin  io.WriteCloser
}

// parseLoginPrompt extracts the page and one-time code from a CLI's sign-in output.
func parseLoginPrompt(output string) (url, code string) {
	clean := reANSI.ReplaceAllString(output, "")
	if m := reURL.FindString(clean); m != "" {
		url = strings.TrimRight(m, ".,;)")
	}
	code = reCode.FindString(clean)
	return url, code
}

// interactiveLogin runs the provider's sign-in command and returns when the CLI exits. The UI is told
// what to do through onLoginPrompt; SubmitLoginCode and CancelLogin act on the running session.
func (s *Service) interactiveLogin(ctx context.Context, id domainagent.ProviderID, executable string, env []string) error {
	spec := domainagent.Registry[id].Login
	if len(spec.Args) == 0 {
		return apperr.Validation("this provider has no interactive sign-in command")
	}
	runCtx, cancel := context.WithCancel(ctx) // the caller bounds ctx (Limits.LoginTimeout)
	defer cancel()
	sess := &loginSession{cancel: cancel}

	s.loginMu.Lock()
	if prev := s.logins[id]; prev != nil {
		prev.cancel() // a second attempt replaces a forgotten first one
	}
	s.logins[id] = sess
	s.loginMu.Unlock()
	defer func() {
		s.loginMu.Lock()
		if s.logins[id] == sess {
			delete(s.logins, id)
		}
		s.loginMu.Unlock()
	}()

	cmd := proc.Command(runCtx, executable, spec.Args...)
	cmd.Env = env
	out, w := io.Pipe()
	cmd.Stdout, cmd.Stderr = w, w
	if spec.PasteCode {
		stdin, err := cmd.StdinPipe()
		if err != nil {
			return fmt.Errorf("%s sign-in: %w", id, err)
		}
		sess.stdin = stdin
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("%s sign-in could not start: %w", id, err)
	}

	var tail strings.Builder
	read := make(chan struct{})
	safego.Go("agent-login-output", func() {
		defer close(read)
		s.watchLoginOutput(id, spec, out, &tail)
	})
	waitErr := cmd.Wait()
	_ = w.Close()
	<-read
	if waitErr != nil {
		if runCtx.Err() != nil && ctx.Err() == nil {
			return fmt.Errorf("%s sign-in was cancelled", id)
		}
		return fmt.Errorf("%s sign-in failed: %s", id, loginFailureDetail(tail.String(), waitErr))
	}
	return nil
}

// watchLoginOutput reads the CLI's output, tells the UI whenever the page or code it shows changes, and
// keeps the last part for an error message.
func (s *Service) watchLoginOutput(id domainagent.ProviderID, spec domainagent.LoginSpec, r io.Reader, tail *strings.Builder) {
	var seen strings.Builder
	var last domainagent.LoginPrompt
	buf := make([]byte, 4096)
	for {
		n, err := r.Read(buf)
		if n > 0 {
			seen.Write(buf[:n])
			if seen.Len() > maxLoginOutput {
				keep := seen.String()[seen.Len()-maxLoginOutput/2:]
				seen.Reset()
				seen.WriteString(keep)
			}
			url, code := parseLoginPrompt(seen.String())
			if url != "" || code != "" {
				p := domainagent.LoginPrompt{ProviderID: id, URL: url, Code: code, PasteCode: spec.PasteCode, OpenURL: !spec.OpensBrowser}
				if p != last {
					last = p
					s.emitLoginPrompt(p)
				}
			}
		}
		if err != nil {
			break
		}
	}
	tail.WriteString(reANSI.ReplaceAllString(seen.String(), ""))
}

// loginFailureDetail is the last meaningful line the CLI printed, with anything key-like removed.
func loginFailureDetail(output string, err error) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if line := strings.TrimSpace(lines[i]); line != "" {
			return redactSecrets(firstLineTrimmedMax(line, 300))
		}
	}
	return err.Error()
}

func (s *Service) emitLoginPrompt(p domainagent.LoginPrompt) {
	if s.onLoginPrompt != nil {
		s.onLoginPrompt(p)
	}
}

func (s *Service) loginSessionFor(id domainagent.ProviderID) *loginSession {
	s.loginMu.Lock()
	defer s.loginMu.Unlock()
	return s.logins[id]
}

// SubmitLoginCode hands the code the user copied from the sign-in page to the running sign-in.
func (s *Service) SubmitLoginCode(_ context.Context, id domainagent.ProviderID, code string) error {
	if err := domainagent.ValidateProviderID(id); err != nil {
		return err
	}
	code = strings.TrimSpace(strings.NewReplacer("\r", "", "\n", "").Replace(code))
	if code == "" || len(code) > maxPastedCode {
		return apperr.Validation("the sign-in code is empty or too long")
	}
	sess := s.loginSessionFor(id)
	if sess == nil || sess.stdin == nil {
		return apperr.NotFound("no sign-in is waiting for a code")
	}
	sess.mu.Lock()
	defer sess.mu.Unlock()
	if _, err := io.WriteString(sess.stdin, code+"\n"); err != nil {
		return apperr.Unavailable("the sign-in is no longer running")
	}
	return nil
}

// CancelLogin stops a running sign-in (the user closed the dialog).
func (s *Service) CancelLogin(_ context.Context, id domainagent.ProviderID) {
	if sess := s.loginSessionFor(id); sess != nil {
		sess.cancel()
	}
}
