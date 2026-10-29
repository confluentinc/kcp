// Package groupoffsets reads consumer groups' committed offsets from a source cluster, detects commits that
// land after a fence, validates the offsets against a destination cluster's high-water marks, and writes them
// to the destination. It has no state machine: the d2s FSM composes it.
//
// The name is deliberately not "offset sync". internal/services/migration/offset_sync.go is a different thing:
// it toggles the cluster link's consumer.offset.sync.enable.
package groupoffsets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/confluentinc/kcp/internal/retry"
)

// worker is one pool member: the function that handles an item and the function that releases whatever the
// worker holds (typically a broker client). do is given the run's context, which ends when any item fails.
type worker struct {
	do      func(ctx context.Context, item string) error
	release func()
}

// forEach runs each item on up to n workers. Every worker is built by setup, so it can own its own client:
// sarama holds a lock per broker connection, so goroutines sharing one client gain nothing against the same
// broker. At most len(items) workers are built. A worker whose client fails to connect on a connection error is
// built again (internal/retry); any other setup failure stops the run. After the first failure, or when ctx
// ends, no further items are handed out, and the context given to do and to setup's retries ends, so the other
// workers stop retrying instead of using the rest of their retry budget. The returned error is the failure of the
// lowest-named failed item, leaving out the items stopped by it, so it does not depend on which worker finished
// first, and it keeps the cause for errors.Is.
func forEach(ctx context.Context, items []string, n int, setup func() (worker, error)) error {
	if len(items) == 0 {
		return nil
	}
	if n < 1 {
		n = 1
	}
	if n > len(items) {
		n = len(items)
	}

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	var (
		mu       sync.Mutex
		failures = map[string]error{}
		failed   atomic.Bool
		wg       sync.WaitGroup
	)
	fail := func(item string, err error) {
		if failed.Load() && ctx.Err() == nil && errors.Is(err, context.Canceled) {
			return // stopped by an earlier failure, which is the one to report
		}
		failed.Store(true)
		stop()
		mu.Lock()
		failures[item] = err
		mu.Unlock()
	}

	jobs := make(chan string)
	for i := 0; i < n; i++ {
		var w worker
		err := retry.Do(runCtx, "kafka.open_client", func(context.Context) error {
			var err error
			w, err = setup()
			return err
		})
		if err != nil {
			fail(fmt.Sprintf("<worker %d>", i), fmt.Errorf("starting worker: %w", err))
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			if w.release != nil {
				defer w.release()
			}
			for item := range jobs {
				if failed.Load() || ctx.Err() != nil {
					continue
				}
				if err := w.do(runCtx, item); err != nil {
					fail(item, err)
				}
			}
		}()
	}

	for _, item := range items {
		if failed.Load() || ctx.Err() != nil {
			break
		}
		jobs <- item
	}
	close(jobs)
	wg.Wait()

	if len(failures) > 0 {
		keys := make([]string, 0, len(failures))
		for k := range failures {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		if len(keys) == 1 {
			return fmt.Errorf("%s: %w", keys[0], failures[keys[0]])
		}
		return fmt.Errorf("%s: %w (and %d more failed)", keys[0], failures[keys[0]], len(keys)-1)
	}
	return ctx.Err()
}
