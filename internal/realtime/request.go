package realtime

import (
	"app/internal/contracts"
)

// Request describes the action being executed. Long-running handlers report
// progress through it and must honour ctx cancellation (client cancel or timeout).
type Request struct {
	ID       string
	Action   string
	ClientID string
	progress func(contracts.ActionProgress)
}

// Progress emits an action:progress event (percent is clamped to 0-100) to the caller only.
func (r *Request) Progress(percent float64, message string) {
	r.ProgressKeyed(percent, message, "", nil)
}

// ProgressKeyed is Progress with a UI translation key; message is the fallback text.
func (r *Request) ProgressKeyed(percent float64, message, key string, params map[string]string) {
	if r == nil || r.progress == nil {
		return
	}
	switch {
	case percent < 0:
		percent = 0
	case percent > 100:
		percent = 100
	}
	r.progress(contracts.ActionProgress{Percent: percent, Message: message, MsgKey: key, MsgParams: params})
}
