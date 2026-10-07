package d2s

import (
	"context"
	"errors"

	"github.com/confluentinc/kcp/internal/services/migration"
)

// errNoHandoff is returned by SyncOffsets when this run's verify_fence handed over no snapshot.
var errNoHandoff = errors.New("sync_offsets has no committed-offset snapshot from this run's verify_fence; it never reads the source itself, so re-run the conversion")

// SyncOffsets runs the sync_offsets transition.
//
// Plan 3 Task 4 stub: Task 6 replaces this whole file with the real sync. The guard below is already the real
// rule.
func (a *D2SActions) SyncOffsets(ctx context.Context, config *migration.MigrationConfig, p Policy) error {
	if a.handoff == nil {
		return errNoHandoff
	}
	return nil
}
