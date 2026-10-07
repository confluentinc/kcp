package migplan

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

type fakeGateway struct {
	gw  *reconcile.GatewayConfig
	err error
}

func (f *fakeGateway) Load(context.Context) (*reconcile.GatewayConfig, error) { return f.gw, f.err }

type fakeLister struct {
	topics    []string
	clusterID string
	err       error
}

func (f *fakeLister) ListTopics(context.Context) ([]string, error) { return f.topics, f.err }
func (f *fakeLister) ClusterID(context.Context) (string, error)    { return f.clusterID, f.err }

type fakeLink struct {
	ls  *LinkStatus
	err error
}

func (f *fakeLink) LinkStatus(context.Context) (*LinkStatus, error) { return f.ls, f.err }

type fakeSecretChecker struct {
	missing    []string
	skipReason string
	calledWith []string
}

func (f *fakeSecretChecker) MissingSecrets(_ context.Context, names []string) ([]string, string, error) {
	f.calledWith = names
	return f.missing, f.skipReason, nil
}

func dynGatewayConfig() *reconcile.GatewayConfig {
	return &reconcile.GatewayConfig{Route: &reconcile.RouteConfig{
		Name:         "migration-route",
		Mode:         "dynamic",
		BoundDomains: []string{"msk", "cc"},
		Rules: map[string]any{"routing": map[string]any{
			"coordination": map[string]any{"group": "msk"},
			"default":      "msk",
		}},
	}}
}

// staticGatewayConfig mirrors reconcile's own package-scoped staticGateway()
// test helper (internal/services/migplan/reconcile/preconditions_test.go) —
// duplicated here rather than imported since that helper is unexported and
// this package's tests exercise the engine, not the pure core directly. The
// route is bound to "msk" (not the target "cc"), and stages redundant auth
// for "cc" referencing a single Secret, "cc-sasl-secret" — the one live
// secret-existence check a static-mode Run must make.
func staticGatewayConfig() *reconcile.GatewayConfig {
	route := map[string]any{
		"name":            "migration-route",
		"streamingDomain": map[string]any{"name": "msk", "bootstrapServerId": "msk-bootstrap"},
		"security": map[string]any{
			"cluster": map[string]any{
				"cc": map[string]any{
					"secretStore": "vault",
					"authentication": map[string]any{
						"sasl": map[string]any{"secretRef": "cc-sasl-secret"},
					},
				},
			},
		},
	}
	obj := map[string]any{
		"spec": map[string]any{
			"streamingDomains": []any{
				map[string]any{
					"name":         "msk",
					"kafkaCluster": map[string]any{"bootstrapServers": []any{map[string]any{"id": "msk-bootstrap"}}},
				},
				map[string]any{
					"name":         "cc",
					"kafkaCluster": map[string]any{"bootstrapServers": []any{map[string]any{"id": "cc-bootstrap"}}},
				},
			},
			"routes": []any{route},
		},
	}
	return &reconcile.GatewayConfig{
		Route:  &reconcile.RouteConfig{Name: "migration-route", Mode: "static", BoundDomains: []string{"msk"}, Raw: route},
		RawObj: obj,
	}
}

func TestEngineRunHappyPath(t *testing.T) {
	eng := NewReconciliationEngine(
		&fakeGateway{gw: dynGatewayConfig()},
		&fakeLister{topics: []string{"orders"}}, // source
		&fakeLister{topics: []string{"orders"}}, // target
		&fakeLink{ls: &LinkStatus{
			OffsetSyncEnabled: false,
			Mirrors:           map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		&fakeSecretChecker{},
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.Refused() {
		t.Fatalf("expected success, refused: %+v", plan.Report)
	}
	if plan.Artifacts == nil || len(plan.Artifacts.PromoteTopics) != 1 {
		t.Fatalf("expected 1 migratable topic, got %+v", plan.Artifacts)
	}
}

// TestEngineRunProviderErrorIsError proves that a failure in ANY of the four
// providers surfaces as a Go error (an I/O failure the caller must handle), never
// swallowed into a feasibility refusal — a refusal means "read everything, the
// plan is infeasible", which is a different outcome from "could not read".
func TestEngineRunProviderErrorIsError(t *testing.T) {
	boom := errors.New("boom")
	okLink := &fakeLink{ls: &LinkStatus{Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive}}}
	cases := []struct {
		name string
		eng  *ReconciliationEngine
	}{
		{"gateway", NewReconciliationEngine(&fakeGateway{err: boom}, &fakeLister{}, &fakeLister{}, okLink, &fakeSecretChecker{})},
		{"source", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{err: boom}, &fakeLister{}, okLink, &fakeSecretChecker{})},
		{"target", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{}, &fakeLister{err: boom}, okLink, &fakeSecretChecker{})},
		{"link", NewReconciliationEngine(&fakeGateway{gw: dynGatewayConfig()}, &fakeLister{}, &fakeLister{}, &fakeLink{err: boom}, &fakeSecretChecker{})},
	}
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if _, err := c.eng.Run(context.Background(), in); err == nil {
				t.Fatalf("a %s-provider error must be returned as a Go error, not swallowed into a plan", c.name)
			}
		})
	}
}

