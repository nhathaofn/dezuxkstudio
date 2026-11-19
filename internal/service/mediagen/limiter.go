package mediagen

import (
	"context"
	"sync"
	"time"

	"app/internal/apperr"
	"app/internal/config"
	domainprovider "app/internal/domain/provider"
)

// rpmWindow is the 60-second sliding window defined by requests-per-minute provider limits.
const rpmWindow = time.Minute

type rateLimitCtxKey struct{}

type rateLimitScope struct {
	owner string
	cap   domainprovider.Capability
}

func withRateLimitScope(ctx context.Context, owner string, cap domainprovider.Capability) context.Context {
	if owner == "" || cap == "" {
		return ctx
	}
	return context.WithValue(ctx, rateLimitCtxKey{}, rateLimitScope{owner: owner, cap: cap})
}

func rateLimitScopeFromContext(ctx context.Context) (rateLimitScope, bool) {
	if ctx == nil {
		return rateLimitScope{}, false
	}
	v, ok := ctx.Value(rateLimitCtxKey{}).(rateLimitScope)
	return v, ok && v.owner != "" && v.cap != ""
}

type limiterKey struct {
	owner string
	cap   domainprovider.Capability
}

type bucketState struct {
	inFlight     int
	lastStart    time.Time
	backoffUntil time.Time
	timestamps   []time.Time
	notify       chan struct{}
}

// rateLimiter enforces per-(provider, capability) MaxWorkers, RPM, RequestGapMs, and 429 Retry-After backoff
// configured in Settings -> Providers.
type rateLimiter struct {
	mu               sync.Mutex
	buckets          map[limiterKey]*bucketState
	pollInterval     time.Duration
	defaultMaxWorker int
}

func newRateLimiter(pollInterval time.Duration, defaultMaxWorkers int) *rateLimiter {
	def := config.Default(config.ModeDesktop)
	if pollInterval <= 0 {
		pollInterval = def.MediaGen.LimiterPollInterval
	}
	if defaultMaxWorkers <= 0 {
		defaultMaxWorkers = def.Providers.DefaultMaxWorkers
	}
	return &rateLimiter{
		buckets:          make(map[limiterKey]*bucketState),
		pollInterval:     pollInterval,
		defaultMaxWorker: defaultMaxWorkers,
	}
}

func (l *rateLimiter) getOrCreateBucketLocked(key limiterKey) *bucketState {
	b := l.buckets[key]
	if b == nil {
		b = &bucketState{notify: make(chan struct{})}
		l.buckets[key] = b
	} else if b.notify == nil {
		b.notify = make(chan struct{})
	}
	return b
}

func wakeBucketLocked(b *bucketState) {
	if b == nil || b.notify == nil {
		return
	}
	ch := b.notify
	b.notify = make(chan struct{})
	close(ch)
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
		b := l.getOrCreateBucketLocked(key)
		now := time.Now()
		cutoff := now.Add(-rpmWindow)
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
			rpmWait := time.Until(b.timestamps[0].Add(rpmWindow))
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
		if !b.backoffUntil.IsZero() {
			boWait := b.backoffUntil.Sub(now)
			if boWait > wait {
				wait = boWait
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
					if cur := l.buckets[key]; cur != nil {
						if cur.inFlight > 0 {
							cur.inFlight--
						}
						wakeBucketLocked(cur)
					}
					l.mu.Unlock()
				})
			}, nil
		}

		ch := b.notify
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
	if backoff <= 0 || owner == "" || cap == "" {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	key := limiterKey{owner: owner, cap: cap}
	b := l.getOrCreateBucketLocked(key)
	targetTime := time.Now().Add(backoff)
	if targetTime.After(b.backoffUntil) {
		b.backoffUntil = targetTime
	}
	if targetTime.After(b.lastStart) {
		b.lastStart = targetTime
	}
}

// acquireLimit coordinates rate-limiter acquisition with domain limits and returns a scoped context + cleanup callback.
func (s *Service) acquireLimit(ctx context.Context, target targetSpec, cap domainprovider.Capability, limits domainprovider.Limits) (context.Context, func(), error) {
	release, err := s.limiter.acquire(ctx, target.ownerID, cap, limits)
	if err != nil {
		return ctx, nil, apperr.From(err)
	}
	return withRateLimitScope(ctx, target.ownerID, cap), release, nil
}
