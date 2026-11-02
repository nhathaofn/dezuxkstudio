package watermark

import (
	"context"
	"fmt"
	"io"
	"os"
	"sync"

	"app/internal/fsutil"
	"app/internal/safego"
)

// ProcessBatch processes a slice of BatchItems concurrently using a thread-safe worker pool.
// Output ordering is guaranteed to match the input slice index.
func (e *Engine) ProcessBatch(ctx context.Context, items []BatchItem, workers int) []BatchResult {
	if len(items) == 0 {
		return nil
	}
	if workers <= 0 {
		workers = e.cfg.Workers
	}
	if workers > len(items) {
		workers = len(items)
	}

	results := make([]BatchResult, len(items))
	jobsCh := make(chan int, len(items))
	for i := range items {
		jobsCh <- i
	}
	close(jobsCh)

	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		safego.Go("watermark.batch.worker", func() {
			defer wg.Done()
			for idx := range jobsCh {
				if ctx.Err() != nil {
					results[idx] = BatchResult{
						ID:  items[idx].ID,
						Err: ctx.Err(),
					}
					continue
				}
				func() {
					defer func() {
						if r := recover(); r != nil {
							results[idx] = BatchResult{
								ID:  items[idx].ID,
								Err: fmt.Errorf("panic processing item %s: %v", items[idx].ID, r),
							}
						}
					}()
					out, detected, format, info, score, gain, err := e.removeInternal(items[idx].Data)
					results[idx] = BatchResult{
						ID:           items[idx].ID,
						Output:       out,
						OutputFormat: format,
						Detected:     detected,
						Info:         info,
						Score:        score,
						Gain:         gain,
						Err:          err,
					}
				}()
			}
		})
	}
	wg.Wait()
	return results
}

// ProcessStream returns a stream channel receiving processed BatchResults from an input channel.
func (e *Engine) ProcessStream(ctx context.Context, in <-chan BatchItem, workers int) <-chan BatchResult {
	if workers <= 0 {
		workers = e.cfg.Workers
	}
	outCh := make(chan BatchResult, workers*2)
	var wg sync.WaitGroup

	for i := 0; i < workers; i++ {
		wg.Add(1)
		safego.Go("watermark.stream.worker", func() {
			defer wg.Done()
			for {
				select {
				case <-ctx.Done():
					return
				case item, ok := <-in:
					if !ok {
						return
					}
					var res BatchResult
					func() {
						defer func() {
							if r := recover(); r != nil {
								res = BatchResult{
									ID:  item.ID,
									Err: fmt.Errorf("panic processing item %s: %v", item.ID, r),
								}
							}
						}()
						out, detected, format, info, score, gain, err := e.removeInternal(item.Data)
						res = BatchResult{
							ID:           item.ID,
							Output:       out,
							OutputFormat: format,
							Detected:     detected,
							Info:         info,
							Score:        score,
							Gain:         gain,
							Err:          err,
						}
					}()
					select {
					case <-ctx.Done():
						return
					case outCh <- res:
					}
				}
			}
		})
	}

	safego.Go("watermark.stream.wait", func() {
		wg.Wait()
		close(outCh)
	})

	return outCh
}

// RemoveBatch processes multiple images concurrently using default settings.
func RemoveBatch(ctx context.Context, items []BatchItem, workers int) []BatchResult {
	return Default().ProcessBatch(ctx, items, workers)
}

// WriteAtomic writes image data to path atomically using fsutil.WriteAtomic.
// In-place overwrites preserve destination file integrity if write fails.
func WriteAtomic(path string, data []byte, perm os.FileMode) error {
	return fsutil.WriteAtomic(path, perm, func(w io.Writer) error {
		_, err := w.Write(data)
		return err
	})
}
