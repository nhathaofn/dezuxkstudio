package agent

import (
	"errors"

	"app/internal/apperr"
)

// User-facing agent messages carry a frontend i18n key ("agent.err.*" in frontend/src/lib/i18n) plus
// parameters, so the UI shows them in the user's language. The English text stays as the fallback for
// logs, older clients and persisted history. Keys must exist in every frontend locale.
const (
	msgNotSignedIn        = "agent.err.notSignedIn"        // {name}
	msgExecutableNotFound = "agent.err.executableNotFound" // {name}
	msgCustomPathMissing  = "agent.err.customPathMissing"  // {path} {fallback}
	msgCustomPathGone     = "agent.err.customPathGone"     // {path}
	msgTurnTimeout        = "agent.err.turnTimeout"        // {duration}
	msgNotConnected       = "agent.err.notConnected"
	msgBusy               = "agent.err.busy"
	msgPromptEmpty        = "agent.err.promptEmpty"
	msgClearWhileBusy     = "agent.err.clearWhileBusy"
	msgApprovalGone       = "agent.err.approvalGone"
	msgModelChangeBusy    = "agent.err.modelChangeBusy"
	msgProcessExited      = "agent.err.processExited" // {detail}
	msgSuperseded         = "agent.err.superseded"
	msgSignInFailed       = "agent.err.signInFailed" // {detail}
)

// localizedError attaches an i18n key to an error without changing its text.
type localizedError struct {
	key    string
	params map[string]string
	err    error
}

func (e *localizedError) Error() string { return e.err.Error() }
func (e *localizedError) Unwrap() error { return e.err }

func localize(err error, key string, params map[string]string) error {
	if err == nil {
		return nil
	}
	return &localizedError{key: key, params: params, err: err}
}

// messageOf returns the i18n key and params of err, if it carries one.
func messageOf(err error) (string, map[string]string) {
	var le *localizedError
	if errors.As(err, &le) {
		return le.key, le.params
	}
	return "", nil
}

// userError builds an apperr whose details tell the frontend which message to show.
func userError(code apperr.Code, key, english string, params map[string]string) *apperr.Error {
	details := map[string]any{"msg_key": key}
	if len(params) > 0 {
		details["msg_params"] = params
	}
	return apperr.New(code, english).WithDetails(details)
}

// toUserError converts an error to an apperr with code, keeping its i18n key when it has one.
func toUserError(code apperr.Code, err error) *apperr.Error {
	if key, params := messageOf(err); key != "" {
		return userError(code, key, err.Error(), params)
	}
	return apperr.New(code, err.Error())
}
