package groupoffsets

import (
	"context"

	"go.opentelemetry.io/otel/attribute"

	"github.com/confluentinc/kcp/internal/tracing"
)

// ListGroups runs a consumer-group listing inside a groupoffsets.list_groups span, recording which cluster
// was listed (kcp.cluster: source or destination) and, on success, how many groups it returned
// (kcp.groups). A listing's cost grows with the cluster's group count, not with the groups the conversion
// copies, so it is kept apart from the snapshots. list's error is returned unchanged.
func ListGroups[T any](ctx context.Context, cluster string, list func(context.Context) ([]T, error)) (_ []T, err error) {
	ctx, span := tracing.Start(ctx, tracerName, "groupoffsets.list_groups", attribute.String("kcp.cluster", cluster))
	defer func() { tracing.End(span, err) }()
	groups, err := list(ctx)
	if err != nil {
		return nil, err
	}
	span.SetAttributes(attribute.Int("kcp.groups", len(groups)))
	return groups, nil
}
