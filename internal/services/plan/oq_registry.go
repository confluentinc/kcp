package plan

// oqMeta describes how one Open-Question ID should render: its
// priority for sort-order, its base severity prefix, and an optional
// promoted severity + title when a sibling OQ co-fires.
//
// Putting these three concerns in one struct collapses what was
// previously 5 parallel structures keyed by OQ ID (the detector
// emitting the ID, an iota priority constant, a priority-lookup
// switch, a severity-lookup switch, and a sibling-aware promotion
// override). New OQ = one new entry in `oqRegistry`.
type oqMeta struct {
	// Priority controls the Actions Needed sort order — lower
	// priority renders first. IDs missing from the registry fall
	// through to priorityUnknown and sort last. (Actions Needed's
	// section number is computed dynamically by RenderMarkdown
	// based on which sections fired; the registry only orders the
	// items within it.)
	Priority int

	// Severity is the [HIGH] / [MED] / [LOW] label prefix when no
	// promotion applies. Classes:
	//   [HIGH] blocker — fix before cutover
	//   [MED]  affects accuracy — fix before relying on the plan
	//   [LOW]  preference — pick one
	//
	// These are provisional text tokens (chosen to replace the previous
	// emoji prefixes) — reword them here and in render_markdown.go's
	// severityLegend together if the report vocabulary changes.
	Severity string

	// PromoteWhen lists sibling OQ IDs that, when also present on
	// the same Plan, promote this OQ's severity (and optionally
	// rewrite its title). Empty = no promotion ever fires.
	PromoteWhen []string

	// PromotedSeverity replaces Severity when any PromoteWhen sibling
	// is present. Empty falls back to Severity.
	PromotedSeverity string

	// PromotedTitle replaces the OQ's rendered title when promoted.
	// Empty falls back to the OQ's natural Title field — but for
	// "preference"-shaped titles being promoted to blocker (a [LOW]→[HIGH]
	// promotion), keeping the original title creates a contradictory
	// signal ("pick" + [HIGH]), so promotion entries should always
	// supply a replacement.
	PromotedTitle string
}

// priorityUnknown is the sort-last fallback. Anchor at a high value
// so adding a new low-priority OQ doesn't accidentally outrank it.
const priorityUnknown = 1000

// oqRegistry holds every OQ ID emitted by the detectors. Priority is
// sparse + grouped by concern so new IDs land between siblings
// without renumbering. The concern bands are:
//
//	100s  — Sizing
//	200s  — Cluster Type
//	300s  — Networking
//	400s  — Cutover
//	500s  — Auth
//	600s  — Schema
//	700s  — Tiered Storage
//	800s  — Cost Reconciliation
//	900s  — Fleet / state-file (cross-cutting)
//
// Within a band, 10-unit spacing leaves room for an inserted OQ to
// slot between existing entries without a shuffle.
var oqRegistry = map[string]oqMeta{
	// Networking
	"networking_privatelink_over_cap": {
		Priority: 310,
		Severity: "[HIGH]",
	},

	// Cutover
	"downtime_tolerance_requires_gateway": {
		Priority: 410,
		Severity: "[HIGH]",
	},
	"downtime_tolerance_unknown": {
		Priority: 420,
		Severity: "[HIGH]",
	},
	"sub_pattern_unknown": {
		Priority: 425,
		Severity: "[MED]",
	},
	"gateway_prereqs_pending": {
		Priority:         430,
		Severity:         "[LOW]",
		PromoteWhen:      []string{"auth_target_gateway_incompatible"},
		PromotedSeverity: "[HIGH]",
		PromotedTitle:    "Gateway prereqs — moot until the auth conflict above is resolved",
	},
	"gateway_intent_unconfirmed": {
		Priority:         440,
		Severity:         "[LOW]",
		PromoteWhen:      []string{"auth_target_gateway_incompatible"},
		PromotedSeverity: "[HIGH]",
		PromotedTitle:    "Gateway intent — moot until the auth conflict above is resolved",
	},

	// Auth
	"target_auth_method_unknown": {
		Priority: 510,
		Severity: "[HIGH]",
	},
	"auth_target_gateway_incompatible": {
		Priority: 520,
		Severity: "[HIGH]",
	},
	"auth_posture_unknown": {
		Priority: 530,
		Severity: "[MED]",
	},

	// Schema migration
	"schema_strategy_invalid": {
		Priority: 610,
		Severity: "[HIGH]",
	},
	"schema_linking_ineligible": {
		Priority: 620,
		Severity: "[HIGH]",
	},
	"schema_strategy_unknown": {
		Priority: 630,
		Severity: "[MED]",
	},
	"schema_linking_eligibility_unknown": {
		Priority: 640,
		Severity: "[MED]",
	},
	"schema_state_strategy_mismatch": {
		Priority: 650,
		Severity: "[MED]",
	},

	// Tiered Storage
	"tiered_consumer_history_invalid": {
		Priority: 710,
		Severity: "[HIGH]",
	},
	"tiered_historical_strategy_invalid": {
		Priority: 720,
		Severity: "[HIGH]",
	},
	"tiered_strategy_undeclared": {
		Priority: 730,
		Severity: "[MED]",
	},

	// Cost reconciliation
	"cost_data_not_collected": {
		Priority: 810,
		Severity: "[MED]",
	},

	// Cross-cutting fleet / state-file signals
	"state_file_stale": {
		Priority: 910,
		Severity: "[MED]",
	},
	"missing_p95_metrics": {
		Priority: 920,
		Severity: "[MED]",
	},
	"broker_inventory_empty": {
		Priority: 930,
		Severity: "[MED]",
	},
	"topic_inventory_empty": {
		Priority: 940,
		Severity: "[MED]",
	},
	"acls_not_scanned": {
		Priority: 950,
		Severity: "[MED]",
	},
	"cluster_override_unknown_cluster": {
		Priority: 960,
		Severity: "[MED]",
	},
	"osk_source_unsupported": {
		Priority: 970,
		Severity: "[MED]",
	},
	"cluster_type_unrecognised": {
		Priority: 980,
		Severity: "[MED]",
	},
}

// oqMetaFor returns the registry entry for an OQ ID, or a sentinel
// (Priority: priorityUnknown, Severity: [MED]) when the ID is unknown.
// [MED] is the safe default for accuracy-class signals; an unknown ID
// indicates a missed registry entry — surfacing it as [MED] keeps it
// visible without falsely claiming blocker status.
func oqMetaFor(id string) oqMeta {
	if m, ok := oqRegistry[id]; ok {
		return m
	}
	return oqMeta{Priority: priorityUnknown, Severity: "[MED]"}
}

// promoteSeverity applies the sibling-aware promotion rule: if any
// of `meta.PromoteWhen` is in `siblings`, returns the promoted
// severity + title; otherwise returns the base severity + the
// original title.
func promoteSeverity(meta oqMeta, originalTitle string, siblings map[string]bool) (severity, title string) {
	for _, trigger := range meta.PromoteWhen {
		if siblings[trigger] {
			sev := meta.PromotedSeverity
			if sev == "" {
				sev = meta.Severity
			}
			t := meta.PromotedTitle
			if t == "" {
				t = originalTitle
			}
			return sev, t
		}
	}
	return meta.Severity, originalTitle
}
