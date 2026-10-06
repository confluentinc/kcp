package engine

import (
	"fmt"
	"strings"
)

// Blocks 7–9 — connectors, topic readiness, historical data. All three
// distinguish "asked and the answer is no" from "never asked" (nil pointer),
// because the plan never scans the cluster and must not claim knowledge it
// doesn't have.

// ConnectorsKind is the stable typed identity of a Connectors verdict, so the
// renderer switches on it rather than on the display Value copy. Not serialized
// (json:"-") to keep plan.json byte-identical.
type ConnectorsKind string

const (
	ConnectorsKindNone        ConnectorsKind = "none"
	ConnectorsKindNotAssessed ConnectorsKind = "not_assessed"
	ConnectorsKindManaged     ConnectorsKind = "managed"
	ConnectorsKindSelfManaged ConnectorsKind = "self_managed"
)

// ConnectorsResult is the connectors verdict.
type ConnectorsResult struct {
	Value   string         `json:"value"`
	Pending bool           `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Kind    ConnectorsKind `json:"-"`
	Reason  string         `json:"reason"`
	Action  *string        `json:"action"`
	Source  string         `json:"source,omitempty"`
}

// Shared connector sentences, kept word for word with the plan renderer.
const (
	managedSinkOffsetsSentence        = "Confluent-managed sink connectors read with their own consumer group, so create each one with the offsets of its MSK Connect consumer group (connect-<connector-name>); see Create connectors with offsets (" + srcConnectorOffsets + "). Add an offsets block to each sink connector's generated definition before you apply it."
	utilityVersionSentence            = "This mode needs Kafka Connect on Apache Kafka 3.6 or Confluent Platform 7.6 or later, which has the offsets REST API."
	utilityNewOffsetsSentence         = "Use the Connect Migration Utility with stop_create_latest_offset for source connectors only. " + utilityVersionSentence + " Create sink connectors without carried-over offsets, since the topics on Confluent Cloud have new offsets."
	replicatorSinkOffsetsSentence     = "At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, print each partition's offset for that time with kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group <any-unused-group-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --dry-run, then create the connector with those offsets; see Create connectors with offsets (" + srcConnectorOffsets + ")."
	selfManagedSinkOffsetsSentence    = "At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, set the new sink's consumer group to that time, then create the sink connector: kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group connect-<connector-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --execute. Replicator keeps each record's timestamp, so the sink resumes where the old one stopped; a few records around that time may be delivered twice."
	generatedSinkOffsetsBlockSentence = "Add an offsets block to each sink connector's generated definition before you apply it."
	sourceOffsetsSentence             = "Source connectors don't carry offsets over, so check where each one should start before you apply it."
	sourceOffsetsAfterSinksSentence   = "Source connectors don't carry offsets over, so check where each one should start."
	sinkAfterPromoteSentence          = "Stop each old sink connector with its service, before you promote its topics, so its last offsets sync. Create sink connectors only after you promote their topics, and list each sink's consumer group (connect-<connector-name>) in consumer.offset.group.filters so it resumes from its synced offsets."
	utilityReplicatorOffsetsSentence  = "Use the Connect Migration Utility with stop_create_latest_offset for source connectors only. " + utilityVersionSentence + " " + replicatorSinkOffsetsSentence
	utilityAfterPromoteSentence       = "Run the utility only after you promote the mirror topics, since it creates the Confluent-managed connectors right away."
)

// connectorKinds names the connector runtimes a source can carry, for the
// "no …" / "whether this cluster runs …" copy. MSK adds MSK Connect; every source
// can run self-managed Connect.
func connectorKinds(p Profile) string {
	if p.isMSK() {
		return "MSK Connect or self-managed Connect"
	}
	return "self-managed Connect"
}

func connectorsDecision(p Profile, via string) ConnectorsResult {
	present := deref(p.MSKConnectPresent) == "Yes" || deref(p.SelfManagedConnectors) == "Yes"
	asked := p.MSKConnectPresent != nil || p.SelfManagedConnectors != nil
	if !present {
		if asked {
			return ConnectorsResult{Value: "None to move", Kind: ConnectorsKindNone, Reason: basis(srcOr(p.ConnectorsAnswered, "no "+connectorKinds(p))) + "there is no connector work in this plan."}
		}
		return ConnectorsResult{Value: "Not assessed", Kind: ConnectorsKindNotAssessed, Reason: "We don't yet know whether this cluster runs " + connectorKinds(p) + ". The scan didn't capture it, so tell us and we'll fold it in. Connectors do not travel with your topics, so this is worth settling before you cut over.", Action: strptr("Tell us about your connectors")}
	}
	// Connector destination defaults to Confluent-managed when the customer hasn't
	// chosen one (the connector_destination question's built-in default), so the
	// verdict always resolves to a concrete method rather than dead-ending on
	// "destination not chosen". defaulted keeps the provenance honest: we don't say
	// "based on your answer" for a default the customer never set.
	dest := p.ConnectorDestination
	defaulted := dest == ""
	if defaulted {
		dest = "Move to Confluent-managed"
	}
	mskConnect := deref(p.MSKConnectPresent) == "Yes"
	selfManaged := deref(p.SelfManagedConnectors) == "Yes"
	leadDrivers := []driver{srcOr(p.ConnectorsAnswered, "connectors on the source")}
	var value string
	var kind ConnectorsKind
	switch dest {
	case "Keep self-managed":
		value = "Run them yourself on your own Connect cluster"
		kind = ConnectorsKindSelfManaged
		leadDrivers = append(leadDrivers, ans("keeping them self-managed"))
	default: // Move to Confluent-managed, chosen or defaulted
		value = "Rebuild as Confluent-managed connectors"
		kind = ConnectorsKindManaged
		if !defaulted {
			leadDrivers = append(leadDrivers, ans("moving them to Confluent-managed"))
		}
	}
	reason := basis(leadDrivers...) + "connectors do not travel with your topics, so whichever way you go, you set each one up again on the other side."
	switch {
	case mskConnect && dest == "Keep self-managed":
		reason += " You are on MSK Connect today, which is a managed service with no Confluent Cloud equivalent, so \"self-managed\" here means running a Connect cluster yourself. That is infrastructure you do not operate at the moment."
	case mskConnect && selfManaged:
		reason += " You run both MSK Connect and your own self-managed connectors today, and each is recreated as a Confluent-managed connector."
	case mskConnect:
		reason += " You are on MSK Connect today, so each connector is recreated as a Confluent-managed connector."
	}
	if p.isCP() && kind == ConnectorsKindManaged {
		reason += " Your Confluent Platform connectors are Confluent connectors, so most have a Confluent Cloud fully-managed equivalent to move to."
	}
	if defaulted {
		reason += " Confluent-managed is the default here; you can choose to keep them self-managed and run your own Connect cluster instead."
	}
	// The Connect Migration Utility's offset options need a self-managed Connect REST endpoint
	// (worker URLs), which MSK Connect does not expose. So MSK Connect gets generated definitions
	// (applied after the mirror topics are promoted when a cluster link moves the data), and the
	// utility is for self-managed Connect only. Connectors that stay self-managed use neither.
	moved := dest != "Keep self-managed"
	byLink := via == "" || via == viaLink
	if mskConnect && moved {
		if byLink {
			reason += " Generate the connector definitions with kcp create-asset migrate-connectors msk, and apply them only after you promote the mirror topics, so they do not start early." +
				" " + managedSinkOffsetsSentence
		} else {
			reason += " Generate the connector definitions with kcp create-asset migrate-connectors msk."
		}
		if byLink {
			reason += " " + sourceOffsetsAfterSinksSentence
		} else {
			reason += " " + sourceOffsetsSentence
		}
	}
	// Self-managed Connect on a Cluster Linking plan: source connectors are recreated on your own
	// Connect as before; sinks wait for the promote and resume from their synced consumer group.
	if dest == "Keep self-managed" && byLink {
		reason += " " + sinkAfterPromoteSentence
	}
	if selfManaged && moved {
		reason += " Confluent's Connect Migration Utility copies each connector's configuration across, so you are not retyping them."
		if byLink {
			reason += " When you move a connector, use the Connect Migration Utility with stop_create_latest_offset so it resumes from its last offsets instead of re-reading or re-snapshotting. " + utilityVersionSentence
		} else {
			// Replicator changes offsets and start-fresh topics are empty, so sinks cannot reuse their old offsets.
			if via == viaReplicator {
				reason += " " + utilityReplicatorOffsetsSentence
			} else {
				reason += " " + utilityNewOffsetsSentence
			}
		}
		if byLink {
			reason += " " + utilityAfterPromoteSentence
		}
		if mskConnect {
			reason += " Use one creation path per connector: the utility for self-managed Connect, the generated definitions for MSK Connect, not both for the same connector."
		} else {
			reason += " Use one creation path per connector: the utility or generated definitions, not both."
		}
	}
	// Every Replicator plan with connectors says both, whichever path moved them.
	if via == viaReplicator {
		sinkSentence := replicatorSinkOffsetsSentence
		if !moved {
			sinkSentence = selfManagedSinkOffsetsSentence
		}
		if !strings.Contains(reason, sinkSentence) {
			reason += " " + sinkSentence
		}
		// A generated definition is applied for MSK Connect, and for self-managed connectors
		// moved to Confluent-managed; kept self-managed sinks are set by the reset-offsets command.
		if moved && (mskConnect || selfManaged) && !strings.Contains(reason, generatedSinkOffsetsBlockSentence) {
			reason += " " + generatedSinkOffsetsBlockSentence
		}
		// Where the utility moves the connectors, its stop_create_latest_offset mode carries source offsets.
		if (!selfManaged || !moved) && !strings.Contains(reason, "carry offsets over") {
			reason += " " + sourceOffsetsSentence
		}
	}
	return ConnectorsResult{Value: value, Kind: kind, Reason: reason, Action: strptr("Migrate connectors")}
}

// TopicsResult is the topic-readiness verdict.
type TopicsResult struct {
	Value   string `json:"value"`
	Pending bool   `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Tiered  bool   `json:"tiered"`
	Reason  string `json:"reason"`
	Source  string `json:"source,omitempty"`
}

