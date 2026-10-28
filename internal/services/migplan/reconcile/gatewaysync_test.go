package reconcile

import (
	"reflect"
	"strings"
	"testing"
)

var testSync = func(v SyncVerdict) GatewaySync {
	return GatewaySync{Verdict: v, Detail: "pod p-0 reports configId 41, the CR holds 42", Evidence: []string{"p-0: 41", "p-1: 42"}}
}

func syncPrecondition(t *testing.T, p *Plan) (PreconditionResult, bool) {
	t.Helper()
	for _, pc := range p.Report.Preconditions {
		if pc.Name == GatewayPodsCheckName {
			return pc, true
		}
	}
	return PreconditionResult{}, false
}

func mustSyncPrecondition(t *testing.T, p *Plan) PreconditionResult {
	t.Helper()
	pc, ok := syncPrecondition(t, p)
	if !ok {
		t.Fatalf("no %q precondition in %+v", GatewayPodsCheckName, p.Report.Preconditions)
	}
	return pc
}

// syncScenario builds the same plan with a chosen verdict, so a test can compare
// what the verdict changed.
type syncScenario struct {
	name string
	run  func(s GatewaySync) *Plan
}

func staticFresh(s GatewaySync) *Plan {
	gw := staticGateway()
	gw.Sync = s
	in := ReconcileInput{Type: TypeStatic, Topics: []string{"t1", "t2"}, Route: "migration-route", TargetDomain: "cc"}
	return Reconcile(in, gw, []string{"t1", "t2"}, []string{}, map[string]MirrorState{"t1": MirrorActive, "t2": MirrorActive}, false, ClusterIDs{}, nil, "")
}

func dynamicFresh(s GatewaySync) *Plan {
	gw := dynGateway()
	gw.Sync = s
	in := ReconcileInput{Type: TypeDynamic, Topics: []string{"t1", "t2"}, Route: "migration-route", TargetDomain: "cc"}
	return Reconcile(in, gw, []string{"t1", "t2"}, []string{}, map[string]MirrorState{"t1": MirrorActive, "t2": MirrorActive}, false, ClusterIDs{}, nil, "")
}

var freshScenarios = []syncScenario{{"static", staticFresh}, {"dynamic", dynamicFresh}}

func TestGatewaySyncUncheckedChangesNothing(t *testing.T) {
	for _, sc := range freshScenarios {
		t.Run(sc.name, func(t *testing.T) {
			got := sc.run(GatewaySync{})
			want := sc.run(GatewaySync{Verdict: SyncUnchecked, Detail: "ignored", Evidence: []string{"ignored"}})
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("unchecked must not alter the plan:\n got %+v\nwant %+v", got, want)
			}
			if _, ok := syncPrecondition(t, got); ok {
				t.Fatal("unchecked must add no pod-check precondition")
			}
			if got.Report.Refused() || got.Artifacts == nil {
				t.Fatalf("expected a plan, got %+v", got.Report)
			}
		})
	}
}

func TestGatewaySyncNilGatewayIsUntouched(t *testing.T) {
	p := &Plan{Mode: "static", NothingToDo: true}
	if got := applyGatewaySync(p, nil); got != p || !got.NothingToDo {
		t.Fatalf("a nil gateway must leave the plan alone, got %+v", got)
	}
}

func TestGatewaySyncInSyncAddsPassingCheck(t *testing.T) {
	for _, sc := range freshScenarios {
		t.Run(sc.name, func(t *testing.T) {
			base := sc.run(GatewaySync{})
			got := sc.run(GatewaySync{Verdict: SyncInSync})
			pc := mustSyncPrecondition(t, got)
			if !pc.OK || pc.Skipped {
				t.Fatalf("in sync must be a plain pass, got %+v", pc)
			}
			if got.Report.Refused() || !reflect.DeepEqual(got.Artifacts, base.Artifacts) {
				t.Fatalf("in sync must leave the artifacts unchanged, got %+v", got.Report)
			}
		})
	}
}

func TestGatewaySyncOutOfSyncRefusesFreshPlan(t *testing.T) {
	for _, sc := range freshScenarios {
		t.Run(sc.name, func(t *testing.T) {
			in := testSync(SyncOutOfSync)
			got := sc.run(in)
			if !got.Report.Refused() || got.Artifacts != nil {
				t.Fatalf("out of sync must refuse with no artifacts, got %+v", got)
			}
			pc := mustSyncPrecondition(t, got)
			if pc.OK || pc.Detail != in.Detail || !reflect.DeepEqual(pc.Evidence, in.Evidence) {
				t.Fatalf("precondition = %+v, want a failure carrying the verdict's detail and evidence", pc)
			}
		})
	}
}

