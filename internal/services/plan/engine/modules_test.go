package engine

import (
	"strings"
	"testing"
)

func TestSchema_Decisions(t *testing.T) {
	cases := []struct {
		name       string
		p          Profile
		wantValue  string
		wantAction *string // pointer compare by value; nil means "must be nil"
		checkNil   bool
	}{
		{"None+schemaless", Profile{SourceSRType: sourceSRNone, SchemaStrategy: schemaStrategySchemaless}, "Schemaless", nil, true},
		{"None+fresh", Profile{SourceSRType: sourceSRNone, SchemaStrategy: schemaStrategyFresh}, "Start with a fresh Schema Registry", strptr("Create Schema Registry"), false},
		{"None+undeclared", Profile{SourceSRType: sourceSRNone}, "Pending", nil, false},
		{"Glue+migrate", Profile{SourceSRType: sourceSRGlue, SchemaStrategy: schemaStrategyMigrate}, "Glue bulk re-registration", strptr("Migrate Glue schemas"), false},
		{"Glue+fresh", Profile{SourceSRType: sourceSRGlue, SchemaStrategy: schemaStrategyFresh}, "Start with a fresh Schema Registry", nil, false},
		{"CPE7+migrate+reachable", Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategyMigrate, SourceSROutboundReachableToCC: "Yes"}, "Schema Linking", strptr("Set up Schema Linking"), false},
		{"CPE7+migrate+unreachable", Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategyMigrate, SourceSROutboundReachableToCC: "No"}, "Replicator", strptr("Set up Replicator"), false},
		{"CPE7+migrate+gov", Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategyMigrate, SourceSROutboundReachableToCC: "Yes", TargetIsGovCloud: "Yes"}, "Replicator", nil, false},
		{"CPE7+schemaless", Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategySchemaless}, "Schemaless (mismatch)", nil, false},
		{"CPCommunity+migrate", Profile{SourceSRType: sourceSRCPCommunity, SchemaStrategy: schemaStrategyMigrate}, "Replicator", strptr("Set up Replicator"), false},
		{"Other", Profile{SourceSRType: sourceSROther}, "Special handling", nil, true},
		{"unset", Profile{}, "Pending", nil, false},
	}
	for _, c := range cases {
		v := schemaDecision(c.p)
		if v.Value != c.wantValue {
			t.Errorf("%s: value=%q, want %q", c.name, v.Value, c.wantValue)
		}
		if c.checkNil && v.Action != nil {
			t.Errorf("%s: action=%v, want nil", c.name, *v.Action)
		}
		if c.wantAction != nil && (v.Action == nil || *v.Action != *c.wantAction) {
			t.Errorf("%s: action=%v, want %q", c.name, v.Action, *c.wantAction)
		}
	}

	// Replicator states the license in the reason and is tech-assist.
	rep := schemaDecision(Profile{SourceSRType: sourceSRCPCommunity, SchemaStrategy: schemaStrategyMigrate})
	if !rep.TechAssist || !strings.Contains(rep.Reason, "needs a license") {
		t.Errorf("community replicator: techAssist=%v reason=%q", rep.TechAssist, rep.Reason)
	}
	// Schema Linking and Glue carry pros/cons.
	sl := schemaDecision(Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategyMigrate, SourceSROutboundReachableToCC: "Yes"})
	if len(sl.Pros) == 0 || len(sl.Cons) == 0 {
		t.Errorf("schema linking should carry pros/cons")
	}
	// schemaless / mismatch carry an open question.
	if schemaDecision(Profile{SourceSRType: sourceSRNone, SchemaStrategy: schemaStrategySchemaless}).OpenQuestion == "" {
		t.Errorf("schemaless should carry an open question")
	}
	// Other is customer-owned special handling: tech-assist (the render adds the
	// Talk to a person CTA), no self-serve action, and the reason says it's yours to run.
	other := schemaDecision(Profile{SourceSRType: sourceSROther})
	if other.Action != nil || !other.TechAssist || !strings.Contains(other.Reason, "yours to run") {
		t.Errorf("Other: action=%v techAssist=%v reason=%q", other.Action, other.TechAssist, other.Reason)
	}
}

func TestConnectors_Decisions(t *testing.T) {
	no := connectorsDecision(Profile{MSKConnectPresent: strptr("No"), SelfManagedConnectors: strptr("No")})
	if no.Value != "None to move" {
		t.Errorf("none present: value=%q, want None to move", no.Value)
	}
	if got := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes"), ConnectorDestination: "Move to Confluent-managed"}).Value; got != "Rebuild as Confluent-managed connectors" {
		t.Errorf("move to managed: value=%q", got)
	}
	if got := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Keep self-managed"}).Value; got != "Run them yourself on your own Connect cluster" {
		t.Errorf("keep self-managed: value=%q", got)
	}
	// Destination unset: defaults to Confluent-managed (the connector_destination
	// built-in default) rather than dead-ending on "not chosen".
	if got := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes")}).Value; got != "Rebuild as Confluent-managed connectors" {
		t.Errorf("destination defaulted: value=%q, want Rebuild as Confluent-managed connectors", got)
	}
	if got := connectorsDecision(Profile{}).Value; got != "Not assessed" {
		t.Errorf("never asked: value=%q, want Not assessed", got)
	}
}

