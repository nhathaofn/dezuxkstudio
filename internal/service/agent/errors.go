package agent

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"
)

// errTurnCancelled marks a turn the user stopped; it is reported as a normal completion, not an error.
var errTurnCancelled = errors.New("agent turn cancelled")

// authError marks a failure the provider itself reported as missing/invalid credentials.
// Only errors coming back from the provider process are classified, so an unrelated error that merely
// contains "unauthorized" (a tool, a URL, a file name) never flips the provider to signed-out.
type authError struct{ err error }

func (e *authError) Error() string { return e.err.Error() }
func (e *authError) Unwrap() error { return e.err }

// providerAuthPatterns are the phrases the supported CLIs/agents use for missing credentials.
var providerAuthPatterns = []string{
	"unauthenticated",
	"unauthorized",
	"authentication_failed",
	"authentication required",
	"not logged in",
	"not signed in",
	"login required",
	"please run /login",
}

// classifyProviderError wraps an error returned by a provider process (ACP call or CLI run) as an
// authError when it reports missing credentials. Call it only at provider boundaries.
func classifyProviderError(err error) error {
	if err == nil || isAuthError(err) {
		return err
	}
	msg := strings.ToLower(err.Error())
	for _, p := range providerAuthPatterns {
		if strings.Contains(msg, p) {
			return &authError{err: err}
		}
	}
	return err
}

func isAuthError(err error) bool {
	var ae *authError
	return errors.As(err, &ae)
}

// turnResult maps the outcome of a turn: a user cancel becomes errTurnCancelled, an expired turn a
// readable timeout, and provider errors are classified.
func turnResult(ctx context.Context, err error, timeout time.Duration) error {
	switch {
	case errors.Is(ctx.Err(), context.Canceled):
		return errTurnCancelled
	case errors.Is(ctx.Err(), context.DeadlineExceeded):
		return localize(fmt.Errorf("agent turn timed out after %s", timeout), msgTurnTimeout, map[string]string{"duration": timeout.String()})
	}
	return classifyProviderError(err)
}