func topicReadinessDecision(p Profile) TopicsResult {
	tiered := deref(p.StorageMode) == "Yes"
	needsRemediation := deref(p.TopicRemediationFlag) == "Yes"
	askedTopics := p.TopicRemediationFlag != nil
	// Start fresh copies nothing, so no topic carries over: the customer creates them, and
	// Confluent Cloud does not create a topic on first write. The mechanism does not depend
	// on the tier, so it is resolved without one.
	fresh := resolveMechanism(p, "", "").Mechanism == "start-fresh"
	var reason string
	switch {
	case needsRemediation:
		lead := basis(srcOr(p.TopicsAnswered, "topics with non-default settings")) + "your topics carry over as they are. Confluent Cloud fixes replication factor at 3, so any topic with a different replication factor is created with a replication factor of 3."
		if fresh {
			lead = startFreshTopics + " Confluent Cloud fixes replication factor at 3, so create each topic with a replication factor of 3."
		}
		reason = lead + " Retention and cleanup policy are fully configurable. Set them on your Confluent Cloud topics to match your source, or keep Confluent Cloud's defaults: 7-day retention and a delete cleanup policy. Max message size is also configurable, but capped by cluster type: up to 8 MB on Basic or Standard, up to 20 MB on Enterprise or Dedicated. If any source topic carries messages larger than 8 MB, confirm your target cluster type supports that message size before you migrate. Otherwise, nothing needs changing on your source cluster."
	case askedTopics:
		if fresh {
			reason = startFreshTopics + " You told us your topics use Confluent Cloud's defaults, so create them with those defaults."
		} else {
			reason = basis(srcOr(p.TopicsAnswered, "topic settings match the defaults")) + "your topics carry over as they are."
		}
	default:
		reason = "We haven't checked your topic settings and we don't scan your cluster. Confluent Cloud fixes replication factor at 3 (a topic with a different replication factor is created with a replication factor of 3), and retention and cleanup policy are fully configurable to match your source or Confluent Cloud's defaults (7-day retention, delete cleanup policy). Max message size is also configurable, but capped by cluster type (up to 8 MB on Basic or Standard, up to 20 MB on Enterprise or Dedicated), so check your largest source message size against that cap before you migrate. Outside of that, nothing here needs changing on your source first."
	}
	if fresh && !needsRemediation && !askedTopics {
		reason = startFreshTopics + " " + reason
	}
	if tiered {
		reason += " This cluster uses tiered storage, so view the historical-data decision for your backfill approach."
	}
	value := "Not assessed"
	switch {
	case needsRemediation:
		value = "Set matching topic settings on Confluent Cloud"
	case askedTopics:
		value = "Topics should mirror as they are"
		if fresh {
			value = "Create your topics on the new cluster"
		}
	}
	return TopicsResult{Value: value, Tiered: tiered, Reason: reason}
}

