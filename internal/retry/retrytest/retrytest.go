// Package retrytest shortens the retry schedule for one test, so a test that drives a retry does not wait the
// production backoff. Tests using it must not call t.Parallel().
package retrytest

import (
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/retry"
)

// Fast makes every retry.Do wait 1-2 ms between attempts and give up after 100 ms, until the test ends.
func Fast(t testing.TB) {
	t.Helper()
	t.Cleanup(retry.SetScheduleForTest(retry.Schedule{
		FirstWait: time.Millisecond,
		MaxWait:   2 * time.Millisecond,
		Budget:    100 * time.Millisecond,
	}))
}
