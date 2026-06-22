package clusterlink

import (
	"context"
	"testing"

	svclink "github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/stretchr/testify/require"
)

type fakeSource struct{ id string }

func (f fakeSource) ClusterID(context.Context) (string, error) { return f.id, nil }

type fakeTarget struct {
	clusterID   string
	existing    *svclink.ClusterLink
	createdReq  *svclink.CreateClusterLinkRequest
	createdName string
}

func (f *fakeTarget) ClusterID(context.Context) (string, error) { return f.clusterID, nil }
func (f *fakeTarget) GetClusterLink(context.Context, string) (*svclink.ClusterLink, error) {
	return f.existing, nil
}
func (f *fakeTarget) CreateClusterLink(_ context.Context, name string, req svclink.CreateClusterLinkRequest) error {
	f.createdName = name
	f.createdReq = &req
	return nil
}

func newReconciler(src fakeSource, tgt *fakeTarget) *Reconciler {
	return New(Config{
		LinkName:               "src-to-dest",
		SourceBootstrapServers: []string{"source:29092"},
		SecurityProtocol:       "PLAINTEXT",
	}, src, tgt)
}

func TestPlan_Missing_IsCreate(t *testing.T) {
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1", existing: nil})
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.False(t, plan.Empty())
	require.Equal(t, reconcile.ActionCreate, plan.Changes()[0].Action)
}

func TestPlan_PresentSameSource_IsNoOp(t *testing.T) {
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1",
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1"}})
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, plan.Empty())
	require.Equal(t, reconcile.ActionPresent, plan.Changes()[0].Action)
}

func TestPlan_PresentDifferentSource_IsDrift(t *testing.T) {
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1",
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "OTHER"}})
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcile.ActionDrift, plan.Changes()[0].Action)
	require.Contains(t, plan.Changes()[0].Detail, "OTHER")
	require.True(t, plan.Empty(), "drift plan must not be treated as requiring a create")
}

func TestPlan_PresentWhenTargetOmitsSourceID(t *testing.T) {
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1",
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: ""}})
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcile.ActionPresent, plan.Changes()[0].Action)
}

func TestApply_CreatesWithDerivedRequest(t *testing.T) {
	tgt := &fakeTarget{clusterID: "dest-1", existing: nil}
	r := newReconciler(fakeSource{id: "src-1"}, tgt)
	plan, _ := r.Plan(context.Background())
	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, out.Created, 1)
	require.Equal(t, "src-to-dest", tgt.createdName)
	require.Equal(t, "src-1", tgt.createdReq.SourceClusterID)
	require.Equal(t, "PLAINTEXT", tgt.createdReq.SecurityProtocol)
}

func TestApply_DriftDoesNotCreate(t *testing.T) {
	tgt := &fakeTarget{clusterID: "dest-1", existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "OTHER"}}
	r := newReconciler(fakeSource{id: "src-1"}, tgt)
	plan, _ := r.Plan(context.Background())
	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Empty(t, out.Created)
	require.Len(t, out.Drift, 1)
	require.Nil(t, tgt.createdReq, "drift must never create/override")
}
