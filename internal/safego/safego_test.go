package safego

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRecoverAndGo(t *testing.T) {
	var called atomic.Bool
	var gotName string
	var gotVal any
	var gotStack []byte
	var mu sync.Mutex
	handled := make(chan struct{}, 1)

	SetPanicHandler(func(name string, value any, stack []byte) {
		mu.Lock()
		gotName = name
		gotVal = value
		gotStack = stack
		mu.Unlock()
		called.Store(true)
		select {
		case handled <- struct{}{}:
		default:
		}
	})
	defer SetPanicHandler(nil)

	Go("test.panic", func() {
		panic("boom")
	})
	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for PanicHandler on panic inside safego.Go")
	}

	if !called.Load() {
		t.Fatal("expected PanicHandler to be invoked")
	}
	mu.Lock()
	defer mu.Unlock()
	if gotName != "test.panic" {
		t.Fatalf("expected name %q, got %q", "test.panic", gotName)
	}
	if gotVal != "boom" {
		t.Fatalf("expected panic value %q, got %v", "boom", gotVal)
	}
	if len(gotStack) == 0 {
		t.Fatal("expected non-empty stack trace")
	}

	// Non-panicking goroutine completes cleanly and does not invoke handler.
	called.Store(false)
	var wg sync.WaitGroup
	wg.Add(1)
	Go("test.clean", func() {
		defer wg.Done()
	})
	wg.Wait()
	if called.Load() {
		t.Fatal("unexpected PanicHandler call on clean goroutine")
	}
}
