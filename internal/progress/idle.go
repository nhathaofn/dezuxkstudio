package progress

import (
	"context"
	"sync"
	"time"
)

type idleContext struct {
	parent     context.Context
	timeout    time.Duration
	mu         sync.Mutex
	timer      *time.Timer
	deadline   time.Time
	done       chan struct{}
	err        error
	stopParent func() bool
}

// NewIdleContext returns a context that expires after idle without a touch, not after a fixed
// total duration. touch re-arms the deadline; cancel releases it. This is the model the binaries
// download introduced: the realtime hub uses it per action, the agent service per chat turn.
func NewIdleContext(parent context.Context, idle time.Duration) (ctx context.Context, touch func(), cancel func()) {
	return newIdleContext(parent, idle)
}

func newIdleContext(parent context.Context, timeout time.Duration) (*idleContext, func(), func()) {
	c := &idleContext{
		parent:   parent,
		timeout:  timeout,
		done:     make(chan struct{}),
		deadline: time.Now().Add(timeout),
	}
	if parentErr := parent.Err(); parentErr != nil {
		c.err = parentErr
		close(c.done)
		return c, func() {}, func() {}
	}

	c.timer = time.AfterFunc(timeout, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		// Re-check deadline under lock so a timer invocation that raced with reset() does not expire early.
		if c.err == nil && time.Until(c.deadline) <= 0 {
			c.err = context.DeadlineExceeded
			close(c.done)
		}
	})

	c.stopParent = context.AfterFunc(parent, func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.err == nil {
			if c.timer != nil {
				c.timer.Stop()
			}
			c.err = parent.Err()
			close(c.done)
		}
	})

	reset := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.err == nil {
			c.deadline = time.Now().Add(c.timeout)
			if c.timer != nil {
				c.timer.Reset(c.timeout)
			}
		}
	}

	cancel := func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		if c.stopParent != nil {
			c.stopParent()
		}
		if c.timer != nil {
			c.timer.Stop()
		}
		if c.err == nil {
			c.err = context.Canceled
			close(c.done)
		}
	}

	return c, reset, cancel
}

func (c *idleContext) Deadline() (time.Time, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if pd, ok := c.parent.Deadline(); ok && pd.Before(c.deadline) {
		return pd, true
	}
	return c.deadline, true
}

func (c *idleContext) Done() <-chan struct{} {
	return c.done
}

func (c *idleContext) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

func (c *idleContext) Value(key any) any {
	return c.parent.Value(key)
}
