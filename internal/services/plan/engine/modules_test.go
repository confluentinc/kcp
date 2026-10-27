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
	no := connectorsDecision(Profile{MSKConnectPresent: strptr("No"), SelfManagedConnectors: strptr("No")}, viaLink)
	if no.Value != "None to move" {
		t.Errorf("none present: value=%q, want None to move", no.Value)
	}
	if got := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes"), ConnectorDestination: "Move to Confluent-managed"}, viaLink).Value; got != "Rebuild as Confluent-managed connectors" {
		t.Errorf("move to managed: value=%q", got)
	}
	if got := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Keep self-managed"}, viaLink).Value; got != "Run them yourself on your own Connect cluster" {
		t.Errorf("keep self-managed: value=%q", got)
	}
	// Destination unset: defaults to Confluent-managed (the connector_destination
	// built-in default) rather than dead-ending on "not chosen".
	if got := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes")}, viaLink).Value; got != "Rebuild as Confluent-managed connectors" {
		t.Errorf("destination defaulted: value=%q, want Rebuild as Confluent-managed connectors", got)
	}
	if got := connectorsDecision(Profile{}, viaLink).Value; got != "Not assessed" {
		t.Errorf("never asked: value=%q, want Not assessed", got)
	}
}

func TestTopics_Decisions(t *testing.T) {
	if got := topicReadinessDecision(Profile{TopicRemediationFlag: strptr("Yes")}).Value; got != "Set matching topic settings on Confluent Cloud" {
		t.Errorf("needs remediation: value=%q", got)
	}
	if got := topicReadinessDecision(Profile{TopicRemediationFlag: strptr("No")}).Value; got != "Topics should mirror as they are" {
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
	if !strings.Contains(unk.Reason, "you haven't confirmed whether your consumers need their history") || !strings.Contains(unk.Reason, "from the earliest offset. Confirm to lock this in.") {
		t.Errorf("tiered+unknown reason: %q", unk.Reason)
	}
	if !strings.Contains(notReq.Reason, "The migration still mirrors your topics up to cutover.") {
		t.Errorf("tiered+not required reason: %q", notReq.Reason)
	}
	noTier := historicalDataDecision(Profile{StorageMode: strptr("No")})
	if noTier.Value != "No separate backfill to plan" || !strings.Contains(noTier.Reason, "(no tiered storage)") || !strings.Contains(noTier.Reason, "nothing to re-fetch from object storage") || strings.Contains(noTier.Reason, "long-retention") {
		t.Errorf("scanless no-tiered: value=%q reason=%q", noTier.Value, noTier.Reason)
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

// Start fresh copies nothing, so the topics verdict tells the customer to create them
// rather than claiming they mirror.
func TestTopics_StartFreshCreatesTopics(t *testing.T) {
	const create = "Create your topics on the new cluster before you move producers, because automatic topic creation is off by default on Confluent Cloud."
	fresh := func(flag string) Profile {
		return Profile{NeedsDataMigration: "No", TopicRemediationFlag: strptr(flag)}
	}
	got := topicReadinessDecision(fresh("No"))
	if got.Value != "Create your topics on the new cluster" || !strings.HasPrefix(got.Reason, create) || strings.Contains(got.Reason, "carry over as they are") {
		t.Errorf("start fresh, defaults: %+v", got)
	}
	got = topicReadinessDecision(fresh("Yes"))
	if got.Value != "Set matching topic settings on Confluent Cloud" || !strings.HasPrefix(got.Reason, create) || strings.Contains(got.Reason, "carry over as they are") {
		t.Errorf("start fresh, remediation: %+v", got)
	}
	got = topicReadinessDecision(Profile{NeedsDataMigration: "No"})
	if got.Value != "Not assessed" || !strings.HasPrefix(got.Reason, create) {
		t.Errorf("start fresh, unasked: %+v", got)
	}
	if got := topicReadinessDecision(Profile{NeedsDataMigration: "Yes", TopicRemediationFlag: strptr("No")}); got.Value != "Topics should mirror as they are" {
		t.Errorf("data moves: value = %q", got.Value)
	}
}

// The Connect Migration Utility needs a self-managed Connect REST endpoint, so MSK
// Connect plans never recommend it and say how offsets behave for the generated definitions.
func TestConnectors_UtilityOnlyForSelfManagedConnect(t *testing.T) {
	const utility = "stop_create_latest_offset"
	msk := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes")}, viaLink).Reason
	for _, want := range []string{
		"Generate the connector definitions with kcp create-asset migrate-connectors msk, and apply them only after you promote the mirror topics, so they do not start early.",
		"Confluent-managed sink connectors read with their own consumer group, so create each one with the offsets of its MSK Connect consumer group (connect-<connector-name>); see Create connectors with offsets (https://docs.confluent.io/cloud/current/connectors/offsets.html).",
		"Source connectors don't carry offsets over, so check where each one should start.",
	} {
		if !strings.Contains(msk, want) {
			t.Errorf("MSK Connect reason missing %q: %s", want, msk)
		}
	}
	if strings.Contains(msk, "consumer.offset.group.filters") {
		t.Errorf("MSK Connect reason still mentions the offset group filters: %s", msk)
	}
	if strings.Contains(msk, utility) || strings.Contains(msk, "Migration Utility") {
		t.Errorf("MSK Connect reason recommends the utility: %s", msk)
	}
	self := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes")}, viaLink).Reason
	if !strings.Contains(self, utility) || !strings.Contains(self, "Use one creation path per connector: the utility or generated definitions, not both.") {
		t.Errorf("self-managed reason = %s", self)
	}
	kept := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Keep self-managed"}, viaLink).Reason
	if strings.Contains(kept, utility) {
		t.Errorf("keep self-managed reason mentions %s: %s", utility, kept)
	}
	mixed := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes"), SelfManagedConnectors: strptr("Yes")}, viaLink).Reason
	if !strings.Contains(mixed, "the utility for self-managed Connect, the generated definitions for MSK Connect, not both for the same connector.") {
		t.Errorf("mixed reason = %s", mixed)
	}
}

// Item: the promote, sink-offset and utility-timing wording only applies when a cluster
// link moves the data; Replicator and start-fresh plans drop it, and connectors that stay
// self-managed use neither migrate-connectors msk nor the utility.
func TestConnectors_WordingFollowsDataMovement(t *testing.T) {
	both := Profile{MSKConnectPresent: strptr("Yes"), SelfManagedConnectors: strptr("Yes")}
	const promote = "promote the mirror topics"
	const sink = "Confluent-managed sink connectors read with their own consumer group"
	const afterPromote = "Run the utility only after you promote the mirror topics, since it creates the Confluent-managed connectors right away."

	link := connectorsDecision(both, viaLink).Reason
	for _, want := range []string{promote, sink, afterPromote} {
		if !strings.Contains(link, want) {
			t.Errorf("link reason missing %q: %s", want, link)
		}
	}
	for _, via := range []string{viaReplicator, viaNone} {
		got := connectorsDecision(both, via).Reason
		for _, bad := range []string{promote, sink, "consumer.offset.group.filters", afterPromote} {
			if strings.Contains(got, bad) {
				t.Errorf("%s reason contains %q: %s", via, bad, got)
			}
		}
		if !strings.Contains(got, "Generate the connector definitions with kcp create-asset migrate-connectors msk.") {
			t.Errorf("%s reason dropped generate-definitions: %s", via, got)
		}
	}
	// Connectors that stay self-managed: no msk generation, no utility.
	keep := both
	keep.ConnectorDestination = "Keep self-managed"
	got := connectorsDecision(keep, viaLink).Reason
	for _, bad := range []string{"migrate-connectors msk", "Migration Utility", afterPromote, promote} {
		if strings.Contains(got, bad) {
			t.Errorf("keep self-managed reason contains %q: %s", bad, got)
		}
	}
}

// A nil/empty via is treated as the Cluster Linking path, so the promote sentences
// appear; Replicator and start-fresh never carry them.
func TestConnectorsDecision_ViaGating(t *testing.T) {
	p := Profile{MSKConnectPresent: strptr("Yes"), SelfManagedConnectors: strptr("Yes")}
	for _, via := range []string{"", viaLink} {
		r := connectorsDecision(p, via).Reason
		if !strings.Contains(r, utilityAfterPromoteSentence) || !strings.Contains(r, managedSinkOffsetsSentence) {
			t.Errorf("via %q reason missing link sentences: %q", via, r)
		}
	}
	for _, via := range []string{viaReplicator, viaNone} {
		r := connectorsDecision(p, via).Reason
		if strings.Contains(r, utilityAfterPromoteSentence) || strings.Contains(r, managedSinkOffsetsSentence) || strings.Contains(r, "promote") {
			t.Errorf("via %q reason has link-only sentences: %q", via, r)
		}
	}
}

// With MSK Connect and self-managed connectors both moving over Replicator, the sink
// offsets sentence appears once.
func TestConnectors_ReplicatorBothMovedSinkSentenceOnce(t *testing.T) {
	p := Profile{MSKConnectPresent: strptr("Yes"), SelfManagedConnectors: strptr("Yes")}
	r := connectorsDecision(p, viaReplicator).Reason
	if strings.Count(r, replicatorSinkOffsetsSentence) != 1 || strings.Count(r, sourceOffsetsSentence) != 1 {
		t.Errorf("want each offsets sentence once: %s", r)
	}
}

// On a link plan the source-offsets sentence follows one that ends "before you apply it",
// and self-managed sinks wait for the promote.
func TestConnectors_LinkPlanCopy(t *testing.T) {
	const after = "Source connectors don't carry offsets over, so check where each one should start."
	msk := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes")}, viaLink).Reason
	if !strings.Contains(msk, "before you apply it. "+after) || strings.Contains(msk, after+"before") || strings.Contains(msk, "should start before you apply it") {
		t.Errorf("MSK Connect link reason: %s", msk)
	}
	if r := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes")}, viaReplicator).Reason; !strings.Contains(r, "should start before you apply it.") {
		t.Errorf("replicator reason: %s", r)
	}
	keep := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Keep self-managed"}, viaLink).Reason
	if !strings.Contains(keep, sinkAfterPromoteSentence) {
		t.Errorf("self-managed link reason missing sink sentence: %s", keep)
	}
	if r := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Keep self-managed"}, viaReplicator).Reason; strings.Contains(r, sinkAfterPromoteSentence) {
		t.Errorf("replicator reason has the link sink sentence: %s", r)
	}
}

// On a mixed fleet the Migration Utility sentence is scoped to self-managed Connect, since MSK
// Connect definitions are generated rather than copied by the utility.
func TestConnectorsDecision_MixedFleetScopesUtility(t *testing.T) {
	mixed := connectorsDecision(Profile{MSKConnectPresent: strptr("Yes"), SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Move to Confluent-managed"}, viaLink).Reason
	if !strings.Contains(mixed, "For your self-managed Connect connectors, Confluent's Connect Migration Utility copies") {
		t.Fatalf("mixed fleet: utility sentence not scoped to self-managed Connect: %s", mixed)
	}
	selfOnly := connectorsDecision(Profile{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: "Move to Confluent-managed"}, viaLink).Reason
	if strings.Contains(selfOnly, "For your self-managed Connect connectors") || !strings.Contains(selfOnly, "Confluent's Connect Migration Utility copies each connector's configuration across") {
		t.Fatalf("self-managed only: unexpected utility wording: %s", selfOnly)
	}
}
