package reconcile

import (
	"reflect"
	"strings"
	"testing"
)

var reversibleRouteCheckNames = []string{
	"route exists",
	"route is static",
	"route is bound to one of the two declared domains",
	"route has no fence kcp didn't write",
	"both domains are declared with exactly one bootstrap server id",
	"route carries pre-staged auth for both domains",
	"staged auth secrets exist",
}

var reversibleLinkCheckNames = []string{
	"cluster link exists on both clusters",
	"target cluster matches the manifest",
	"source cluster matches the manifest",
	"each link copy points at the other cluster",
	"both link copies are one link",
	"cluster link is healthy on both copies",
	"cluster link is bidirectional on both copies",
	"cluster link has no prefix on either copy",
	"consumer offset sync is on for both copies",
	"consumer offset group filter is the single * LOCAL_MIRROR entry on both copies",
}

func routeChecks(t *testing.T, gw *GatewayConfig, mutate func(f *ReversibleFacts)) []PreconditionResult {
	t.Helper()
	f := neverReversedFacts()
	f.Gateway = gw
	if mutate != nil {
		mutate(&f)
	}
	return checkReversibleRoute(reversibleInput("cc"), f)
}

func requireFailed(t *testing.T, res []PreconditionResult, name string, wantInDetail ...string) PreconditionResult {
	t.Helper()
	r, ok := findCheck(res, name)
	if !ok || r.OK {
		t.Fatalf("want a failed %q, got %+v", name, res)
	}
	for _, w := range wantInDetail {
		if !strings.Contains(r.Detail, w) {
			t.Errorf("%q detail = %q, want it to contain %q", name, r.Detail, w)
		}
	}
	return r
}

func requireAllOK(t *testing.T, res []PreconditionResult) {
	t.Helper()
	for _, r := range res {
		if !r.OK {
			t.Errorf("%q failed: %s", r.Name, r.Detail)
		}
	}
}

func TestReversibleRouteChecks_HappyPath(t *testing.T) {
	res := routeChecks(t, reversibleGateway("cp", false), nil)
	requireAllOK(t, res)
	if got := checkNames(res); !reflect.DeepEqual(got, reversibleRouteCheckNames) {
		t.Errorf("names =\n%q\nwant\n%q", got, reversibleRouteCheckNames)
	}
}

func TestReversibleRouteChecks_RouteMissing(t *testing.T) {
	gw := reversibleGateway("cp", false)
	gw.Route.Name = "other-route"
	res := routeChecks(t, gw, nil)
	requireFailed(t, res, "route exists", `"migration-route"`)
	if len(res) != 1 {
		t.Errorf("a missing route leaves nothing else to check, got %+v", res)
	}
}

func TestReversibleRouteChecks_RouteDynamic(t *testing.T) {
	gw := reversibleGateway("cp", false)
	gw.Route.Mode = RouteModeDynamic
	requireFailed(t, routeChecks(t, gw, nil), "route is static", "reversibleStaticMigration")
}

func TestReversibleRouteChecks_BoundToThirdDomain(t *testing.T) {
	requireFailed(t, routeChecks(t, reversibleGateway("gcp", false), nil),
		"route is bound to one of the two declared domains", `"gcp"`, `"cp"`, `"cc"`)
}

func TestReversibleRouteChecks_ForeignFence(t *testing.T) {
	gw := reversibleGateway("cp", false)
	gw.Route.Raw["fence"] = map[string]any{"scope": "ALL", "errorCode": "UNKNOWN_SERVER_ERROR"}
	requireFailed(t, routeChecks(t, gw, nil), staticFenceCheckName, "UNKNOWN_SERVER_ERROR")
}

func TestReversibleRouteChecks_KcpFenceAccepted(t *testing.T) {
	requireAllOK(t, routeChecks(t, reversibleGateway("cp", true), nil))
}

func TestReversibleRouteChecks_DomainWithTwoBootstrapIDs(t *testing.T) {
	gw := reversibleGateway("cp", false)
	cc := gw.RawObj["spec"].(map[string]any)["streamingDomains"].([]any)[1].(map[string]any)
	cc["kafkaCluster"].(map[string]any)["bootstrapServers"] = []any{map[string]any{"id": "cc-sasl"}, map[string]any{"id": "cc-plain"}}
	requireFailed(t, routeChecks(t, gw, nil), "both domains are declared with exactly one bootstrap server id", `"cc"`, "2 bootstrap server ids")
}

