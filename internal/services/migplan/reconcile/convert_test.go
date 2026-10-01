package reconcile

import (
	"strings"
	"testing"
)

// convertGateway is a fully-migrated TBM route: both topics have kcp's
// condition to cc ahead of the catch-all to msk.
func convertGateway() *GatewayConfig {
	rules := map[string]any{
		"routing": map[string]any{
			"coordination": map[string]any{"group": "msk"},
			"conditions": []any{
				map[string]any{"topics": []any{"orders", "payments"}, "streamingDomain": "cc"},
				map[string]any{"topicPatterns": []any{".*"}, "streamingDomain": "msk"},
			},
		},
		"fencing": []any{},
	}
	route := dynamicRouteRaw()
	route["rules"] = rules
	return &GatewayConfig{Route: &RouteConfig{
		Name: "migration-route", Mode: "dynamic", BoundDomains: []string{"msk", "cc"},
		Rules: rules, Raw: route,
	}}
}

func convertInput() ReconcileInput {
	return ReconcileInput{Route: "migration-route", TargetDomain: "cc", ConvertTo: "static"}
}

var convergedTopics = []string{"orders", "payments"}

func convergedMirrors() map[string]MirrorState {
	return map[string]MirrorState{"orders": MirrorStopped, "payments": MirrorStopped}
}

func reconcileConverged(gw *GatewayConfig, groups GroupFacts) *Plan {
	return ReconcileConvert(convertInput(), gw, convergedTopics, convergedTopics, convergedMirrors(), false, ClusterIDs{}, groups)
}

func convertPrecondition(t *testing.T, r Report, name string) PreconditionResult {
	t.Helper()
	for _, p := range r.Preconditions {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("no precondition %q in %+v", name, r.Preconditions)
	return PreconditionResult{}
}

func TestReconcileConvert_HappyPath(t *testing.T) {
	p := reconcileConverged(convertGateway(), GroupFacts{})

	if p.Mode != "convert" {
		t.Fatalf("Mode = %q, want convert", p.Mode)
	}
	if p.Report.Refused() || p.Artifacts == nil || p.NothingToDo {
		t.Fatalf("want a feasible plan with artifacts, got %+v", p)
	}
	if !p.Report.ConvertToStatic {
		t.Error("Report.ConvertToStatic must be set when a conversion has work")
	}
	if len(p.Report.Unchanged) != 2 {
		t.Errorf("Unchanged = %v, want both topics", p.Report.Unchanged)
	}
	fencing := artifactFencing(t, p.Artifacts.FenceRules)
	if len(fencing) != 1 || !isKcpConvertFence(fencing[0]) {
		t.Errorf("fence artifact fencing = %v, want exactly kcp's convert fence", fencing)
	}
	if got := artifactFencing(t, p.Artifacts.RollbackFenceRules); len(got) != 0 {
		t.Errorf("rollback artifact fencing = %v, want none", got)
	}
	route := switchedRoute(t, p.Artifacts.SwitchoverRules)
	if sd, _ := route["streamingDomain"].(map[string]any); sd["name"] != "cc" || sd["bootstrapServerId"] != "CC" {
		t.Errorf("switched streamingDomain = %v, want cc/CC", route["streamingDomain"])
	}
	if !p.Artifacts.RollbackAllowed {
		t.Error("a conversion promotes nothing, so rollback is always allowed before the switch")
	}
	if p.Artifacts.FencedAtStart {
		t.Error("FencedAtStart must be false on a route kcp hasn't fenced")
	}
}

func TestReconcileConvert_RefusesAnUnmigratedTopic(t *testing.T) {
	topics := append([]string{"refunds"}, convergedTopics...)
	mirrors := convergedMirrors()
	mirrors["refunds"] = MirrorActive

	p := ReconcileConvert(convertInput(), convertGateway(), topics, topics, mirrors, false, ClusterIDs{}, GroupFacts{})

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a topic still routed to the source must refuse the conversion")
	}
	if len(p.Report.FailFast) != 1 || p.Report.FailFast[0].Topic != "refunds" {
		t.Fatalf("FailFast = %v, want refunds", p.Report.FailFast)
	}
	if !strings.Contains(p.Report.FailFast[0].Reason, "not migrated yet") {
		t.Errorf("reason = %q, want it to say the topic isn't migrated yet", p.Report.FailFast[0].Reason)
	}
}

func TestReconcileConvert_RefusesSplitBrain(t *testing.T) {
	p := reconcileConverged(convertGateway(), GroupFacts{
		SourceGroups: []string{"orders-app"},
		TargetStates: map[string]string{"orders-app": "Stable"},
	})
	if !p.Report.Refused() {
		t.Fatal("a source group active on the destination must refuse")
	}
	if pc := convertPrecondition(t, p.Report, GroupSplitBrainCheckName); pc.OK {
		t.Errorf("split-brain precondition = %+v, want a failure", pc)
	}
}

