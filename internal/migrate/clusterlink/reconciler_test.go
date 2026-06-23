package clusterlink

import (
	"context"
	"os"
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
		Auth: LinkAuth{
			SecurityProtocol: "SASL_SSL",
			SaslMechanism:    "SCRAM-SHA-256",
			SaslJaasConfig:   "jaas",
		},
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
	require.Equal(t, "SASL_SSL", tgt.createdReq.SecurityProtocol)
	require.Equal(t, "SCRAM-SHA-256", tgt.createdReq.SaslMechanism)
	require.Equal(t, "jaas", tgt.createdReq.SaslJaasConfig)
}

func TestPlan_Plaintext_NoTLSMaterial(t *testing.T) {
	tgt := &fakeTarget{clusterID: "dest-1", existing: nil}
	r := New(Config{
		LinkName:               "l",
		SourceBootstrapServers: []string{"s:9092"},
		Auth:                   LinkAuth{SecurityProtocol: "PLAINTEXT"},
	}, fakeSource{id: "src-1"}, tgt)
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	_, err = r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Equal(t, "PLAINTEXT", tgt.createdReq.SecurityProtocol)
	require.Nil(t, tgt.createdReq.SourceTLS)
}

func TestPlan_TLSMaterialForwarded(t *testing.T) {
	dir := t.TempDir()
	ca := dir + "/ca.crt"
	require.NoError(t, os.WriteFile(ca, []byte("CA-PEM"), 0600))
	tgt := &fakeTarget{clusterID: "dest-1", existing: nil}
	r := New(Config{
		LinkName:               "l",
		SourceBootstrapServers: []string{"s:9092"},
		Auth: LinkAuth{
			SecurityProtocol: "SASL_SSL",
			SaslMechanism:    "SCRAM-SHA-256",
			SaslJaasConfig:   "j",
			CACertPath:       ca,
		},
	}, fakeSource{id: "src-1"}, tgt)
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	_, err = r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.NotNil(t, tgt.createdReq.SourceTLS)
	require.Equal(t, "CA-PEM", tgt.createdReq.SourceTLS.CACertPEM)
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

// --- source-initiated mode ---

// recordingTarget records every create (and its order across both clients via
// the shared *createLog), so source-mode tests can assert ordering + routing.
type recordingTarget struct {
	clusterID string
	existing  *svclink.ClusterLink
	side      string // "dest" or "src" — tag for the shared log
	log       *createLog
}

type createEntry struct {
	side string
	name string
	req  svclink.CreateClusterLinkRequest
}

type createLog struct{ entries []createEntry }

func (t *recordingTarget) ClusterID(context.Context) (string, error) { return t.clusterID, nil }
func (t *recordingTarget) GetClusterLink(context.Context, string) (*svclink.ClusterLink, error) {
	return t.existing, nil
}
func (t *recordingTarget) CreateClusterLink(_ context.Context, name string, req svclink.CreateClusterLinkRequest) error {
	t.log.entries = append(t.log.entries, createEntry{side: t.side, name: name, req: req})
	return nil
}

func newSourceInitiated(src fakeSource, dest, srcLink *recordingTarget) *Reconciler {
	return NewSourceInitiated(Config{
		LinkName:             "src-to-dest",
		Mode:                 ModeSource,
		DestBootstrapServers: []string{"dest:29092"},
		DestAuth: LinkAuth{
			SecurityProtocol: "SASL_SSL",
			SaslMechanism:    "SCRAM-SHA-512",
			SaslJaasConfig:   "dest-jaas",
		},
		Configs: map[string]string{"consumer.offset.sync.enable": "true"},
	}, src, dest, srcLink)
}

func TestSourceInitiated_Plan_TwoCreatesInOrder(t *testing.T) {
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.False(t, plan.Empty())

	changes := plan.Changes()
	require.Len(t, changes, 2)
	require.Equal(t, reconcile.ActionCreate, changes[0].Action)
	require.Equal(t, reconcile.ActionCreate, changes[1].Action)
	require.Contains(t, changes[0].Summary, "destination side")
	require.Contains(t, changes[1].Summary, "source side")
}

func TestSourceInitiated_Apply_RoutesToCorrectClientInOrder(t *testing.T) {
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, out.Created, 2)

	require.Len(t, log.entries, 2)
	// Destination first.
	d := log.entries[0]
	require.Equal(t, "dest", d.side)
	require.Equal(t, "src-to-dest", d.name)
	require.Equal(t, "DESTINATION", d.req.LinkMode)
	require.Equal(t, "INBOUND", d.req.ConnectionMode)
	require.Equal(t, "src-1", d.req.SourceClusterID)
	require.Empty(t, d.req.SourceBootstrapServers, "destination side carries no bootstrap")
	require.Empty(t, d.req.SecurityProtocol, "destination side carries no connection auth")

	// Source second, carrying destination address + source→destination auth.
	s := log.entries[1]
	require.Equal(t, "src", s.side)
	require.Equal(t, "src-to-dest", s.name)
	require.Equal(t, "SOURCE", s.req.LinkMode)
	require.Equal(t, "OUTBOUND", s.req.ConnectionMode)
	require.Empty(t, s.req.SourceClusterID, "source side must NOT carry source_cluster_id")
	require.Equal(t, []string{"dest:29092"}, s.req.SourceBootstrapServers)
	require.Equal(t, "SASL_SSL", s.req.SecurityProtocol)
	require.Equal(t, "SCRAM-SHA-512", s.req.SaslMechanism)
	require.Equal(t, "dest-jaas", s.req.SaslJaasConfig)
	require.Equal(t, "true", s.req.Configs["consumer.offset.sync.enable"])
}

func TestSourceInitiated_BothPresent_AllPresent(t *testing.T) {
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log,
		existing: &svclink.ClusterLink{LinkName: "src-to-dest"}}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log,
		existing: &svclink.ClusterLink{LinkName: "src-to-dest"}}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, plan.Empty())

	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, out.Present, 2)
	require.Empty(t, out.Created)
	require.Empty(t, log.entries, "nothing created when both sides present")
}

func TestSourceInitiated_OnlyDestPresent_CreatesSourceOnly(t *testing.T) {
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log,
		existing: &svclink.ClusterLink{LinkName: "src-to-dest"}}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log} // absent
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.False(t, plan.Empty())

	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, out.Created, 1)
	require.Len(t, out.Present, 1)
	require.Len(t, log.entries, 1)
	require.Equal(t, "src", log.entries[0].side, "only the missing (source) side is created")
	require.Equal(t, "SOURCE", log.entries[0].req.LinkMode)
}

func TestSourceInitiated_OnlySourcePresent_CreatesDestOnly(t *testing.T) {
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log} // absent
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log,
		existing: &svclink.ClusterLink{LinkName: "src-to-dest"}}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, out.Created, 1)
	require.Len(t, log.entries, 1)
	require.Equal(t, "dest", log.entries[0].side, "only the missing (destination) side is created")
	require.Equal(t, "DESTINATION", log.entries[0].req.LinkMode)
	require.Equal(t, "INBOUND", log.entries[0].req.ConnectionMode)
}

func TestSourceInitiated_CheckPreconditions_RequiresSourceTarget(t *testing.T) {
	r := NewSourceInitiated(Config{LinkName: "l", Mode: ModeSource}, fakeSource{id: "s"},
		&recordingTarget{clusterID: "d", side: "dest", log: &createLog{}}, nil)
	err := r.CheckPreconditions(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "source REST")
}