// TestReconciliationEngine_Run_StaticMode_ChecksOnlyTargetSecret proves that a
// static-mode route causes Run to resolve exactly the target's staged secret
// name(s) via reconcile.ResolveStagedSecretNames and invoke the secrets
// provider with exactly that list — not an empty/whole-CR scan — and that the
// provider's result flows through to the returned Plan (via Reconcile's
// missingSecrets arg, surfaced as a precondition failure when non-empty).
func TestReconciliationEngine_Run_StaticMode_ChecksOnlyTargetSecret(t *testing.T) {
	secrets := &fakeSecretChecker{missing: nil}
	eng := NewReconciliationEngine(
		&fakeGateway{gw: staticGatewayConfig()},
		&fakeLister{topics: []string{"orders"}}, // source
		&fakeLister{topics: []string{"orders"}}, // target
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secrets,
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "static" {
		t.Fatalf("plan.Mode = %q, want %q", plan.Mode, "static")
	}
	if len(secrets.calledWith) != 1 || secrets.calledWith[0] != "cc-sasl-secret" {
		t.Fatalf("secrets.calledWith = %v, want [cc-sasl-secret]", secrets.calledWith)
	}

	// Now prove a reported-missing secret surfaces as a precondition failure.
	secretsMissing := &fakeSecretChecker{missing: []string{"cc-sasl-secret"}}
	eng2 := NewReconciliationEngine(
		&fakeGateway{gw: staticGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secretsMissing,
	)
	plan2, err := eng2.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if !plan2.Report.Refused() {
		t.Fatalf("expected refusal when the secrets provider reports a missing secret, got %+v", plan2.Report)
	}
}

// TestReconciliationEngine_Run_StaticMode_SecretCheckSkippedIsWarningNotRefusal
// is a regression test for a real bug found via live e2e testing: a
// permission denial reaching this far must surface as a Report.Warnings
// entry, never a refusal — a skipped check is not the same fact as a
// confirmed-missing secret, and conflating them made every AAO e2e migration
// fail outright in an RBAC-restricted namespace.
func TestReconciliationEngine_Run_StaticMode_SecretCheckSkippedIsWarningNotRefusal(t *testing.T) {
	secrets := &fakeSecretChecker{skipReason: "no permission to read secrets in namespace \"confluent\""}
	eng := NewReconciliationEngine(
		&fakeGateway{gw: staticGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secrets,
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	plan, err := eng.Run(context.Background(), in)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Report.Refused() {
		t.Fatalf("a skipped secret check must not refuse the plan, got %+v", plan.Report)
	}
	found := false
	for _, w := range plan.Report.Warnings {
		if w == secrets.skipReason {
			found = true
		}
	}
	if !found {
		t.Fatalf("Report.Warnings = %v, want it to contain the skip reason %q", plan.Report.Warnings, secrets.skipReason)
	}

	// The precondition line itself must record the skip too — not a plain
	// pass — so the rendered report never claims "✓ staged auth secrets
	// exist" for a check that never ran, contradicting the warning above.
	foundPrecondition := false
	for _, pc := range plan.Report.Preconditions {
		if pc.Name == "staged auth secrets exist" {
			foundPrecondition = true
			if !pc.OK || !pc.Skipped {
				t.Errorf("staged auth secrets exist precondition = %+v, want OK: true, Skipped: true", pc)
			}
			if pc.Detail != secrets.skipReason {
				t.Errorf("precondition Detail = %q, want the skip reason %q", pc.Detail, secrets.skipReason)
			}
		}
	}
	if !foundPrecondition {
		t.Fatalf("expected a %q precondition, got %+v", "staged auth secrets exist", plan.Report.Preconditions)
	}
}

// TestReconciliationEngine_Run_DynamicMode_NeverCallsSecretsProvider proves
// that a dynamic-mode gateway never invokes the secrets provider at all — a
// dynamic-route migration has no redundant-auth concept, so no live secret
// check should ever be made for one.
func TestReconciliationEngine_Run_DynamicMode_NeverCallsSecretsProvider(t *testing.T) {
	secrets := &fakeSecretChecker{}
	eng := NewReconciliationEngine(
		&fakeGateway{gw: dynGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{
			Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive},
		}},
		secrets,
	)
	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	if _, err := eng.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if secrets.calledWith != nil {
		t.Fatalf("secrets provider must never be called for a dynamic-mode route, calledWith=%v", secrets.calledWith)
	}
}

type fakeGroupLister struct {
	partitions    map[string]int // nil answers 1 for every requested topic
	partitionsErr error
	partitionsFor []string
	partitionsN   int
	groups        []types.ConsumerGroupListing
	err           error
	calls         int
	tracked       map[string][]string
	trackedErr    error
	trackedFor    []string // the group ids CommittedTopics was asked about
	trackedN      int
	denied        bool // CanDescribeAnyGroup reports false
	accessErr     error
	topicsDenied  bool // CanDescribeAnyTopic reports false
	topicsErr     error
}

func (f *fakeGroupLister) CanDescribeAnyGroup(context.Context) (bool, error) {
	if f.accessErr != nil {
		return false, f.accessErr
	}
	return !f.denied, nil
}

func (f *fakeGroupLister) ListGroups(context.Context) ([]types.ConsumerGroupListing, error) {
	f.calls++
	return f.groups, f.err
}

func (f *fakeGroupLister) CommittedTopics(_ context.Context, groups []string) (map[string][]string, error) {
	f.trackedN++
	f.trackedFor = groups
	return f.tracked, f.trackedErr
}

// convertEngineGateway is a fully-migrated dynamic route the conversion can plan.
func convertEngineGateway() *reconcile.GatewayConfig {
	rules := map[string]any{"routing": map[string]any{
		"coordination": map[string]any{"group": "msk"},
		"conditions":   []any{map[string]any{"topics": []any{"orders"}, "streamingDomain": "cc"}},
	}}
	raw := map[string]any{
		"name": "migration-route",
		"streamingDomains": []any{
			map[string]any{"name": "msk", "bootstrapServerId": "MSK"},
			map[string]any{"name": "cc", "bootstrapServerId": "CC"},
		},
		"security": map[string]any{"cluster": map[string]any{"cc": map[string]any{"auth": "passthrough"}}},
		"rules":    rules,
	}
	return &reconcile.GatewayConfig{Route: &reconcile.RouteConfig{
		Name: "migration-route", Mode: "dynamic", BoundDomains: []string{"msk", "cc"}, Rules: rules, Raw: raw,
	}}
}

func convertEngine(src, tgt GroupLister) *ReconciliationEngine {
	return NewReconciliationEngine(
		&fakeGateway{gw: convertEngineGateway()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorStopped}}},
		&fakeSecretChecker{},
	).WithGroupListers(src, tgt)
}

var convertIn = reconcile.ReconcileInput{Route: "migration-route", TargetDomain: "cc", ConvertTo: "static"}

func TestEngineRun_ConversionUsesBothGroupListers(t *testing.T) {
	src := &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "orders-app"}}}
	tgt := &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "orders-app", State: "Stable"}}}

	plan, err := convertEngine(src, tgt).Run(context.Background(), convertIn)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "convert" {
		t.Fatalf("Mode = %q, want convert", plan.Mode)
	}
	if !plan.Report.Refused() {
		t.Fatal("the group listing must reach the split-brain check: orders-app is active on the destination")
	}
	if src.calls != 1 || tgt.calls != 1 {
		t.Errorf("group lister calls = %d/%d, want 1/1", src.calls, tgt.calls)
	}
}