func TestGatewaySyncOutOfSyncRefusesNothingToDo(t *testing.T) {
	gw := staticGateway()
	route := gw.RawObj["spec"].(map[string]any)["routes"].([]any)[0].(map[string]any)
	route["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	gw.Sync = testSync(SyncOutOfSync)
	in := ReconcileInput{Type: TypeStatic, Topics: []string{"t1"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"t1"}, []string{"t1"}, map[string]MirrorState{"t1": MirrorStopped}, false, ClusterIDs{}, nil, "")
	if !p.Report.Refused() || p.NothingToDo {
		t.Fatalf("a nothing-to-do plan on out-of-sync pods must be refused, got %+v", p)
	}
}

func TestGatewaySyncOutOfSyncClearsRestore(t *testing.T) {
	gw := staticGateway()
	in := ReconcileInput{Type: TypeStatic, Topics: []string{"t1"}, Route: "migration-route", TargetDomain: "cc",
		PauseConsumerOffsetSync: true, OffsetSyncBaselineEnabled: true}
	run := func(s GatewaySync) *Plan {
		gw.Sync = s
		return Reconcile(in, gw, []string{"t1"}, []string{"t1"}, map[string]MirrorState{"t1": MirrorActive}, true, ClusterIDs{}, nil, "")
	}
	if !run(GatewaySync{}).Report.RestoreOffsetSync {
		t.Fatal("fixture must owe a restore without the verdict")
	}
	got := run(testSync(SyncOutOfSync))
	if got.Report.RestoreOffsetSync || !got.Report.Refused() {
		t.Fatalf("a refusal must not owe a restore, got %+v", got.Report)
	}
}

func TestGatewaySyncCannotTellFreshPlanProceeds(t *testing.T) {
	for _, sc := range freshScenarios {
		t.Run(sc.name, func(t *testing.T) {
			base := sc.run(GatewaySync{})
			in := testSync(SyncCannotTell)
			got := sc.run(in)
			if got.Report.Refused() || got.Artifacts == nil || !reflect.DeepEqual(got.Artifacts, base.Artifacts) {
				t.Fatalf("cannot tell on a from-scratch plan must proceed, got %+v", got.Report)
			}
			pc := mustSyncPrecondition(t, got)
			if !pc.OK || !pc.Skipped || pc.Detail != in.Detail || !reflect.DeepEqual(pc.Evidence, in.Evidence) {
				t.Fatalf("precondition = %+v, want a skip carrying the verdict's detail and evidence", pc)
			}
		})
	}
}

func TestGatewaySyncCannotTellFencedAtStartRefuses(t *testing.T) {
	gw := staticGateway()
	gw.Route.Raw["fence"] = map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}
	gw.Sync = testSync(SyncCannotTell)
	in := ReconcileInput{Type: TypeStatic, Topics: []string{"t1"}, Route: "migration-route", TargetDomain: "cc"}
	p := Reconcile(in, gw, []string{"t1"}, []string{"t1"}, map[string]MirrorState{"t1": MirrorActive}, false, ClusterIDs{}, nil, "")
	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatalf("a fenced-at-start plan must be refused on cannot tell, got %+v", p)
	}
	pc := mustSyncPrecondition(t, p)
	if pc.OK || !strings.HasSuffix(pc.Detail, "this plan treats the fence as already done") {
		t.Fatalf("Detail = %q", pc.Detail)
	}
	if !reflect.DeepEqual(pc.Evidence, testSync(SyncCannotTell).Evidence) {
		t.Fatalf("Evidence = %v", pc.Evidence)
	}
}

func TestGatewaySyncCannotTellUnchangedRefuses(t *testing.T) {
	gw := dynGateway()
	gw.Sync = testSync(SyncCannotTell)
	in := ReconcileInput{Type: TypeDynamic, Topics: []string{"t1", "t2"}, Route: "migration-route", TargetDomain: "cc"}
	// Route conditions send t1 to the target already.
	gw.Route.Rules = map[string]any{
		"routing": map[string]any{
			"coordination": map[string]any{"group": "msk"},
			"conditions":   []any{map[string]any{"topics": []any{"t1"}, "streamingDomain": "cc"}},
			"default":      "msk",
		},
	}
	p := Reconcile(in, gw, []string{"t1", "t2"}, []string{"t1", "t2"},
		map[string]MirrorState{"t1": MirrorStopped, "t2": MirrorActive}, false, ClusterIDs{}, nil, "")
	if len(p.Report.Unchanged) != 1 {
		t.Fatalf("fixture must leave one topic Unchanged, got %+v", p.Report)
	}
	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatalf("cannot tell with an Unchanged topic must refuse, got %+v", p)
	}
	if d := mustSyncPrecondition(t, p).Detail; !strings.Contains(d, "1 topic(s) on the target") {
		t.Fatalf("Detail = %q", d)
	}
}

func TestGatewaySyncConvert(t *testing.T) {
	t.Run("already static, cannot tell", func(t *testing.T) {
		gw := staticGateway()
		gw.Route.Raw["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
		gw.Sync = testSync(SyncCannotTell)
		p := reconcileConverged(gw, GroupFacts{})
		if !p.Report.Refused() || p.NothingToDo {
			t.Fatalf("a completed conversion must be refused on cannot tell, got %+v", p)
		}
		if d := mustSyncPrecondition(t, p).Detail; !strings.HasSuffix(d, "this plan treats the conversion as already done") {
			t.Fatalf("Detail = %q", d)
		}
	})
	t.Run("convertible, out of sync", func(t *testing.T) {
		if p := reconcileConverged(convertGateway(), trackedGroups()); !p.Report.ConvertToStatic || p.Artifacts == nil {
			t.Fatalf("fixture must convert without the verdict, got %+v", p.Report)
		}
		gw := convertGateway()
		gw.Sync = testSync(SyncOutOfSync)
		p := reconcileConverged(gw, trackedGroups())
		if !p.Report.Refused() || p.Report.ConvertToStatic || p.Artifacts != nil {
			t.Fatalf("out of sync must refuse the conversion, got %+v", p)
		}
	})
	t.Run("convertible, cannot tell names the link topics", func(t *testing.T) {
		gw := convertGateway()
		gw.Sync = testSync(SyncCannotTell)
		p := reconcileConverged(gw, trackedGroups())
		if !p.Report.Refused() {
			t.Fatalf("a conversion with link topics routed to the target must be refused, got %+v", p.Report)
		}
		if d := mustSyncPrecondition(t, p).Detail; !strings.Contains(d, "2 link topic(s) routed to the target") {
			t.Fatalf("Detail = %q", d)
		}
	})
}