func TestReversibleRouteChecks_UndeclaredDomain(t *testing.T) {
	gw := reversibleGateway("cp", false)
	spec := gw.RawObj["spec"].(map[string]any)
	spec["streamingDomains"] = spec["streamingDomains"].([]any)[:1]
	requireFailed(t, routeChecks(t, gw, nil), "both domains are declared with exactly one bootstrap server id", `"cc"`, "not declared")
}

func TestReversibleRouteChecks_MissingStagedAuthForSourceDomain(t *testing.T) {
	gw := reversibleGateway("cp", false)
	delete(gw.Route.Raw["security"].(map[string]any)["cluster"].(map[string]any), "cp")
	r := requireFailed(t, routeChecks(t, gw, nil), "route carries pre-staged auth for both domains", "security.cluster.cp")
	if strings.Contains(r.Detail, "security.cluster.cc") {
		t.Errorf("detail = %q, want only the missing domain named", r.Detail)
	}
}

func TestReversibleRouteChecks_SecretsMissing(t *testing.T) {
	res := routeChecks(t, reversibleGateway("cp", false), func(f *ReversibleFacts) { f.MissingSecrets = []string{"cp-secret"} })
	requireFailed(t, res, "staged auth secrets exist", "cp-secret")
}

func TestReversibleRouteChecks_SecretCheckSkippedIsSkip(t *testing.T) {
	res := routeChecks(t, reversibleGateway("cp", false), func(f *ReversibleFacts) { f.SecretCheckSkipped = "secrets: forbidden" })
	r, _ := findCheck(res, "staged auth secrets exist")
	if !r.OK || !r.Skipped || r.Detail != "secrets: forbidden" {
		t.Errorf("got %+v, want a skip carrying the reason", r)
	}
}

func linkChecks(mutate func(f *ReversibleFacts)) []PreconditionResult {
	f := neverReversedFacts()
	if mutate != nil {
		mutate(&f)
	}
	return checkReversibleLink(reversibleInput("cc"), f)
}

func TestReversibleLinkChecks_HappyPath(t *testing.T) {
	res := linkChecks(nil)
	requireAllOK(t, res)
	if got := checkNames(res); !reflect.DeepEqual(got, reversibleLinkCheckNames) {
		t.Errorf("names =\n%q\nwant\n%q", got, reversibleLinkCheckNames)
	}
}

func TestReversibleLinkChecks_SourceCopyNeverCreated(t *testing.T) {
	res := linkChecks(func(f *ReversibleFacts) { f.SourceLink = LinkCopy{} })
	requireFailed(t, res, "cluster link exists on both clusters", `"rev-link"`, "was never created on the source cluster")
	for _, name := range reversibleLinkCheckNames[3:] {
		if _, ok := findCheck(res, name); ok {
			t.Errorf("%q must not run without both copies", name)
		}
	}
}

func TestReversibleLinkChecks_TargetIDMismatch(t *testing.T) {
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.LiveTargetClusterID = "other" }),
		"target cluster matches the manifest", `"tgt-id"`, `"other"`)
}

func TestReversibleLinkChecks_EmptyLiveIDFailsClosed(t *testing.T) {
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.LiveSourceClusterID = "" }), "source cluster matches the manifest")
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.TargetLink.RemoteClusterID = "" }), "each link copy points at the other cluster", "target copy")
}

func TestReversibleLinkChecks_RemoteIDsCrossed(t *testing.T) {
	res := linkChecks(func(f *ReversibleFacts) {
		f.SourceLink.RemoteClusterID, f.TargetLink.RemoteClusterID = "src-id", "tgt-id"
	})
	requireFailed(t, res, "each link copy points at the other cluster", "source copy", "target copy")
}

func TestReversibleLinkChecks_LinkIDsDiffer(t *testing.T) {
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.SourceLink.LinkID = "link-2" }),
		"both link copies are one link", `"link-2"`, `"link-1"`)
}

