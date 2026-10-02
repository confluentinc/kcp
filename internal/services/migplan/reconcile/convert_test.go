package reconcile

import (
	"fmt"
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

// routeToTarget adds topic to the condition that sends kcp's migrated topics to
// the target domain.
func routeToTarget(gw *GatewayConfig, topic string) {
	routing := gw.Route.Rules["routing"].(map[string]any)
	cond := routing["conditions"].([]any)[0].(map[string]any)
	cond["topics"] = append(cond["topics"].([]any), topic)
}

func convertInput() ReconcileInput {
	return ReconcileInput{Route: "migration-route", TargetDomain: "cc", ConvertTo: "static"}
}

var convergedTopics = []string{"orders", "payments"}

// trackedGroups is one source group with committed offsets on both topics.
func trackedGroups() GroupFacts {
	return GroupFacts{
		SourceGroups:  []string{"orders-app"},
		TrackedTopics: map[string][]string{"orders-app": {"orders", "payments"}},
	}
}

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
	p := reconcileConverged(convertGateway(), trackedGroups())

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

func TestReconcileConvert_RefusesATrackedUnmigratedTopic(t *testing.T) {
	topics := append([]string{"refunds"}, convergedTopics...)
	mirrors := convergedMirrors()
	mirrors["refunds"] = MirrorActive
	groups := GroupFacts{
		SourceGroups:  []string{"refunds-app"},
		TrackedTopics: map[string][]string{"refunds-app": {"refunds"}},
	}

	p := ReconcileConvert(convertInput(), convertGateway(), topics, topics, mirrors, false, ClusterIDs{}, groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a topic a source group has committed on, still routed to the source, must refuse the conversion")
	}
	if len(p.Report.FailFast) != 1 || p.Report.FailFast[0].Topic != "refunds" {
		t.Fatalf("FailFast = %v, want refunds", p.Report.FailFast)
	}
	if !strings.Contains(p.Report.FailFast[0].Reason, "not migrated yet") {
		t.Errorf("reason = %q, want it to say the topic isn't migrated yet", p.Report.FailFast[0].Reason)
	}
}

func TestReconcileConvert_UntrackedUnmigratedTopicWarnsOnly(t *testing.T) {
	topics := append([]string{"refunds"}, convergedTopics...)
	mirrors := convergedMirrors()
	mirrors["refunds"] = MirrorActive

	// No source group has committed offsets on refunds.
	p := ReconcileConvert(convertInput(), convertGateway(), topics, topics, mirrors, false, ClusterIDs{}, trackedGroups())

	if p.Report.Refused() || p.Artifacts == nil {
		t.Fatalf("an untracked, unmigrated topic must not refuse, got %+v", p.Report)
	}
	if len(p.Report.Warnings) != 1 || !strings.Contains(p.Report.Warnings[0], "refunds") {
		t.Fatalf("warnings = %v, want one naming refunds", p.Report.Warnings)
	}
	if len(p.Report.Unchanged) != 2 || len(p.Report.FailFast) != 0 {
		t.Errorf("Unchanged = %v, FailFast = %v; want only the two tracked topics checked", p.Report.Unchanged, p.Report.FailFast)
	}
}

func TestReconcileConvert_UntrackedConvergedTopicIsSilent(t *testing.T) {
	// A converged topic nobody commits on (e.g. _schemas) adds no warning.
	p := reconcileConverged(convertGateway(), GroupFacts{})
	if p.Report.Refused() || len(p.Report.Warnings) != 0 {
		t.Fatalf("got %+v, want a clean plan", p.Report)
	}
}

func TestReconcileConvert_TrackedTopicOnNeitherClusterWarnsAndSkips(t *testing.T) {
	groups := GroupFacts{
		SourceGroups:  []string{"orders-app"},
		TrackedTopics: map[string][]string{"orders-app": {"orders", "payments", "ghost"}},
	}
	p := reconcileConverged(convertGateway(), groups)

	if p.Report.Refused() {
		t.Fatalf("a tracked topic that exists nowhere must not refuse, got %+v", p.Report)
	}
	if len(p.Report.Warnings) != 1 || !strings.Contains(p.Report.Warnings[0], "ghost") {
		t.Fatalf("warnings = %v, want one naming ghost", p.Report.Warnings)
	}
}

// A topic created natively on the destination is tracked by a source group
// (coordination is pinned to source, so the group's offsets on it live there).
// It was never mirrored and is not on the source, but it needs no migration.
func TestReconcileConvert_TrackedDestinationNativeTopicPasses(t *testing.T) {
	targets := append([]string{"audit-native"}, convergedTopics...) // on destination only
	groups := GroupFacts{
		SourceGroups:  []string{"audit-app"},
		TrackedTopics: map[string][]string{"audit-app": {"orders", "payments", "audit-native"}},
	}
	gw := convertGateway()
	routeToTarget(gw, "audit-native")
	p := ReconcileConvert(convertInput(), gw, convergedTopics, targets, convergedMirrors(), false, ClusterIDs{}, groups)

	if p.Report.Refused() || p.Artifacts == nil {
		t.Fatalf("a tracked destination-native topic must not refuse, got %+v", p.Report)
	}
	if len(p.Report.Warnings) != 0 {
		t.Errorf("warnings = %v, want none: the topic exists, needs no migration, and routes to the destination", p.Report.Warnings)
	}
	found := false
	for _, tv := range p.Report.Unchanged {
		if tv.Topic == "audit-native" {
			found = true
		}
	}
	if !found {
		t.Errorf("Unchanged = %v, want audit-native listed as checked and converged", p.Report.Unchanged)
	}
}

// A tracked destination-native topic whose route still points at the source is
// not reachable by its consumers through the gateway today, so the offsets their
// groups hold may be stale and cannot be trusted to carry over: refuse until the
// operator routes it to the destination.
func TestReconcileConvert_RefusesATrackedDestinationNativeTopicNotRoutedToTarget(t *testing.T) {
	targets := append([]string{"audit-native", "ledger-native"}, convergedTopics...)
	groups := GroupFacts{
		SourceGroups:  []string{"audit-app"},
		TrackedTopics: map[string][]string{"audit-app": {"orders", "payments", "audit-native", "ledger-native"}},
	}
	gw := convertGateway()
	routeToTarget(gw, "ledger-native") // routed; audit-native falls to the catch-all (msk)

	p := ReconcileConvert(convertInput(), gw, convergedTopics, targets, convergedMirrors(), false, ClusterIDs{}, groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a tracked destination-native topic the route sends to the source must refuse the conversion")
	}
	if len(p.Report.FailFast) != 1 || p.Report.FailFast[0].Topic != "audit-native" {
		t.Fatalf("FailFast = %v, want only audit-native", p.Report.FailFast)
	}
	reason := p.Report.FailFast[0].Reason
	if !strings.Contains(reason, "only on the destination") || !strings.Contains(reason, "route") {
		t.Errorf("reason = %q, want it to say the topic is destination-only and the route doesn't send it there", reason)
	}
	if len(p.Report.Warnings) != 0 {
		t.Errorf("warnings = %v, want none: this is a refusal, not a warning", p.Report.Warnings)
	}
	checked := map[string]bool{}
	for _, tv := range p.Report.Unchanged {
		checked[tv.Topic] = true
	}
	if checked["audit-native"] || !checked["ledger-native"] {
		t.Errorf("Unchanged = %v, want the routed ledger-native converged and the unrouted audit-native not", p.Report.Unchanged)
	}
}

// A destination-only tracked topic can still be a mirror on the link whose source
// topic was deleted. Only a promoted (stopped) mirror, or no mirror at all, is
// converged; a live, pending or failed one is still mid-migration.
func TestReconcileConvert_DestinationNativeTopicMirrorState(t *testing.T) {
	for _, tc := range []struct {
		name    string
		mirror  MirrorState
		refused bool
	}{
		{"not a mirror", MirrorNone, false},
		{"promoted mirror", MirrorStopped, false},
		{"active mirror", MirrorActive, true},
		{"pending promotion", MirrorPending, true},
		{"failed mirror", MirrorBad, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			targets := append([]string{"audit-native"}, convergedTopics...)
			mirrors := convergedMirrors()
			mirrors["audit-native"] = tc.mirror
			groups := GroupFacts{
				SourceGroups:  []string{"audit-app"},
				TrackedTopics: map[string][]string{"audit-app": {"orders", "payments", "audit-native"}},
			}
			gw := convertGateway()
			routeToTarget(gw, "audit-native")

			p := ReconcileConvert(convertInput(), gw, convergedTopics, targets, mirrors, false, ClusterIDs{}, groups)

			if p.Report.Refused() != tc.refused {
				t.Fatalf("refused = %v, want %v (report %+v)", p.Report.Refused(), tc.refused, p.Report)
			}
			if tc.refused {
				if len(p.Report.FailFast) != 1 || p.Report.FailFast[0].Topic != "audit-native" ||
					!strings.Contains(p.Report.FailFast[0].Reason, "mirror") {
					t.Errorf("FailFast = %v, want audit-native refused with a reason naming the mirror", p.Report.FailFast)
				}
			}
		})
	}
}

