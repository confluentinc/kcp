package plan

// OQMeta describes how one Open-Question ID should render: its
// priority for sort-order, its base severity prefix, and an optional
// promoted severity + title when a sibling OQ co-fires.
//
// Putting these three concerns in one struct collapses what was
// previously 5 parallel structures keyed by OQ ID (the detector
// emitting the ID, an iota priority constant, a priority-lookup
// switch, a severity-lookup switch, and a sibling-aware promotion
// override). New OQ = one new entry in `oqRegistry`.
type OQMeta struct {
	// Priority controls the Actions Needed sort order — lower
	// priority renders first. IDs missing from the registry fall
	// through to PriorityUnknown and sort last. (Actions Needed's
	// section number is computed dynamically by RenderMarkdown
	// based on which sections fired; the registry only orders the
	// items within it.)
	Priority int

	// Severity is the 🔴 / 🟡 / 🟢 prefix when no promotion applies.
	// Classes:
	//   🔴 blocker — fix before cutover
	//   🟡 affects accuracy — fix before relying on the plan
	//   🟢 preference — pick one
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
	// "preference"-shaped titles being promoted to blocker (a 🟢→🔴
	// promotion), keeping the original title creates a contradictory
	// signal ("pick" + 🔴), so promotion entries should always
	// supply a replacement.
	PromotedTitle string
}

// PriorityUnknown is the sort-last fallback. Anchor at a high value
// so adding a new low-priority OQ doesn't accidentally outrank it.
const PriorityUnknown = 1000

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
var oqRegistry = map[string]OQMeta{
	// Networking
	"networking_privatelink_over_cap": {
		Priority: 310,
		Severity: "🔴",
	},

	// Cutover
	"downtime_tolerance_requires_gateway": {
		Priority: 410,
		Severity: "🔴",
	},
	"downtime_tolerance_unknown": {
		Priority: 420,
		Severity: "🔴",
	},
	"gateway_prereqs_pending": {
		Priority:         430,
		Severity:         "🟢",
		PromoteWhen:      []string{"auth_target_gateway_incompatible"},
		PromotedSeverity: "🔴",
		PromotedTitle:    "Gateway prereqs — moot until the auth conflict above is resolved",
	},
	"gateway_intent_unconfirmed": {
		Priority:         440,
		Severity:         "🟢",
		PromoteWhen:      []string{"auth_target_gateway_incompatible"},
		PromotedSeverity: "🔴",
		PromotedTitle:    "Gateway intent — moot until the auth conflict above is resolved",
	},

	// Auth
	"target_auth_method_unknown": {
		Priority: 510,
		Severity: "🔴",
	},
	"auth_target_gateway_incompatible": {
		Priority: 520,
		Severity: "🔴",
	},
	"auth_posture_unknown": {
		Priority: 530,
		Severity: "🟡",
	},

	// Schema migration
	"schema_strategy_invalid": {
		Priority: 610,
		Severity: "🔴",
	},
	"schema_linking_ineligible": {
		Priority: 620,
		Severity: "🔴",
	},
	"schema_strategy_unknown": {
		Priority: 630,
		Severity: "🟡",
	},
	"schema_linking_eligibility_unknown": {
		Priority: 640,
		Severity: "🟡",
	},
	"schema_state_strategy_mismatch": {
		Priority: 650,
		Severity: "🟡",
	},

	// Tiered Storage
	"tiered_consumer_history_invalid": {
		Priority: 710,
		Severity: "🔴",
	},
	"tiered_historical_strategy_invalid": {
		Priority: 720,
		Severity: "🔴",
	},
	"tiered_strategy_undeclared": {
		Priority: 730,
		Severity: "🟡",
	},

	// Cost reconciliation
	"cost_data_not_collected": {
		Priority: 810,
		Severity: "🟡",
	},

	// Cross-cutting fleet / state-file signals
	"state_file_stale": {
		Priority: 910,
		Severity: "🟡",
	},
	"missing_p95_metrics": {
		Priority: 920,
		Severity: "🟡",
	},
	"broker_inventory_empty": {
		Priority: 930,
		Severity: "🟡",
	},
	"topic_inventory_empty": {
		Priority: 940,
		Severity: "🟡",
	},
	"acls_not_scanned": {
		Priority: 950,
		Severity: "🟡",
	},
}

// oqMetaFor returns the registry entry for an OQ ID, or a sentinel
// (Priority: PriorityUnknown, Severity: 🟡) when the ID is unknown.
// 🟡 is the safe default for accuracy-class signals; an unknown ID
// indicates a missed registry entry — surfacing it as 🟡 keeps it
// visible without falsely claiming blocker status.
func oqMetaFor(id string) OQMeta {
	if m, ok := oqRegistry[id]; ok {
		return m
	}
	return OQMeta{Priority: PriorityUnknown, Severity: "🟡"}
}

// promoteSeverity applies the sibling-aware promotion rule: if any
// of `meta.PromoteWhen` is in `siblings`, returns the promoted
// severity + title; otherwise returns the base severity + the
// original title.
func promoteSeverity(meta OQMeta, originalTitle string, siblings map[string]bool) (severity, title string) {
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
