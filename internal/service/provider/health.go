package provider

import (
	"context"
	"sync"

	"app/internal/safego"
)

// CheckAll checks every saved credential and custom provider, at most concurrency at a time: each is
// tested and, when it works, its models are refreshed from the provider API (the same path as a fresh
// save). Run in the background at start-up so dead keys are flagged and new models appear in Settings.
// It returns when all tests finished or ctx is cancelled.
func (s *Service) CheckAll(ctx context.Context, concurrency int) {
	if concurrency < 1 {
		concurrency = 1
	}
	creds, err := s.repo.ListCredentials(ctx)
	if err != nil {
		return
	}
	custom, err := s.repo.ListCustom(ctx)
	if err != nil {
		return
	}
	var jobs []func()
	for _, c := range creds {
		id := c.ID
		jobs = append(jobs, func() { s.checkAndRefresh(ctx, id) })
	}
	for _, p := range custom {
		id := p.ID
		jobs = append(jobs, func() { s.checkAndRefresh(ctx, id) })
	}
	sem := make(chan struct{}, concurrency)
	var wg sync.WaitGroup
	for _, job := range jobs {
		select {
		case <-ctx.Done():
			wg.Wait()
			return
		case sem <- struct{}{}:
		}
		wg.Add(1)
		safego.Go("provider-health-check", func() {
			defer wg.Done()
			defer func() { <-sem }()
			job()
		})
	}
	wg.Wait()
}
