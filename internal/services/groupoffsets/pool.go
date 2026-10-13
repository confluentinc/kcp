// Package groupoffsets reads consumer groups' committed offsets from a source cluster, detects commits that
// land after a fence, validates the offsets against a destination cluster's high-water marks, and writes them
// to the destination. It has no state machine: the d2s FSM composes it.
//
// The name is deliberately not "offset sync". internal/services/migration/offset_sync.go is a different thing:
// it toggles the cluster link's consumer.offset.sync.enable.
package groupoffsets

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// worker is one pool member: the function that handles an item and the function that releases whatever the
// worker holds (typically a broker client).
type worker struct {
	do      func(item string) error
	release func()
}

// forEach runs each item on up to n workers. Every worker is built by setup, so it can own its own client:
// sarama holds a lock per broker connection, so goroutines sharing one client gain nothing against the same
// broker. At most len(items) workers are built. After the first failure, or when ctx ends, no further items
// are handed out. The returned error is the failure of the lowest-named failed item, so it does not depend on
// which worker finished first, and it keeps the cause for errors.Is.
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

	var (
		mu       sync.Mutex
		failures = map[string]error{}
		failed   atomic.Bool
		wg       sync.WaitGroup
	)
	fail := func(item string, err error) {
		failed.Store(true)
		mu.Lock()
		failures[item] = err
		mu.Unlock()
	}

	jobs := make(chan string)
	for i := 0; i < n; i++ {
		w, err := setup()
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
				if err := w.do(item); err != nil {
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
