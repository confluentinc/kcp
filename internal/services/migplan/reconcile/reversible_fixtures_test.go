package reconcile

const starFilter = `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]}]}`

var reversibleBootstrapIDs = map[string]string{"cp": "cp-plain", "cc": "cc-sasl"}

// reversibleGateway is a static route migration-route bound to bound, with
// domains cp and cc declared (one bootstrap server id each) and auth staged
// for both. The route map is shared by RawObj and Route.Raw, as findRoute
// leaves it.
func reversibleGateway(bound string, fenced bool) *GatewayConfig {
	staged := func(secret string) map[string]any {
		return map[string]any{
			"secretStore":    "vault",
			"authentication": map[string]any{"sasl": map[string]any{"secretRef": secret}},
		}
	}
	route := map[string]any{
		"name":            "migration-route",
		"streamingDomain": map[string]any{"name": bound, "bootstrapServerId": reversibleBootstrapIDs[bound]},
		"security": map[string]any{"cluster": map[string]any{
			"cp": staged("cp-secret"),
			"cc": staged("cc-secret"),
		}},
	}
	if fenced {
		route["fence"] = map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}
	}
	domain := func(name string) map[string]any {
		return map[string]any{
			"name":         name,
			"kafkaCluster": map[string]any{"bootstrapServers": []any{map[string]any{"id": reversibleBootstrapIDs[name]}}},
		}
	}
	obj := map[string]any{"spec": map[string]any{
		"streamingDomains": []any{domain("cp"), domain("cc")},
		"routes":           []any{route},
	}}
	return &GatewayConfig{
		Route:  &RouteConfig{Name: "migration-route", Mode: RouteModeStatic, BoundDomains: []string{bound}, Raw: route},
		RawObj: obj,
	}
}

func reversibleInput(writableOn string) ReversibleInput {
	return ReversibleInput{
		Topics:          []string{"a", "b"},
		Route:           "migration-route",
		LinkName:        "rev-link",
		SourceDomain:    "cp",
		TargetDomain:    "cc",
		WritableOn:      writableOn,
		SourceClusterID: "src-id",
		TargetClusterID: "tgt-id",
	}
}

func healthyCopy(remote string, rows map[string]MirrorRow) LinkCopy {
	if rows == nil {
		rows = map[string]MirrorRow{}
	}
	return LinkCopy{
		Found:             true,
		LinkID:            "link-1",
		RemoteClusterID:   remote,
		LinkError:         "NO_ERROR",
		Mode:              "BIDIRECTIONAL",
		OffsetSyncEnabled: true,
		GroupFilters:      starFilter,
		Rows:              rows,
	}
}

func row(s RowStatus, raw string) MirrorRow { return MirrorRow{Status: s, Raw: raw} }

var (
	active             = row(RowActive, "ACTIVE")
	stopped            = row(RowStopped, "STOPPED")
	pendingSynchronize = row(RowPendingSynchronize, "PENDING_SYNCHRONIZE")
	pendingMirror      = row(RowPendingMirror, "PENDING_MIRROR")
	pendingStopped     = row(RowPendingStopped, "PENDING_STOPPED")
)

// neverReversedFacts is a link created source to target and never reversed:
// the target copy mirrors a and b, the source copy has no rows, and the route
// is bound to the source domain, unfenced.
func neverReversedFacts() ReversibleFacts {
	return ReversibleFacts{
		Gateway:             reversibleGateway("cp", false),
		SourceTopics:        []string{"a", "b"},
		TargetTopics:        []string{"a", "b"},
		LiveSourceClusterID: "src-id",
		LiveTargetClusterID: "tgt-id",
		SourceLink:          healthyCopy("tgt-id", nil),
		TargetLink:          healthyCopy("src-id", map[string]MirrorRow{"a": active, "b": active}),
	}
}

func checkNames(res []PreconditionResult) []string {
	names := make([]string, len(res))
	for i, r := range res {
		names[i] = r.Name
	}
	return names
}

func findCheck(res []PreconditionResult, name string) (PreconditionResult, bool) {
	for _, r := range res {
		if r.Name == name {
			return r, true
		}
	}
	return PreconditionResult{}, false
}
