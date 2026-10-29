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
	if r == nil || r.progress == nil {
		return
	}
	switch {
	case percent < 0:
		percent = 0
	case percent > 100:
		percent = 100
	}
	r.progress(contracts.ActionProgress{Percent: percent, Message: message})
}