func TestEngineRun_ConversionFetchesCommittedTopicsForSourceGroupsOnly(t *testing.T) {
	src := &fakeGroupLister{
		groups:  []types.ConsumerGroupListing{{GroupID: "a"}, {GroupID: "b"}},
		tracked: map[string][]string{"a": {"orders"}},
	}
	tgt := &fakeGroupLister{}

	plan, err := convertEngine(src, tgt).Run(context.Background(), convertIn)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(src.trackedFor, []string{"a", "b"}) || src.trackedN != 1 {
		t.Errorf("source CommittedTopics asked about %v (%d calls), want [a b] once", src.trackedFor, src.trackedN)
	}
	if tgt.trackedN != 0 {
		t.Errorf("destination CommittedTopics called %d times, want 0", tgt.trackedN)
	}
	if len(plan.Report.Unchanged) != 1 || plan.Report.Unchanged[0].Topic != "orders" {
		t.Errorf("Unchanged = %v, want the tracked topic orders checked", plan.Report.Unchanged)
	}
}

func TestEngineRun_ConversionRefusesWhenTheSourceCredentialCannotSeeEveryGroup(t *testing.T) {
	for name, src := range map[string]*fakeGroupLister{
		"denied": {denied: true},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := convertEngine(src, &fakeGroupLister{}).Run(context.Background(), convertIn)
			if err != nil {
				t.Fatal(err)
			}
			if !plan.Report.Refused() {
				t.Fatal("a source credential whose group listing may be partial must refuse the conversion")
			}
			refused := false
			for _, pc := range plan.Report.Preconditions {
				if pc.Name == reconcile.SourceGroupVisibilityCheckName && !pc.OK {
					refused = true
				}
			}
			if !refused {
				t.Errorf("preconditions = %+v, want %q to fail", plan.Report.Preconditions, reconcile.SourceGroupVisibilityCheckName)
			}
		})
	}
}

