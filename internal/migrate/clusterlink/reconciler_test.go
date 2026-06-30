package clusterlink

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

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
	liveConfigs map[string]string
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
func (f *fakeTarget) GetClusterLinkConfigs(context.Context, string) (map[string]string, error) {
	return f.liveConfigs, nil
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

func TestPlan_ExistsButFailed_IsDrift(t *testing.T) {
	// A link that exists with the right source but is in a non-ACTIVE state
	// (e.g. source creds revoked after a once-healthy link) must be reported as
	// drift, not Present — otherwise a re-apply reports green while no data flows.
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1",
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1", LinkState: "FAILED", LinkError: "authentication failed"}})
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcile.ActionDrift, plan.Changes()[0].Action)
	require.Contains(t, plan.Changes()[0].Detail, "FAILED")
	require.Contains(t, plan.Changes()[0].Detail, "authentication failed")
	require.True(t, plan.Empty(), "an unhealthy link is drift (report-only), never a create")
}

func TestPlan_ExistsAndActive_IsPresent(t *testing.T) {
	// An explicit ACTIVE state with a matching source is healthy → Present.
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1",
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1", LinkState: "ACTIVE"}})
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcile.ActionPresent, plan.Changes()[0].Action)
}

// cp-server 8.x reports a healthy link with link_error="NO_ERROR" (the Kafka
// ClusterLinkError zero-value), not an empty string. That sentinel must be
// treated as healthy → Present, not drift. Regression: a literal LinkError==""
// check flagged every healthy cp-server link as drift.
func TestPlan_ExistsActiveNoErrorSentinel_IsPresent(t *testing.T) {
	r := newReconciler(fakeSource{id: "src-1"}, &fakeTarget{clusterID: "dest-1",
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1", LinkState: "ACTIVE", LinkError: "NO_ERROR"}})
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
	clusterID   string
	existing    *svclink.ClusterLink
	side        string // "dest" or "src" — tag for the shared log
	log         *createLog
	failN       int   // fail the first failN create calls...
	failErr     error // ...with this error (simulates a transient)
	liveConfigs map[string]string
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
	if t.failN > 0 {
		t.failN--
		return t.failErr
	}
	t.log.entries = append(t.log.entries, createEntry{side: t.side, name: name, req: req})
	return nil
}
func (t *recordingTarget) GetClusterLinkConfigs(context.Context, string) (map[string]string, error) {
	return t.liveConfigs, nil
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

func TestSourceInitiated_Plan_FailedDestLink_IsDrift(t *testing.T) {
	// The destination (INBOUND) link already exists but is non-ACTIVE; the source
	// (OUTBOUND) side is absent. Expect: dest side → drift (report-only), src side → create.
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log,
		existing: &svclink.ClusterLink{LinkName: "src-to-dest", LinkState: "FAILED", LinkError: "source unreachable"}}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	changes := plan.Changes()
	require.Len(t, changes, 2)
	require.Equal(t, reconcile.ActionDrift, changes[0].Action, "failed dest link must be drift, not present")
	require.Contains(t, changes[0].Detail, "FAILED")
	require.Equal(t, reconcile.ActionCreate, changes[1].Action, "absent source side still plans a create")
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

// shrinkPropagationRetry shrinks the retry timing for fast unit tests.
func shrinkPropagationRetry(t *testing.T) {
	t.Helper()
	ot, ob := linkPropagationRetryTimeout, linkPropagationRetryBackoff
	linkPropagationRetryTimeout, linkPropagationRetryBackoff = 2*time.Second, time.Millisecond
	t.Cleanup(func() { linkPropagationRetryTimeout, linkPropagationRetryBackoff = ot, ob })
}

// The source-side OUTBOUND create races the INBOUND link's propagation to the
// destination; a single apply must retry that transient rather than fail.
func TestSourceInitiated_Apply_RetriesSourceSideOnPropagationRace(t *testing.T) {
	shrinkPropagationRetry(t)
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log,
		failN:   2,
		failErr: fmt.Errorf("unexpected status code 400: the destination cluster does not have a link named src-to-dest"),
	}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err, "transient propagation race must be retried, not surfaced")
	require.Len(t, out.Created, 2)
	require.Len(t, log.entries, 2, "both sides eventually created (source after retries)")
	require.Equal(t, "src", log.entries[1].side)
}

// A non-propagation error on the source side is NOT retried — it fails fast.
func TestSourceInitiated_Apply_DoesNotRetryOtherErrors(t *testing.T) {
	shrinkPropagationRetry(t)
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log,
		failN:   1,
		failErr: fmt.Errorf("unexpected status code 401: authentication failed"),
	}
	r := newSourceInitiated(fakeSource{id: "src-1"}, dest, srcLink)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	_, err = r.Apply(context.Background(), plan)
	require.Error(t, err)
	require.Contains(t, err.Error(), "authentication failed")
}

