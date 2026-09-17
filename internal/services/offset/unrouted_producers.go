package offset

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"time"
)

// ErrUnroutedProducers is returned by DetectUnroutedProducers when a source
// offset increases across the monitoring window: evidence of a producer
// writing directly to the source cluster, bypassing a fenced gateway. Both
// the AAO (migration) and TBM orchestrators re-export this under their own
// name (e.g. `var ErrUnroutedProducers = offset.ErrUnroutedProducers`) so
// their existing errors.Is call sites keep working unchanged.
var ErrUnroutedProducers = errors.New("unrouted producers detected")

// Reporter is the minimal user-facing output surface DetectUnroutedProducers
// needs. Both migration's and TBM's own unexported reporter types satisfy
// this structurally.
type Reporter interface {
	Detail(format string, args ...any)
}

// FetchSourceAndDestinationOffsets sweeps both clusters' offsets for the
// given topics concurrently — the clusters are independent, so a poll tick
// pays the slower of the two sweeps rather than their sum. Shared by the AAO
// (migration) and TBM orchestrators, which otherwise hand-maintained two
// identical copies.
func FetchSourceAndDestinationOffsets(ctx context.Context, source, destination Provider, topics []string) (map[string]map[int32]int64, map[string]map[int32]int64, error) {
	var (
		wg                     sync.WaitGroup
		sourceOffsets, destOff map[string]map[int32]int64
		sourceErr, destErr     error
	)
	wg.Add(2)
	go func() {
		defer wg.Done()
		sourceOffsets, sourceErr = source.GetMany(ctx, topics)
	}()
	go func() {
		defer wg.Done()
		destOff, destErr = destination.GetMany(ctx, topics)
	}()
	wg.Wait()

	var errs []error
	if sourceErr != nil {
		errs = append(errs, fmt.Errorf("failed to get source offsets: %w", sourceErr))
	}
	if destErr != nil {
		errs = append(errs, fmt.Errorf("failed to get destination offsets: %w", destErr))
	}
	if len(errs) > 0 {
		return nil, nil, errors.Join(errs...)
	}
	return sourceOffsets, destOff, nil
}

// DetectUnroutedProducers takes two source offset snapshots separated by the
// given duration. If any partition's offset increases between snapshots, it
// means a producer is writing directly to the source cluster (bypassing the
// fenced gateway) and the migration should not proceed. Shared by the AAO
// (migration) and TBM orchestrators, which otherwise hand-maintained two
// identical copies — both wrap the same ErrUnroutedProducers, so either
// package's orchestrator can errors.Is against it.
func DetectUnroutedProducers(ctx context.Context, source Provider, reporter Reporter, topics []string, duration time.Duration) error {
	// Snapshot 1 — one batched sweep across all topics.
	slog.Debug("taking first source offset snapshot", "topicCount", len(topics))
	snapshot1, err := source.GetMany(ctx, topics)
	if err != nil {
		return fmt.Errorf("failed to get source offsets: %w", err)
	}

	// Wait, then snapshot 2
	reporter.Detail("Monitoring source offsets for %s...", duration)
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(duration):
	}

	slog.Debug("taking second source offset snapshot")
	snapshot2, err := source.GetMany(ctx, topics)
	if err != nil {
		return fmt.Errorf("failed to get source offsets: %w", err)
	}

	var violations []string
	for _, topic := range topics {
		for p, o2 := range snapshot2[topic] {
			// A partition absent from the first snapshot (e.g. created during
			// the window) starts at offset 0, so any data on it was written
			// after fencing — the zero-value baseline flags it.
			o1 := snapshot1[topic][p]
			if o2 > o1 {
				delta := o2 - o1
				rate := float64(delta) / duration.Seconds()
				violations = append(violations, fmt.Sprintf(
					"topic %s partition %d: offset %d → %d (+%d, ~%.0f msg/s)",
					topic, p, o1, o2, delta, rate))
			}
		}
	}

	if len(violations) > 0 {
		sort.Strings(violations)
		return fmt.Errorf("%w:\n  %s\n\nThese producers are bypassing the gateway and writing directly to the source cluster.\nReconfigure them to produce through the migration gateway, then re-run 'kcp migration execute' to resume",
			ErrUnroutedProducers, strings.Join(violations, "\n  "))
	}

	return nil
}