func TestEngineRun_ConversionRefusesWhenTheDestinationCredentialCannotSeeEveryGroup(t *testing.T) {
	for name, tgt := range map[string]*fakeGroupLister{
		"denied": {denied: true},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := convertEngine(&fakeGroupLister{}, tgt).Run(context.Background(), convertIn)
			if err != nil {
				t.Fatal(err)
			}
			failed := map[string]bool{}
			for _, pc := range plan.Report.Preconditions {
				if !pc.OK {
					failed[pc.Name] = true
				}
			}
			if !failed[reconcile.TargetGroupVisibilityCheckName] || failed[reconcile.SourceGroupVisibilityCheckName] {
				t.Errorf("failed preconditions = %v, want only %q", failed, reconcile.TargetGroupVisibilityCheckName)
			}
		})
	}
}

func (f *fakeGroupLister) CanDescribeAnyTopic(context.Context) (bool, error) {
	if f.topicsErr != nil {
		return false, f.topicsErr
	}
	return !f.topicsDenied, nil
}

func TestEngineRun_ConversionRefusesWhenACredentialCannotDescribeEveryTopic(t *testing.T) {
	for name, tc := range map[string]struct {
		src, tgt *fakeGroupLister
		failing  string
	}{
		"source":      {&fakeGroupLister{topicsDenied: true}, &fakeGroupLister{}, reconcile.SourceTopicVisibilityCheckName},
		"destination": {&fakeGroupLister{}, &fakeGroupLister{topicsDenied: true}, reconcile.TargetTopicVisibilityCheckName},
	} {
		t.Run(name, func(t *testing.T) {
			plan, err := convertEngine(tc.src, tc.tgt).Run(context.Background(), convertIn)
			if err != nil {
				t.Fatal(err)
			}
			failed := map[string]bool{}
			for _, pc := range plan.Report.Preconditions {
				if !pc.OK {
					failed[pc.Name] = true
				}
			}
			if len(failed) != 1 || !failed[tc.failing] {
				t.Errorf("failed preconditions = %v, want only %q", failed, tc.failing)
			}
		})
	}
}

