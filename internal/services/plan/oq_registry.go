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
	// Priority controls the §5 Actions Needed sort order — lower
	// priority renders first. IDs missing from the registry fall
	// through to PriorityUnknown and sort last.
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

// oqRegistry holds every OQ ID emitted by the detectors. Adding a
// new OQ requires exactly one entry here and one detector emit; the
// renderer and the priority sort both read from this map.
var oqRegistry = map[string]OQMeta{
	"networking_privatelink_over_cap": {
		Priority: 10,
		Severity: "🔴",
	},
	"downtime_tolerance_requires_gateway": {
		Priority: 20,
		Severity: "🔴",
	},
	"downtime_tolerance_unknown": {
		Priority: 30,
		Severity: "🔴",
	},
	"target_auth_method_unknown": {
		Priority: 40,
		Severity: "🔴",
	},
	"auth_target_gateway_incompatible": {
		Priority: 50,
		Severity: "🔴",
	},
	"gateway_prereqs_pending": {
		Priority:         60,
		Severity:         "🟢",
		PromoteWhen:      []string{"auth_target_gateway_incompatible"},
		PromotedSeverity: "🔴",
		PromotedTitle:    "Gateway prereqs — moot until the auth conflict above is resolved",
	},
	"gateway_intent_unconfirmed": {
		Priority:         70,
		Severity:         "🟢",
		PromoteWhen:      []string{"auth_target_gateway_incompatible"},
		PromotedSeverity: "🔴",
		PromotedTitle:    "Gateway intent — moot until the auth conflict above is resolved",
	},
	"state_file_stale": {
		Priority: 80,
		Severity: "🟡",
	},
	"missing_p95_metrics": {
		Priority: 90,
		Severity: "🟡",
	},
	"broker_inventory_empty": {
		Priority: 100,
		Severity: "🟡",
	},
	"topic_inventory_empty": {
		Priority: 110,
		Severity: "🟡",
	},
	"auth_posture_unknown": {
		Priority: 120,
		Severity: "🟡",
	},
	"acls_not_scanned": {
		Priority: 130,
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
