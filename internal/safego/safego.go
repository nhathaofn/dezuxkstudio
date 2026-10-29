// Package safego runs goroutines that can never crash the process silently.
//
// Every goroutine in the application must be started with Go (or defer Recover)
// so that a panic is logged with its stack and reported to the crash handler.
package safego

import (
	"log/slog"
	"runtime/debug"
	"sync/atomic"
)

// PanicHandler receives recovered panics, e.g. to write a crash dump.
type PanicHandler func(name string, value any, stack []byte)

var handler atomic.Pointer[PanicHandler]

// SetPanicHandler installs the process-wide handler for recovered panics.
func SetPanicHandler(h PanicHandler) {
	if h == nil {
		handler.Store(nil)
		return
	}
	handler.Store(&h)
}

// Go runs fn in a new goroutine guarded by Recover.
func Go(name string, fn func()) {
	go func() {
		defer Recover(name)
		fn()
	}()
}

// Recover must be deferred directly; it logs and reports a panic instead of crashing.
func Recover(name string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	slog.Error("recovered panic", "goroutine", name, "panic", r, "stack", string(stack))
	if h := handler.Load(); h != nil {
		(*h)(name, r, stack)
	}
}