func TestEngineRun_ConversionTopicProbeFailureIsAnError(t *testing.T) {
	boom := errors.New("metadata failed")
	for name, eng := range map[string]*ReconciliationEngine{
		"source":      convertEngine(&fakeGroupLister{topicsErr: boom}, &fakeGroupLister{}),
		"destination": convertEngine(&fakeGroupLister{}, &fakeGroupLister{topicsErr: boom}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eng.Run(context.Background(), convertIn); !errors.Is(err, boom) {
				t.Fatalf("Run error = %v, want the topic probe failure as an error, not a refusal", err)
			}
		})
	}
}

func TestEngineRun_ConversionAccessCheckFailureIsAnError(t *testing.T) {
	boom := errors.New("metadata failed")
	for name, eng := range map[string]*ReconciliationEngine{
		"source":      convertEngine(&fakeGroupLister{accessErr: boom}, &fakeGroupLister{}),
		"destination": convertEngine(&fakeGroupLister{}, &fakeGroupLister{accessErr: boom}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eng.Run(context.Background(), convertIn); !errors.Is(err, boom) {
				t.Fatalf("Run error = %v, want the access check failure as an error, not a refusal", err)
			}
		})
	}
}

func TestEngineRun_ConversionCommittedTopicsFailureIsAnError(t *testing.T) {
	boom := errors.New("coordinator not available")
	src := &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "a"}}, trackedErr: boom}
	if _, err := convertEngine(src, &fakeGroupLister{}).Run(context.Background(), convertIn); !errors.Is(err, boom) {
		t.Fatalf("Run error = %v, want the committed-offsets failure as an error, not a refusal", err)
	}
}

func TestEngineRun_ConversionGroupListingFailureIsAnError(t *testing.T) {
	boom := errors.New("broker b2 failed")
	for name, eng := range map[string]*ReconciliationEngine{
		"source": convertEngine(&fakeGroupLister{err: boom}, &fakeGroupLister{}),
		"target": convertEngine(&fakeGroupLister{}, &fakeGroupLister{err: boom}),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := eng.Run(context.Background(), convertIn); !errors.Is(err, boom) {
				t.Fatalf("Run error = %v, want the group listing failure as an error, not a refusal", err)
			}
		})
	}
}

func TestEngineRun_ConversionWithoutGroupListersIsAnError(t *testing.T) {
	eng := NewReconciliationEngine(&fakeGateway{gw: convertEngineGateway()}, &fakeLister{}, &fakeLister{},
		&fakeLink{ls: &LinkStatus{}}, &fakeSecretChecker{})
	if _, err := eng.Run(context.Background(), convertIn); err == nil {
		t.Fatal("a conversion with no group listers must error")
	}
}

func TestEngineRun_TopicMigrationNeverListsGroups(t *testing.T) {
	src, tgt := &fakeGroupLister{}, &fakeGroupLister{}
	eng := NewReconciliationEngine(
		&fakeGateway{gw: dynGatewayConfig()},
		&fakeLister{topics: []string{"orders"}},
		&fakeLister{topics: []string{"orders"}},
		&fakeLink{ls: &LinkStatus{Mirrors: map[string]reconcile.MirrorState{"orders": reconcile.MirrorActive}}},
		&fakeSecretChecker{},
	).WithGroupListers(src, tgt)

	in := reconcile.ReconcileInput{Topics: []string{"orders"}, Route: "migration-route", TargetDomain: "cc"}
	if _, err := eng.Run(context.Background(), in); err != nil {
		t.Fatal(err)
	}
	if src.calls != 0 || tgt.calls != 0 {
		t.Errorf("a topic migration listed groups %d/%d times, want 0", src.calls, tgt.calls)
	}
}

func (f *fakeGroupLister) PartitionCounts(_ context.Context, topics []string) (map[string]int, error) {
	f.partitionsN++
	f.partitionsFor = topics
	if f.partitionsErr != nil {
		return nil, f.partitionsErr
	}
	if f.partitions != nil {
		return f.partitions, nil
	}
	out := make(map[string]int, len(topics))
	for _, t := range topics {
		out[t] = 1
	}
	return out, nil
}