func TestTopics_Decisions(t *testing.T) {
	if got := topicReadinessDecision(Profile{TopicRemediationFlag: strptr("Yes")}).Value; got != "Set matching topic settings on Confluent Cloud" {
		t.Errorf("needs remediation: value=%q", got)
	}
	if got := topicReadinessDecision(Profile{TopicRemediationFlag: strptr("No")}).Value; got != "Topics carry over as they are" {
		t.Errorf("clean: value=%q", got)
	}
	if got := topicReadinessDecision(Profile{}).Value; got != "Not assessed" {
		t.Errorf("never asked: value=%q, want Not assessed", got)
	}
}

func TestHistorical_Decisions(t *testing.T) {
	if got := historicalDataDecision(Profile{StorageMode: strptr("No")}).Value; got != "No separate backfill to plan" {
		t.Errorf("no tiered: value=%q", got)
	}
	req := historicalDataDecision(Profile{StorageMode: strptr("Yes"), ConsumerHistoryRequirement: "Required"})
	if req.Value != "Backfill the full history" || req.Action != nil || !strings.Contains(req.Reason, "backfill time scales") {
		t.Errorf("tiered+required: value=%q action=%v reason=%q", req.Value, req.Action, req.Reason)
	}
	notReq := historicalDataDecision(Profile{StorageMode: strptr("Yes"), ConsumerHistoryRequirement: "Not required"})
	if notReq.Value != "No separate backfill to plan" || notReq.Action != nil || strings.Contains(notReq.Reason, "backfill time scales") {
		t.Errorf("tiered+not required: value=%q action=%v reason=%q", notReq.Value, notReq.Action, notReq.Reason)
	}
	// A stale "Mixed" falls back to the unanswered default.
	if got := historicalDataDecision(Profile{StorageMode: strptr("Yes"), ConsumerHistoryRequirement: "Mixed"}).Value; got != "Backfill the full history (default)" {
		t.Errorf("stale Mixed: value=%q", got)
	}
	unk := historicalDataDecision(Profile{StorageMode: strptr("Yes")})
	if unk.Value != "Backfill the full history (default)" || unk.Action == nil {
		t.Errorf("tiered+unknown: value=%q action=%v", unk.Value, unk.Action)
	}
	if historicalDataDecision(Profile{}).Value != "Not assessed" {
		t.Errorf("never asked: want Not assessed")
	}
	// Start fresh (no data migration) → no link → no backfill regardless of tiered.
	if got := historicalDataDecision(Profile{NeedsDataMigration: "No", StorageMode: strptr("Yes"), ConsumerHistoryRequirement: "Required"}); got.Value != "No separate backfill to plan" || strings.Contains(got.Reason, "backfill time scales") {
		t.Errorf("start-fresh should have no backfill: value=%q reason=%q", got.Value, got.Reason)
	}
	// Non-tiered but LONG retention + consumers need it → still a backfill.
	longReq := historicalDataDecision(Profile{StorageMode: strptr("No"), LongRetention: strptr("Yes"), ConsumerHistoryRequirement: "Required"})
	if longReq.Value != "Backfill the full history" || !strings.Contains(longReq.Reason, "backfill time scales") || !strings.Contains(longReq.Reason, "long-retention") {
		t.Errorf("non-tiered long retention: value=%q reason=%q", longReq.Value, longReq.Reason)
	}
	// Non-tiered, short retention (both known) → no backfill.
	if got := historicalDataDecision(Profile{StorageMode: strptr("No"), LongRetention: strptr("No")}).Value; got != "No separate backfill to plan" {
		t.Errorf("non-tiered short retention: value=%q", got)
	}
	// Measured size is primary: large retained data + required → backfill, size named.
	big := historicalDataDecision(Profile{RetainedDataGB: f(2048), ConsumerHistoryRequirement: "Required"})
	if big.Value != "Backfill the full history" || !strings.Contains(big.Reason, "2.0 TB") || !strings.Contains(big.Reason, "backfill time scales") {
		t.Errorf("large measured size: value=%q reason=%q", big.Value, big.Reason)
	}
	// Measured small size → carried locally, no separate backfill (even if tiered flag unset).
	small := historicalDataDecision(Profile{RetainedDataGB: f(50), ConsumerHistoryRequirement: "Required"})
	if small.Value != "No separate backfill to plan" || strings.Contains(small.Reason, "backfill time scales") {
		t.Errorf("small measured size: value=%q reason=%q", small.Value, small.Reason)
	}
}