// A topic tracked by two groups is one topic: checked once, reported once.
func TestReconcileConvert_ATopicTrackedByTwoGroupsIsCheckedOnce(t *testing.T) {
	groups := GroupFacts{
		SourceGroups: []string{"orders-app", "audit-app"},
		TrackedTopics: map[string][]string{
			"orders-app": {"orders", "payments"},
			"audit-app":  {"orders"},
		},
	}
	p := reconcileConverged(convertGateway(), groups)

	if p.Report.Refused() {
		t.Fatalf("got %+v, want a clean plan", p.Report)
	}
	count := map[string]int{}
	for _, tv := range p.Report.Unchanged {
		count[tv.Topic]++
	}
	if count["orders"] != 1 || count["payments"] != 1 || len(p.Report.Unchanged) != 2 {
		t.Errorf("Unchanged topic counts = %v, want orders and payments once each", count)
	}
}

func TestJoinCapped(t *testing.T) {
	names := func(n int) []string {
		var out []string
		for i := 1; i <= n; i++ {
			out = append(out, fmt.Sprintf("t%02d", i))
		}
		return out
	}
	for _, tc := range []struct {
		n    int
		want string
	}{
		{0, ""},
		{1, "t01"},
		{20, strings.Join(names(20), ", ")}, // exactly the cap: everything shown, no tail
		{21, strings.Join(names(20), ", ") + " and 1 more"},
		{25, strings.Join(names(20), ", ") + " and 5 more"},
	} {
		if got := joinCapped(names(tc.n), 20); got != tc.want {
			t.Errorf("joinCapped(%d names, 20) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestReconcileConvert_RefusesWhenTheSourceGroupListingIsIncomplete(t *testing.T) {
	groups := trackedGroups()
	groups.SourceListingIncomplete = "the source credential lacks DESCRIBE on the cluster"

	p := reconcileConverged(convertGateway(), groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a conversion whose source group listing may be partial must refuse: a hidden group's topics are never verified")
	}
	pc := convertPrecondition(t, p.Report, SourceGroupVisibilityCheckName)
	if pc.OK || !strings.Contains(pc.Detail, "lacks DESCRIBE on the cluster") {
		t.Errorf("visibility precondition = %+v, want a failure carrying the reason", pc)
	}
}

func TestReconcileConvert_RefusesWhenTheDestinationGroupListingIsIncomplete(t *testing.T) {
	groups := trackedGroups()
	groups.TargetListingIncomplete = "the destination credential lacks DESCRIBE on the cluster"

	p := reconcileConverged(convertGateway(), groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a destination group listing that may be partial must refuse: a hidden active group is a missed split-brain")
	}
	pc := convertPrecondition(t, p.Report, TargetGroupVisibilityCheckName)
	if pc.OK || !strings.Contains(pc.Detail, "destination credential lacks DESCRIBE") {
		t.Errorf("visibility precondition = %+v, want a failure carrying the reason", pc)
	}
	if src := convertPrecondition(t, p.Report, SourceGroupVisibilityCheckName); !src.OK {
		t.Errorf("source visibility = %+v, want it unaffected by the destination", src)
	}
}

func TestReconcileConvert_CompleteGroupListingsPass(t *testing.T) {
	p := reconcileConverged(convertGateway(), trackedGroups())

	for _, name := range []string{SourceGroupVisibilityCheckName, TargetGroupVisibilityCheckName} {
		if pc := convertPrecondition(t, p.Report, name); !pc.OK {
			t.Errorf("%s = %+v, want a pass", name, pc)
		}
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