func TestSourceInitiated_BothPresent_AllPresent(t *testing.T) {
	log := &createLog{}
	dest := &recordingTarget{clusterID: "dest-1", side: "dest", log: log,
		existing: &svclink.ClusterLink{LinkName: "src-to-dest"}}
	srcLink := &recordingTarget{clusterID: "src-1", side: "src", log: log,
		existing:    &svclink.ClusterLink{LinkName: "src-to-dest"},
		liveConfigs: map[string]string{"consumer.offset.sync.enable": "true"}}
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
		existing:    &svclink.ClusterLink{LinkName: "src-to-dest"},
		liveConfigs: map[string]string{"consumer.offset.sync.enable": "true"}}
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

// --- config drift tests ---

func TestPlan_ConfigDrift_IsDrift(t *testing.T) {
	tgt := &fakeTarget{clusterID: "dest-1",
		existing:    &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1"},
		liveConfigs: map[string]string{"consumer.offset.sync.ms": "30000"}}
	r := New(Config{
		LinkName:               "src-to-dest",
		SourceBootstrapServers: []string{"s:9092"},
		Auth:                   LinkAuth{SecurityProtocol: "PLAINTEXT"},
		Configs:                map[string]string{"consumer.offset.sync.ms": "1000"},
	}, fakeSource{id: "src-1"}, tgt)
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcile.ActionDrift, plan.Changes()[0].Action)
	require.Contains(t, plan.Changes()[0].Detail, "consumer.offset.sync.ms")
	require.True(t, plan.Empty(), "config drift must not create")
}

func TestPlan_ConfigMatches_IsPresent(t *testing.T) {
	tgt := &fakeTarget{clusterID: "dest-1",
		existing:    &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1"},
		liveConfigs: map[string]string{"consumer.offset.sync.ms": "1000", "other": "x"}}
	r := New(Config{
		LinkName:               "src-to-dest",
		SourceBootstrapServers: []string{"s:9092"},
		Auth:                   LinkAuth{SecurityProtocol: "PLAINTEXT"},
		Configs:                map[string]string{"consumer.offset.sync.ms": "1000"},
	}, fakeSource{id: "src-1"}, tgt)
	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, reconcile.ActionPresent, plan.Changes()[0].Action)
}

func TestApply_ConfigDrift_DoesNotAlter(t *testing.T) {
	tgt := &fakeTarget{clusterID: "dest-1",
		existing:    &svclink.ClusterLink{LinkName: "src-to-dest", SourceClusterID: "src-1"},
		liveConfigs: map[string]string{"cluster.link.prefix": "old."}}
	r := New(Config{
		LinkName: "src-to-dest", SourceBootstrapServers: []string{"s:9092"},
		Auth:    LinkAuth{SecurityProtocol: "PLAINTEXT"},
		Configs: map[string]string{"cluster.link.prefix": "new."},
	}, fakeSource{id: "src-1"}, tgt)
	plan, _ := r.Plan(context.Background())
	out, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, out.Drift, 1)
	require.Nil(t, tgt.createdReq, "config drift must never create/alter")
}
