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

func convertInput() ReconcileInput {
	return ReconcileInput{Route: "migration-route", TargetDomain: "cc", ConvertTo: "static"}
}

var convergedTopics = []string{"orders", "payments"}

// trackedGroups is one source group with committed offsets on both link topics.
func trackedGroups() GroupFacts {
	return GroupFacts{TrackedTopics: map[string][]string{"orders-app": {"orders", "payments"}}}
}

// linkMirrors is the finished link: both topics promoted, unprefixed.
func linkMirrors() []LinkMirror {
	return []LinkMirror{
		{SourceTopic: "orders", MirrorTopic: "orders", State: MirrorStopped, Status: "STOPPED"},
		{SourceTopic: "payments", MirrorTopic: "payments", State: MirrorStopped, Status: "STOPPED"},
	}
}

func convergedPartitions() PartitionCounts {
	return PartitionCounts{
		Source: map[string]int{"orders": 3, "payments": 3},
		Target: map[string]int{"orders": 3, "payments": 3},
	}
}

func reconcileConverged(gw *GatewayConfig, groups GroupFacts) *Plan {
	return ReconcileConvert(convertInput(), gw, convergedTopics, convergedTopics, linkMirrors(), convergedPartitions(), false, ClusterIDs{}, groups)
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

func TestReconcileConvert_UntrackedConvergedTopicIsSilent(t *testing.T) {
	// A converged topic nobody commits on (e.g. _schemas) adds no warning.
	p := reconcileConverged(convertGateway(), GroupFacts{})
	if p.Report.Refused() || len(p.Report.Warnings) != 0 {
		t.Fatalf("got %+v, want a clean plan", p.Report)
	}
}

// A link topic two groups commit on is one topic: checked once, reported once.
func TestReconcileConvert_ALinkTopicTwoGroupsCommitOnIsReportedOnce(t *testing.T) {
	groups := GroupFacts{TrackedTopics: map[string][]string{
		"orders-app": {"orders", "payments"},
		"audit-app":  {"orders"},
	}}
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
	groups.SourceListingIncomplete = "the source credential cannot describe arbitrary consumer groups"

	p := reconcileConverged(convertGateway(), groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a conversion whose source group listing may be partial must refuse: a hidden group's topics are never verified")
	}
	pc := convertPrecondition(t, p.Report, SourceGroupVisibilityCheckName)
	if pc.OK || !strings.Contains(pc.Detail, "cannot describe arbitrary consumer groups") {
		t.Errorf("visibility precondition = %+v, want a failure carrying the reason", pc)
	}
}

func TestReconcileConvert_RefusesWhenTheDestinationGroupListingIsIncomplete(t *testing.T) {
	groups := trackedGroups()
	groups.TargetListingIncomplete = "the destination credential cannot describe arbitrary consumer groups"

	p := reconcileConverged(convertGateway(), groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a destination group listing that may be partial must refuse: a hidden active group is a missed split-brain")
	}
	pc := convertPrecondition(t, p.Report, TargetGroupVisibilityCheckName)
	if pc.OK || !strings.Contains(pc.Detail, "destination credential cannot describe arbitrary consumer groups") {
		t.Errorf("visibility precondition = %+v, want a failure carrying the reason", pc)
	}
	if src := convertPrecondition(t, p.Report, SourceGroupVisibilityCheckName); !src.OK {
		t.Errorf("source visibility = %+v, want it unaffected by the destination", src)
	}
}

func TestReconcileConvert_RefusesWhenACredentialCannotDescribeEveryTopic(t *testing.T) {
	for _, tc := range []struct {
		side   string
		set    func(*GroupFacts, string)
		name   string
		others []string // the other visibility preconditions, which must stay unaffected
	}{
		{"source", func(g *GroupFacts, r string) { g.SourceTopicsIncomplete = r }, SourceTopicVisibilityCheckName,
			[]string{SourceGroupVisibilityCheckName, TargetGroupVisibilityCheckName, TargetTopicVisibilityCheckName}},
		{"destination", func(g *GroupFacts, r string) { g.TargetTopicsIncomplete = r }, TargetTopicVisibilityCheckName,
			[]string{SourceGroupVisibilityCheckName, TargetGroupVisibilityCheckName, SourceTopicVisibilityCheckName}},
	} {
		t.Run(tc.side, func(t *testing.T) {
			groups := trackedGroups()
			tc.set(&groups, "the "+tc.side+" credential cannot describe arbitrary topics")

			p := reconcileConverged(convertGateway(), groups)

			if !p.Report.Refused() || p.Artifacts != nil {
				t.Fatal("a credential that may not describe every topic silently loses topics from the offset fetch: refuse")
			}
			pc := convertPrecondition(t, p.Report, tc.name)
			if pc.OK || !strings.Contains(pc.Detail, "cannot describe arbitrary topics") {
				t.Errorf("%s = %+v, want a failure carrying the reason", tc.name, pc)
			}
			for _, other := range tc.others {
				if o := convertPrecondition(t, p.Report, other); !o.OK {
					t.Errorf("%s = %+v, want it unaffected", other, o)
				}
			}
		})
	}
}

func TestReconcileConvert_CompleteTopicVisibilityPasses(t *testing.T) {
	p := reconcileConverged(convertGateway(), trackedGroups())

	for _, name := range []string{SourceTopicVisibilityCheckName, TargetTopicVisibilityCheckName} {
		if pc := convertPrecondition(t, p.Report, name); !pc.OK {
			t.Errorf("%s = %+v, want a pass", name, pc)
		}
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
	groups := trackedGroups()
	groups.TargetStates = map[string]string{"orders-app": "Stable"}

	p := reconcileConverged(convertGateway(), groups)

	if !p.Report.Refused() {
		t.Fatal("an in-scope group active on the destination must refuse")
	}
	if pc := convertPrecondition(t, p.Report, GroupSplitBrainCheckName); pc.OK {
		t.Errorf("split-brain precondition = %+v, want a failure", pc)
	}
}

func TestReconcileConvert_IdleDestinationGroupWarns(t *testing.T) {
	groups := trackedGroups()
	groups.TargetStates = map[string]string{"orders-app": "Empty"}

	p := reconcileConverged(convertGateway(), groups)

	if p.Report.Refused() {
		t.Fatalf("an Empty destination group must not refuse, got %+v", p.Report)
	}
	if len(p.Report.Warnings) != 1 || !strings.Contains(p.Report.Warnings[0], "orders-app") {
		t.Errorf("warnings = %v, want one naming orders-app", p.Report.Warnings)
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

			p := ReconcileConvert(in, convertGateway(), convergedTopics, convergedTopics, linkMirrors(), convergedPartitions(), false, ClusterIDs{}, GroupFacts{})

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
	if p := ReconcileConvert(in, gw, convergedTopics, convergedTopics, linkMirrors(), convergedPartitions(), false, ClusterIDs{}, GroupFacts{}); !p.Report.Refused() || p.NothingToDo {
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

func TestReconcileConvert_RefusesALinkTopicThatIsNotPromoted(t *testing.T) {
	mirrors := linkMirrors()
	mirrors[0].State, mirrors[0].Status = MirrorActive, "ACTIVE"

	p := ReconcileConvert(convertInput(), convertGateway(), convergedTopics, convergedTopics, mirrors, convergedPartitions(), false, ClusterIDs{}, trackedGroups())

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("an unpromoted link topic must refuse the conversion")
	}
	if len(p.Report.FailFast) != 1 || p.Report.FailFast[0].Topic != "orders" {
		t.Fatalf("FailFast = %v, want orders", p.Report.FailFast)
	}
	for _, pc := range p.Report.Preconditions {
		if pc.Name == GroupScopeCheckName || pc.Name == GroupSplitBrainCheckName {
			t.Errorf("precondition %q ran, want the group stages skipped after a link topic refusal", pc.Name)
		}
	}
}

func TestReconcileConvert_RefusesAGroupCommittingOutsideTheLink(t *testing.T) {
	topics := append([]string{"refunds"}, convergedTopics...)
	groups := trackedGroups()
	groups.TrackedTopics["refunds-app"] = []string{"refunds"}

	p := ReconcileConvert(convertInput(), convertGateway(), topics, convergedTopics, linkMirrors(), convergedPartitions(), false, ClusterIDs{}, groups)

	if !p.Report.Refused() || p.Artifacts != nil {
		t.Fatal("a group committing outside the link must refuse the conversion")
	}
	if pc := convertPrecondition(t, p.Report, GroupScopeCheckName); pc.OK || !strings.Contains(pc.Detail, "refunds-app (refunds)") {
		t.Errorf("group rule = %+v, want a failure naming refunds-app and refunds", pc)
	}
}

func TestReconcileConvert_UntrackedTopicOffTheLinkWarnsOnly(t *testing.T) {
	topics := append([]string{"_schemas"}, convergedTopics...)

	p := ReconcileConvert(convertInput(), convertGateway(), topics, convergedTopics, linkMirrors(), convergedPartitions(), false, ClusterIDs{}, trackedGroups())

	if p.Report.Refused() || p.Artifacts == nil {
		t.Fatalf("an untracked topic off the link must not refuse, got %+v", p.Report)
	}
	if len(p.Report.Warnings) != 1 || !strings.Contains(p.Report.Warnings[0], "_schemas") {
		t.Fatalf("warnings = %v, want one naming _schemas", p.Report.Warnings)
	}
}

func TestReconcileConvert_VisibilityRefusalStopsBeforeTheLinkChecks(t *testing.T) {
	mirrors := linkMirrors()
	mirrors[0].State = MirrorActive // would refuse, if the link checks ran
	groups := trackedGroups()
	groups.SourceTopicsIncomplete = "the source credential cannot describe arbitrary topics"

	p := ReconcileConvert(convertInput(), convertGateway(), convergedTopics, convergedTopics, mirrors, convergedPartitions(), false, ClusterIDs{}, groups)

	if !p.Report.Refused() {
		t.Fatal("a denied visibility probe must refuse")
	}
	if len(p.Report.FailFast) != 0 || len(p.Report.Unchanged) != 0 {
		t.Errorf("FailFast = %v, Unchanged = %v: the link checks read listings the probe says may be partial, so they must not run", p.Report.FailFast, p.Report.Unchanged)
	}
}

func TestReconcileConvert_RouteRefusalStopsBeforeVisibility(t *testing.T) {
	gw := convertGateway()
	delete(gw.Route.Raw["security"].(map[string]any)["cluster"].(map[string]any), "cc") // no auth for the target domain
	groups := trackedGroups()
	groups.SourceListingIncomplete = "would also refuse"

	p := reconcileConverged(gw, groups)

	if !p.Report.Refused() {
		t.Fatal("a route without target auth must refuse")
	}
	for _, pc := range p.Report.Preconditions {
		if pc.Name == SourceGroupVisibilityCheckName {
			t.Errorf("visibility precondition ran after a route refusal: %+v", p.Report.Preconditions)
		}
	}
}
