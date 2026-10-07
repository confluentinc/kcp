package d2s

import (
	"context"
	"errors"

	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migration"
)

// ErrVerifyRefused is returned by VerifyFence when a conversion check refuses on the facts gathered after the
// fence. The refusal itself is printed with the reconcile report renderer, as --dry-run prints it.
var ErrVerifyRefused = errors.New("the conversion's checks refused after the fence")

// VerifyFence runs the verify_fence transition.
//
// Plan 3 Task 4 stub: Task 5 replaces this whole file with the real check. Until then it hands sync_offsets an
// empty snapshot, so the state machine can be walked in this package's tests; nothing else can reach it, since
// execute refuses a conversion until Task 7.
func (a *D2SActions) VerifyFence(ctx context.Context, config *migration.MigrationConfig, p Policy) error {
	a.handoff = groupoffsets.Snapshot{}
	return ctx.Err()
}