func TestReversibleLinkChecks_UnhealthyCopyNamed(t *testing.T) {
	r := requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.SourceLink.LinkError = "SOURCE_UNREACHABLE" }),
		"cluster link is healthy on both copies", "source copy", "SOURCE_UNREACHABLE")
	if strings.Contains(r.Detail, "target copy") {
		t.Errorf("detail = %q names the healthy copy", r.Detail)
	}
}

func TestReversibleLinkChecks_EmptyLinkErrorIsHealthy(t *testing.T) {
	res := linkChecks(func(f *ReversibleFacts) { f.SourceLink.LinkError, f.TargetLink.LinkError = "", "" })
	if r, _ := findCheck(res, "cluster link is healthy on both copies"); !r.OK {
		t.Errorf("an omitted link_error is healthy, got %+v", r)
	}
}

func TestReversibleLinkChecks_OneCopyNotBidirectional(t *testing.T) {
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.TargetLink.Mode = "DESTINATION" }),
		"cluster link is bidirectional on both copies", "target copy", "DESTINATION")
}

// A copy whose configs don't report link.mode can't be shown to be
// bidirectional, so this path refuses it.
func TestReversibleLinkChecks_AbsentModeRefused(t *testing.T) {
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.SourceLink.Mode = "" }),
		"cluster link is bidirectional on both copies", "source copy")
}

func TestReversibleLinkChecks_PrefixSet(t *testing.T) {
	requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.TargetLink.Prefix = "src." }),
		"cluster link has no prefix on either copy", "target copy", `"src."`)
}

func TestReversibleLinkChecks_OffsetSyncOffOnOneCopy(t *testing.T) {
	r := requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.SourceLink.OffsetSyncEnabled = false }),
		"consumer offset sync is on for both copies", "source copy")
	if strings.Contains(r.Detail, "target copy") {
		t.Errorf("detail = %q names the copy with sync on", r.Detail)
	}
}

func TestReversibleLinkChecks_FilterDetailNamesCopyAndFix(t *testing.T) {
	r := requireFailed(t, linkChecks(func(f *ReversibleFacts) { f.TargetLink.GroupFilters = "" }),
		"consumer offset group filter is the single * LOCAL_MIRROR entry on both copies", "target copy", "is empty")
	const fix = `set consumer.offset.group.filters to {"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]}]} on both copies (configs:alter; takes effect without a link restart)`
	if !strings.HasSuffix(r.Detail, fix) {
		t.Errorf("detail = %q, want it to end with the fix", r.Detail)
	}
}

func TestGroupFilterProblem(t *testing.T) {
	for _, tc := range []struct {
		name, raw, want string // want "" means accepted
	}{
		{"exact star entry", starFilter, ""},
		{"reordered keys and whitespace", ` { "groupFilters" : [ { "topicTypes" : [ "LOCAL_MIRROR" ], "filterType" : "INCLUDE", "patternType" : "LITERAL", "name" : "*" } ] } `, ""},
		{"lower-case enums", `{"groupFilters":[{"name":"*","patternType":"literal","filterType":"include","topicTypes":["local_mirror"]}]}`, ""},
		{"unset", "", "is empty"},
		{"no entries", `{"groupFilters":[]}`, "is empty"},
		{"star without topicTypes", `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE"}]}`, "without topicTypes"},
		{"remote mirror too", `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR","REMOTE_MIRROR"]}]}`, "REMOTE_MIRROR"},
		{"prefixed entry", `{"groupFilters":[{"name":"orders-","patternType":"PREFIXED","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]}]}`, "only the * filter"},
		{"star plus exclusion", `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]},{"name":"tmp-","patternType":"PREFIXED","filterType":"EXCLUDE"}]}`, "only the * filter"},
		{"not json", "not json", "is not valid JSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := groupFilterProblem(tc.raw)
			switch {
			case tc.want == "" && got != "":
				t.Errorf("want accepted, got %q", got)
			case tc.want != "" && !strings.Contains(got, tc.want):
				t.Errorf("got %q, want it to contain %q", got, tc.want)
			}
		})
	}
}
