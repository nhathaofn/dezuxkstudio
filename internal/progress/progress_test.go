package progress

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestNoSinkIsANoOp(t *testing.T) {
	ctx := context.Background()
	Report(ctx, Update{Percent: 10})
	Beat(ctx)
	Func(ctx)(5, "x")
	KeepAlive(ctx)() // must not block or panic
}

func TestReportAndFuncReachTheSink(t *testing.T) {
	var got []Update
	ctx := With(context.Background(), Sink{Report: func(u Update) { got = append(got, u) }})
	Report(ctx, Update{Percent: 40, Key: "k", Params: map[string]string{"a": "b"}})
	Func(ctx)(60, "msg")
	if len(got) != 2 || got[0].Key != "k" || got[1].Percent != 60 || got[1].Message != "msg" {
		t.Fatalf("got %+v", got)
	}
}

func TestKeepAliveBeatsUntilStopped(t *testing.T) {
	var beats atomic.Int32
	ctx := With(context.Background(), Sink{Beat: func() { beats.Add(1) }, Interval: 10 * time.Millisecond})
	stop := KeepAlive(ctx)
	time.Sleep(75 * time.Millisecond)
	stop()
	stop() // idempotent
	n := beats.Load()
	if n < 3 {
		t.Fatalf("expected several beats, got %d", n)
	}
	time.Sleep(40 * time.Millisecond)
	if beats.Load() != n {
		t.Fatal("kept beating after stop")
	}
}

func TestKeepAliveStopsWithContext(t *testing.T) {
	var beats atomic.Int32
	parent, cancel := context.WithCancel(context.Background())
	ctx := With(parent, Sink{Beat: func() { beats.Add(1) }, Interval: 10 * time.Millisecond})
	defer KeepAlive(ctx)()
	cancel()
	time.Sleep(30 * time.Millisecond)
	n := beats.Load()
	time.Sleep(40 * time.Millisecond)
	if beats.Load() != n {
		t.Fatal("kept beating after the context ended")
	}
}

func TestIdleContextTouchAndExpire(t *testing.T) {
	ctx, touch, cancel := NewIdleContext(context.Background(), 40*time.Millisecond)
	defer cancel()

	// Repeated touches keep the reusable timer alive past the initial 40ms window.
	for range 4 {
		time.Sleep(20 * time.Millisecond)
		if ctx.Err() != nil {
			t.Fatalf("idleContext expired despite touch: %v", ctx.Err())
		}
		touch()
	}

	select {
	case <-ctx.Done():
	case <-time.After(500 * time.Millisecond):
		t.Fatal("idleContext did not expire after going idle")
	}
	if ctx.Err() != context.DeadlineExceeded {
		t.Fatalf("expected DeadlineExceeded, got %v", ctx.Err())
	}
}