func TestReconcileConvert_IdleDestinationGroupWarns(t *testing.T) {
	p := reconcileConverged(convertGateway(), GroupFacts{
		SourceGroups: []string{"billing"},
		TargetStates: map[string]string{"billing": "Empty"},
	})
	if p.Report.Refused() {
		t.Fatalf("an Empty destination group must not refuse, got %+v", p.Report)
	}
	if len(p.Report.Warnings) != 1 || !strings.Contains(p.Report.Warnings[0], "billing") {
		t.Errorf("warnings = %v, want one naming billing", p.Report.Warnings)
	}
}

func TestReconcileConvert_ResumeAlreadyFenced(t *testing.T) {
	gw := convertGateway()
	gw.Route.Rules["fencing"] = []any{map[string]any{"topicPatterns": []any{".*"}, "blocked": true}}

	p := reconcileConverged(gw, GroupFacts{})

	if p.Report.Refused() || p.Artifacts == nil {
		t.Fatalf("a resume after the fence must still plan, got %+v", p.Report)
	}
	if !p.Artifacts.FencedAtStart {
		t.Error("FencedAtStart must be true when the route already carries kcp's convert fence")
	}
	if got := artifactFencing(t, p.Artifacts.FenceRules); len(got) != 1 {
		t.Errorf("fence artifact fencing = %v, want one convert fence, not two", got)
	}
	if got := artifactFencing(t, p.Artifacts.RollbackFenceRules); len(got) != 0 {
		t.Errorf("rollback artifact fencing = %v, want kcp's fence removed", got)
	}
}

func TestReconcileConvert_RefusesAFenceKcpDidNotWrite(t *testing.T) {
	gw := convertGateway()
	gw.Route.Rules["fencing"] = []any{map[string]any{"topics": []any{"orders"}, "blocked": true, "trafficType": "PRODUCE"}}

	p := reconcileConverged(gw, GroupFacts{})

	if !p.Report.Refused() {
		t.Fatal("an operator fence the switch would drop must refuse")
	}
	if pc := convertPrecondition(t, p.Report, "route has no fence kcp didn't write"); pc.OK {
		t.Errorf("foreign-fence precondition = %+v, want a failure", pc)
	}
}

func TestReconcileConvert_RefusesWithoutTargetAuth(t *testing.T) {
	gw := convertGateway()
	delete(gw.Route.Raw["security"].(map[string]any)["cluster"].(map[string]any), "cc")

	p := reconcileConverged(gw, GroupFacts{})

	if pc := convertPrecondition(t, p.Report, "route carries auth for the target domain"); pc.OK {
		t.Errorf("target-auth precondition = %+v, want a failure", pc)
	}
}

func TestReconcileConvert_RefusesATargetBindingWithoutABootstrapID(t *testing.T) {
	gw := convertGateway()
	gw.Route.Raw["streamingDomains"] = []any{
		map[string]any{"name": "msk", "bootstrapServerId": "MSK"},
		map[string]any{"name": "cc"},
	}

	p := reconcileConverged(gw, GroupFacts{})

	if pc := convertPrecondition(t, p.Report, "target binding carries a bootstrap server id"); pc.OK {
		t.Errorf("bootstrap-id precondition = %+v, want a failure", pc)
	}
}

func TestReconcileConvert_AlreadyStaticOnTargetIsNothingToDo(t *testing.T) {
	gw := staticGateway()
	gw.Route.Raw["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}

	p := reconcileConverged(gw, GroupFacts{})

	if p.Report.Refused() || !p.NothingToDo || p.Artifacts != nil {
		t.Fatalf("a completed conversion must re-reconcile to nothing to do, got %+v", p)
	}
	if p.Mode != "convert" {
		t.Errorf("Mode = %q, want convert", p.Mode)
	}
}

func TestReconcileConvert_StaticElsewhereIsRefused(t *testing.T) {
	p := reconcileConverged(staticGateway(), GroupFacts{}) // static, bound to msk

	if !p.Report.Refused() {
		t.Fatal("a static route bound to another domain has nothing to convert and must refuse")
	}
}

func TestReconcile_RefusesAConversionInput(t *testing.T) {
	p := Reconcile(convertInput(), dynGateway(), nil, nil, nil, false, ClusterIDs{}, nil, "")

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("Reconcile must refuse an input with ConvertTo set, never plan it as a topic migration")
	}
}

