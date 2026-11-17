package mediagen

import (
	"context"
	"sync"
	"time"

	"app/internal/apperr"
	domainprovider "app/internal/domain/provider"
)

type limiterKey struct {
	owner string
	cap   domainprovider.Capability
}

type bucketState struct {
	inFlight   int
	lastStart  time.Time
	timestamps []time.Time
}

// rateLimiter enforces per-(provider, capability) MaxWorkers, RPM, and RequestGapMs
// configured in Settings -> Providers.
type rateLimiter struct {
	mu               sync.Mutex
	notify           chan struct{}
	buckets          map[limiterKey]*bucketState
	pollInterval     time.Duration
	defaultMaxWorker int
}

func newRateLimiter(pollInterval time.Duration, defaultMaxWorkers int) *rateLimiter {
	if pollInterval <= 0 {
		pollInterval = 250 * time.Millisecond
	}
	if defaultMaxWorkers <= 0 {
		defaultMaxWorkers = 2
	}
	return &rateLimiter{
		notify:           make(chan struct{}),
		buckets:          make(map[limiterKey]*bucketState),
		pollInterval:     pollInterval,
		defaultMaxWorker: defaultMaxWorkers,
	}
}

func (l *rateLimiter) acquire(ctx context.Context, owner string, cap domainprovider.Capability, limits domainprovider.Limits) (func(), error) {
	maxWorkers := limits.MaxWorkers[cap]
	if maxWorkers <= 0 {
		maxWorkers = l.defaultMaxWorker
	}
	rpm := limits.RPM[cap]
	gap := time.Duration(limits.RequestGapMs) * time.Millisecond
	key := limiterKey{owner: owner, cap: cap}

	for {
		l.mu.Lock()
		b := l.buckets[key]
		if b == nil {
			b = &bucketState{}
			l.buckets[key] = b
		}
		now := time.Now()
		cutoff := now.Add(-time.Minute)
		kept := b.timestamps[:0]
		for _, ts := range b.timestamps {
			if ts.After(cutoff) {
				kept = append(kept, ts)
			}
		}
		b.timestamps = kept

		var wait time.Duration
		if b.inFlight >= maxWorkers {
			wait = l.pollInterval
		}
		if rpm > 0 && len(b.timestamps) >= rpm {
			rpmWait := time.Until(b.timestamps[0].Add(time.Minute))
			if rpmWait > wait {
				wait = rpmWait
			}
		}
		if gap > 0 && !b.lastStart.IsZero() {
			gapWait := b.lastStart.Add(gap).Sub(now)
			if gapWait > wait {
				wait = gapWait
			}
		}

		if wait <= 0 {
			b.inFlight++
			b.lastStart = now
			if rpm > 0 {
				b.timestamps = append(b.timestamps, now)
			}
			l.mu.Unlock()

			var once sync.Once
			return func() {
				once.Do(func() {
					l.mu.Lock()
					if cur := l.buckets[key]; cur != nil && cur.inFlight > 0 {
						cur.inFlight--
					}
					ch := l.notify
					l.notify = make(chan struct{})
					close(ch)
					l.mu.Unlock()
				})
			}, nil
		}

		ch := l.notify
		l.mu.Unlock()

		timer := time.NewTimer(wait)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		case <-ch:
			timer.Stop()
		}
	}
}

// recordBackoff pauses subsequent requests for the given (owner, capability) key until now + backoff.
func (l *rateLimiter) recordBackoff(owner string, cap domainprovider.Capability, backoff time.Duration) {
	if backoff <= 0 {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := limiterKey{owner: owner, cap: cap}
	b := l.buckets[key]
	if b == nil {
		b = &bucketState{}
		l.buckets[key] = b
	}
	targetTime := time.Now().Add(backoff)
	if targetTime.After(b.lastStart) {
		b.lastStart = targetTime
	}
}

// acquireLimit coordinates rate-limiter acquisition with domain limits and returns a cleanup callback.
func (s *Service) acquireLimit(ctx context.Context, target targetSpec, cap domainprovider.Capability, limits domainprovider.Limits) (func(), error) {
	release, err := s.limiter.acquire(ctx, target.ownerID, cap, limits)
	if err != nil {
		return nil, apperr.From(err)
	}
	return release, nil
}
