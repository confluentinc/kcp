package engine

import "fmt"

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

func connectorsDecision(p Profile) ConnectorsResult {
	present := deref(p.MSKConnectPresent) == "Yes" || deref(p.SelfManagedConnectors) == "Yes"
	asked := p.MSKConnectPresent != nil || p.SelfManagedConnectors != nil
	if !present {
		if asked {
			return ConnectorsResult{Value: "None to move", Kind: ConnectorsKindNone, Reason: basis(srcOr(p.ConnectorsAnswered, "no MSK Connect or self-managed Connect")) + "there is no connector work in this plan."}
		}
		return ConnectorsResult{Value: "Not assessed", Kind: ConnectorsKindNotAssessed, Reason: "We don't yet know whether this cluster runs MSK Connect or self-managed Connect. The scan didn't capture it, so tell us and we'll fold it in. Connectors do not travel with your topics, so this is worth settling before you cut over.", Action: strptr("Tell us about your connectors")}
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
		leadDrivers = append(leadDrivers, ans("connector destination"))
	default: // Move to Confluent-managed, chosen or defaulted
		value = "Rebuild as Confluent-managed connectors"
		kind = ConnectorsKindManaged
		if !defaulted {
			leadDrivers = append(leadDrivers, ans("connector destination"))
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
	if defaulted {
		reason += " Confluent-managed is the default here; you can choose to keep them self-managed and run your own Connect cluster instead."
	}
	reason += " Confluent's Connect Migration Utility copies each connector's configuration across, so you are not retyping them."
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
	var reason string
	switch {
	case needsRemediation:
		reason = basis(srcOr(p.TopicsAnswered, "topics with non-default settings")) + "your topics carry over as they are. Confluent Cloud fixes replication factor at 3, so any topic with a different replication factor is created with a replication factor of 3. Retention and cleanup policy are fully configurable. Set them on your Confluent Cloud topics to match your source, or keep Confluent Cloud's defaults: 7-day retention and a delete cleanup policy. Max message size is also configurable, but capped by cluster type: up to 8 MB on Basic or Standard, up to 20 MB on Enterprise or Dedicated. If any source topic carries messages larger than 8 MB, confirm your target cluster type supports that message size before you migrate. Otherwise, nothing needs changing on your source cluster."
	case askedTopics:
		reason = basis(srcOr(p.TopicsAnswered, "topic settings match the defaults")) + "your topics carry over as they are."
	default:
		reason = "We haven't checked your topic settings and we don't scan your cluster. Confluent Cloud fixes replication factor at 3 (a topic with a different replication factor is created with a replication factor of 3), and retention and cleanup policy are fully configurable to match your source or Confluent Cloud's defaults (7-day retention, delete cleanup policy). Max message size is also configurable, but capped by cluster type (up to 8 MB on Basic or Standard, up to 20 MB on Enterprise or Dedicated), so check your largest source message size against that cap before you migrate. Outside of that, nothing here needs changing on your source first."
	}
	if tiered {
		reason += " This cluster uses tiered storage, so view the historical-data decision for your backfill approach."
	}
	value := "Not assessed"
	switch {
	case needsRemediation:
		value = "Set matching topic settings on Confluent Cloud"
	case askedTopics:
		value = "Topics carry over as they are"
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
			return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(sc("about "+humanGB(*p.RetainedDataGB)+" retained")) + "that's small enough to come across with your data migration, so there's no separate historical backfill to plan whichever cutover you choose."}
		}
		if p.StorageMode != nil || p.LongRetention != nil {
			return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(srcOr(p.TieredAnswered, "no tiered or long-retention history")) + "the small local window comes across with your data migration, so there's no separate backfill to plan. Run `kcp scan metrics` to size the retained data if you want to confirm."}
		}
		return HistoricalResult{Value: "Not assessed", Reason: "We haven't measured this cluster's retained data (run `kcp scan metrics`) or confirmed tiered storage/retention, so we can't size the backfill.", Action: strptr("Measure retained data / confirm tiered storage")}
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
		return HistoricalResult{Value: "Backfill the full history", Reason: basis(volumeDriver, ans("consumers need history")) + "we copy all of it (" + source + ") to the new cluster from the earliest offset, then new data keeps flowing from cutover." + timeNote}
	case "Not required":
		return HistoricalResult{Value: "No separate backfill to plan", Reason: basis(ans("consumers don't need history")) + "there's nothing extra to copy over; only new data flows to the new cluster from cutover."}
	}
	return HistoricalResult{Value: "Backfill the full history (default)", Reason: basis(volumeDriver) + "this cluster keeps significant history (" + source + ") and you haven't confirmed whether your consumers need it, so we default to copying all of it across." + timeNote, Action: strptr("Confirm historical-data requirement")}
}