// ReconcileConvert runs only a conversion to static; any other ConvertTo (a
// future convertTo: dynamic, or none) is refused before anything else.
func TestReconcileConvert_RefusesANonStaticTarget(t *testing.T) {
	for _, to := range []string{"dynamic", "", "Static"} {
		t.Run(to, func(t *testing.T) {
			in := convertInput()
			in.ConvertTo = to

			p := ReconcileConvert(in, convertGateway(), convergedTopics, convergedTopics, convergedMirrors(), false, ClusterIDs{}, GroupFacts{})

			if !p.Report.Refused() || p.Artifacts != nil || p.NothingToDo {
				t.Fatalf("ConvertTo %q must refuse, got %+v", to, p)
			}
			pc := convertPrecondition(t, p.Report, "conversion target is static")
			if pc.OK || !strings.Contains(pc.Detail, `"`+to+`"`) {
				t.Errorf("precondition = %+v, want a failure naming %q", pc, to)
			}
		})
	}
	// Also refused on a route that is already static on the target: the
	// strategy never reports nothing-to-do for a conversion it doesn't run.
	gw := staticGateway()
	gw.Route.Raw["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	in := convertInput()
	in.ConvertTo = "dynamic"
	if p := ReconcileConvert(in, gw, convergedTopics, convergedTopics, convergedMirrors(), false, ClusterIDs{}, GroupFacts{}); !p.Report.Refused() || p.NothingToDo {
		t.Fatalf("ConvertTo dynamic on a static route must refuse, got %+v", p)
	}
}

const routeFenceCheckName = "route has no route-level fence"

// A route-level fence on the dynamic route is carried into the static route
// unchanged, where the gateway would enforce it; the conversion refuses it.
func TestReconcileConvert_RefusesARouteLevelFence(t *testing.T) {
	gw := convertGateway()
	gw.Route.Raw["fence"] = map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}

	p := reconcileConverged(gw, GroupFacts{})

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a route-level fence the static route would enforce must refuse the conversion")
	}
	pc := convertPrecondition(t, p.Report, routeFenceCheckName)
	if pc.OK {
		t.Fatalf("route-fence precondition = %+v, want a failure", pc)
	}
	for _, want := range []string{"migration-route", `"scope":"ALL"`, "remove it"} {
		if !strings.Contains(pc.Detail, want) {
			t.Errorf("detail = %q, want it to contain %q", pc.Detail, want)
		}
	}
}

// An inert route-level fence (empty, or scope NONE as the CRD may default it)
// blocks nothing and does not refuse.
func TestReconcileConvert_InertRouteLevelFencePasses(t *testing.T) {
	for name, fence := range map[string]map[string]any{
		"empty":      {},
		"scope NONE": {"scope": "NONE"},
		"scope none": {"scope": "none", "errorCode": "BROKER_NOT_AVAILABLE"},
	} {
		t.Run(name, func(t *testing.T) {
			gw := convertGateway()
			gw.Route.Raw["fence"] = fence

			p := reconcileConverged(gw, GroupFacts{})

			if p.Report.Refused() || p.Artifacts == nil {
				t.Fatalf("an inert route-level fence must not refuse, got %+v", p.Report)
			}
			if pc := convertPrecondition(t, p.Report, routeFenceCheckName); !pc.OK {
				t.Errorf("route-fence precondition = %+v, want a pass", pc)
			}
		})
	}
}

// A completed conversion re-reconciles to nothing to do only when the static
// route is unfenced; a static route on the target that carries a live fence is
// refused, never reported as done.
func TestReconcileConvert_AlreadyStaticOnTargetWithAFenceIsRefused(t *testing.T) {
	gw := staticGateway()
	gw.Route.Raw["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	gw.Route.Raw["fence"] = map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}

	p := reconcileConverged(gw, GroupFacts{})

	if p.NothingToDo {
		t.Fatal("a fenced static route must not be reported as nothing to do")
	}
	if !p.Report.Refused() {
		t.Fatal("a fenced static route on the target must refuse")
	}
	if pc := convertPrecondition(t, p.Report, routeFenceCheckName); pc.OK {
		t.Errorf("route-fence precondition = %+v, want a failure", pc)
	}
}

// An inert fence on a completed conversion is still nothing to do.
func TestReconcileConvert_AlreadyStaticOnTargetWithAnInertFenceIsNothingToDo(t *testing.T) {
	gw := staticGateway()
	gw.Route.Raw["streamingDomain"] = map[string]any{"name": "cc", "bootstrapServerId": "cc-bootstrap"}
	gw.Route.Raw["fence"] = map[string]any{"scope": "NONE"}

	p := reconcileConverged(gw, GroupFacts{})

	if p.Report.Refused() || !p.NothingToDo {
		t.Fatalf("an inert fence on a completed conversion is still nothing to do, got %+v", p)
	}
}
