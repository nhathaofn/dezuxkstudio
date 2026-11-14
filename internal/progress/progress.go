// Package progress carries an action's progress reporter through context.Context, so any
// service or node can report progress without depending on the transport.
//
// This generalises the binaries download model: every report resets the action's idle
// timeout on the server (realtime heartbeat context) and on the client (pending request
// timer). An action's timeout therefore measures silence, not total duration: work that keeps
// reporting may run as long as it needs, work that stalls is still cut off.
package progress

import (
	"context"
	"sync"
	"time"

	"app/internal/safego"
)

// Update is one progress report. Key/Params name a UI translation; Message is the fallback.
type Update struct {
	Percent float64
	Message string
	Key     string
	Params  map[string]string
}

// Sink receives reports for one action. The transport installs it with With.
type Sink struct {
	// Report forwards real progress (shown in the UI) and resets the idle timeout.
	Report func(Update)
	// Beat only resets the idle timeout (nothing is shown in the UI).
	Beat func()
	// Interval is how often KeepAlive beats; it must be well below the action's idle timeout.
	Interval time.Duration
}

type ctxKey struct{}

// With returns ctx carrying sink.
func With(ctx context.Context, sink Sink) context.Context {
	return context.WithValue(ctx, ctxKey{}, sink)
}

func from(ctx context.Context) (Sink, bool) {
	s, ok := ctx.Value(ctxKey{}).(Sink)
	return s, ok
}

// Report sends a progress update; a no-op when ctx carries no sink (tests, background work).
func Report(ctx context.Context, u Update) {
	if s, ok := from(ctx); ok && s.Report != nil {
		s.Report(u)
	}
}

// Func adapts Report to the (percent, message) callbacks used by older APIs.
func Func(ctx context.Context) func(percent float64, message string) {
	return func(percent float64, message string) {
		Report(ctx, Update{Percent: percent, Message: message})
	}
}

// Beat resets the idle timeout without showing anything.
func Beat(ctx context.Context) {
	if s, ok := from(ctx); ok && s.Beat != nil {
		s.Beat()
	}
}

// KeepAlive beats every Sink.Interval until stop is called or ctx ends.
//
// Use it ONLY around a wait that is already bounded by its own deadline (agent.login_timeout,
// agent.initialize_timeout, ...): it tells the idle watchdog "still waiting, on purpose". A
// wait without its own bound must report real progress instead, or a hang would never time out.
func KeepAlive(ctx context.Context) (stop func()) {
	s, ok := from(ctx)
	if !ok || s.Beat == nil || s.Interval <= 0 {
		return func() {}
	}
	done := make(chan struct{})
	exited := make(chan struct{})
	var once sync.Once
	safego.Go("progress.keepalive", func() {
		defer close(exited)
		t := time.NewTicker(s.Interval)
		defer t.Stop()
		for {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-t.C:
				select {
				case <-done:
					return
				case <-ctx.Done():
					return
				default:
					s.Beat()
				}
			}
		}
	})
	return func() {
		once.Do(func() {
			close(done)
			<-exited
		})
	}
}