// HistoricalResult is the historical-data (backfill) verdict.
type HistoricalResult struct {
	Value   string  `json:"value"`
	Pending bool    `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Reason  string  `json:"reason"`
	Action  *string `json:"action"`
	Source  string  `json:"source,omitempty"`
}

// significantBackfillGB is the retained-data size (GB) above which a backfill is
// worth confirming consumer need for and planning a cutover window around.
const significantBackfillGB = 256.0

// hasBackfillableHistory reports whether there is enough history to carry that the
// consumer-history question and a backfill plan are worth surfacing. Measured
// retained size is the primary signal; tiered storage and long retention are the
// fallbacks when storage metrics weren't scanned.
func hasBackfillableHistory(p Profile) bool {
	if p.RetainedDataGB != nil {
		return *p.RetainedDataGB >= significantBackfillGB
	}
	return deref(p.StorageMode) == "Yes" || deref(p.LongRetention) == "Yes"
}

// humanGB renders a GB figure as GB or TB.
func humanGB(gb float64) string {
	if gb >= 1024 {
		return fmt.Sprintf("%.1f TB", gb/1024)
	}
	return fmt.Sprintf("%.0f GB", gb)
}

func historicalDataDecision(p Profile) HistoricalResult {
	// Historical data only comes across when you migrate existing data — start
	// fresh and nothing is carried, regardless of stored volume.
	if p.NeedsDataMigration == "No" {
		return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(ans("no data migration")) + "you're starting fresh, so no existing data is copied over and there's no history to carry. Producers and consumers just repoint to the new cluster."}
	}

	measured := p.RetainedDataGB != nil
	if !hasBackfillableHistory(p) {
		// Known-small (measured, or known non-tiered/short-retention): the little
		// history there is comes across automatically, nothing separate to size.
		if measured {
			return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(sc("about "+humanGB(*p.RetainedDataGB)+" retained")) + "your retained data is small enough to come across with your data migration, so there's no separate historical backfill to plan whichever cutover you choose."}
		}
		if p.StorageMode != nil && p.LongRetention == nil {
			// Long retention was never measured, so don't claim the local window is small.
			return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(srcOr(p.TieredAnswered, "no tiered storage")) + "there's nothing to re-fetch from object storage. Your data migration still copies everything your topics currently retain, so if they keep a lot of data, plan a cutover window long enough to move it. Run " + MetricsScanCommand(p) + " to size the retained data."}
		}
		if p.StorageMode != nil || p.LongRetention != nil {
			return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(srcOr(p.TieredAnswered, "no tiered or long-retention history")) + "the small local window comes across with your data migration, so there's no separate backfill to plan. Run " + MetricsScanCommand(p) + " to size the retained data if you want to confirm."}
		}
		return HistoricalResult{Value: "Not assessed", Reason: "We haven't measured this cluster's retained data (run " + MetricsScanCommand(p) + ") or confirmed tiered storage/retention, so we can't size the backfill.", Action: strptr("Measure retained data / confirm tiered storage")}
	}

	// Describe the volume — the measured size if we have it, else the qualitative
	// source. scanTrigger is the short phrase for the "Based on your scan (…)" lead.
	var source, scanTrigger string
	switch {
	case measured:
		source = "about " + humanGB(*p.RetainedDataGB) + " of retained data"
		scanTrigger = "about " + humanGB(*p.RetainedDataGB) + " retained"
	case deref(p.StorageMode) == "Yes":
		source = "your tiered history"
		scanTrigger = "tiered storage in use"
	default:
		source = "your long-retention history"
		scanTrigger = "long retention configured"
	}
	// The volume finding is a scan value when measured; when it falls back to the
	// tiered / long-retention flags, honour whether those were answered.
	volumeDriver := sc(scanTrigger)
	if !measured {
		volumeDriver = srcOr(p.TieredAnswered, scanTrigger)
	}
	const timeNote = " Your backfill time scales with the retained volume, so plan a cutover/backfill window big enough to move it before you switch over."
	switch p.ConsumerHistoryRequirement {
	case "Required":
		return HistoricalResult{Value: "Backfill the full history", Reason: basis(volumeDriver, ans("consumers need history")) + "we copy all of it (" + source + ") to the new cluster from the earliest offset, then new data keeps flowing until cutover." + timeNote}
	case "Not required":
		return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(ans("consumers don't need history")) + "your consumers don't need historical data, so there is no separate backfill to plan. The migration still mirrors your topics up to cutover."}
	}
	return HistoricalResult{Value: "Backfill the full history (default)", Reason: basis(volumeDriver) + "you haven't confirmed whether your consumers need their history, so we default to copying all of it (" + source + ") to the new cluster from the earliest offset. Confirm to lock this in." + timeNote, Action: strptr("Confirm historical-data requirement")}
}
