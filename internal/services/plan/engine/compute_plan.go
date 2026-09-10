package engine

import (
	"encoding/json"
	"strings"
)

// SizingVerdict is the plan-level sizing card (distinct from the internal
// SizingResult): a customer-facing value plus the band and driver reasoning.
type SizingVerdict struct {
	Value             string         `json:"value"`
	Pending           bool           `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Band              int            `json:"band"`
	PartitionsDim     PartitionsDim  `json:"partitions_dim"`
	MissingPartitions bool           `json:"missing_partitions"`
	Reason            string         `json:"reason"`
	Source            string         `json:"source,omitempty"`
	Signals           []SizingSignal `json:"signals,omitempty"` // the measured capacity signals sizing read (audit trail)
}

// SizingSignal is one capacity dimension the sizing read: the value it sized from,
// or a note that the scan didn't measure it. Shown read-only as the sizing audit
// trail, so a reader can see the numbers behind the tier.
type SizingSignal struct {
	Name     string `json:"name"`               // short dimension label, e.g. "ingress throughput"
	Value    string `json:"value"`              // self-describing measured value, e.g. "180 megabytes/sec ingress"
	Measured bool   `json:"measured"`           // false when the scan didn't capture it
	Answered bool   `json:"answered,omitempty"` // the value came from an answer (a band) rather than a scanned measurement
	Driver   bool   `json:"driver,omitempty"`   // this dimension drove the sizing band (and any size-based specialist handoff)
}

// sizingSignals builds the measured-capacity audit trail from a profile: the exact
// partition count (or a supplied band) and the peak ingress/egress/request rate.
func sizingSignals(p Profile) []SizingSignal {
	mbps := func(name string, v *float64) SizingSignal {
		if v == nil {
			return SizingSignal{Name: name}
		}
		return SizingSignal{Name: name, Value: formatCommas(*v) + " megabytes/sec peak " + strings.TrimSuffix(name, " throughput"), Measured: true}
	}
	var parts SizingSignal
	switch {
	case p.isServerless():
		// Serverless is capped below Band 1 by the platform, so the band isn't asked and
		// any partition_band answer doesn't apply. Show the ceiling as the assumption.
		parts = SizingSignal{Name: "partition count", Value: "up to 2,400 partitions (assumed for MSK Serverless)", Measured: true, Answered: true}
	case p.PartitionsExact != nil:
		parts = SizingSignal{Name: "partition count", Value: formatCommas(*p.PartitionsExact) + " partitions", Measured: true}
	case p.PartitionBand != "":
		parts = SizingSignal{Name: "partition count", Value: p.PartitionBand + " partitions", Measured: true, Answered: true}
	default:
		parts = SizingSignal{Name: "partition count"}
	}
	req := SizingSignal{Name: "request rate"}
	if p.PeakRequestsPerSec != nil {
		req = SizingSignal{Name: "request rate", Value: formatCommas(*p.PeakRequestsPerSec) + " peak requests/sec", Measured: true}
	}
	sigs := []SizingSignal{parts, mbps("ingress throughput", p.PeakIngressMbps), mbps("egress throughput", p.PeakEgressMbps), req}
	// Mark the dimension that drove the band, so a size-based specialist handoff can
	// lead with it and the rest read as supporting measurements.
	driverName := map[string]string{
		string(dimPartitions):  "partition count",
		string(dimIngress):     "ingress throughput",
		string(dimEgress):      "egress throughput",
		string(dimRequestRate): "request rate",
	}[sizingBand(p).Driver]
	for i := range sigs {
		if sigs[i].Name == driverName && sigs[i].Measured {
			sigs[i].Driver = true
		}
	}
	return sigs
}

// PlanResult is the full plan: every verdict, plus the withheld flag. When
// withheld is true we do not put the recommendation in front of the customer,
// though the verdicts are still computed (sales needs them, and the human-assist
// reasoning is derived from them).
type PlanResult struct {
	ClusterType    ClusterTypeResult `json:"cluster_type"`
	Sizing         SizingVerdict     `json:"sizing"`
	Networking     NetworkingResult  `json:"networking"`
	Auth           AuthResult        `json:"auth"`
	Switchover     SwitchoverResult  `json:"switchover"`
	Schema         SchemaResult      `json:"schema"`
	Connectors     ConnectorsResult  `json:"connectors"`
	Topics         TopicsResult      `json:"topics"`
	HistoricalData HistoricalResult  `json:"historical_data"`
	HumanAssist    HumanAssistResult `json:"human_assist"`
	Withheld       bool              `json:"withheld"`
}

// MarshalJSON hides the computed verdicts for a withheld cluster: when a plan is
// routed to a specialist we do not put an automated recommendation in front of
// the customer, in plan.json any more than in plan.md. Only the withheld flag and
// the human-assist reasoning (why it needs a specialist) are serialized.
func (p PlanResult) MarshalJSON() ([]byte, error) {
	if p.Withheld {
		return json.Marshal(struct {
			HumanAssist HumanAssistResult `json:"human_assist"`
			Withheld    bool              `json:"withheld"`
		}{p.HumanAssist, true})
	}
	type alias PlanResult
	return json.Marshal(alias(p))
}

// Public docs cited per recommendation (docs.confluent.io only).
const (
	srcClusterType                   = "https://docs.confluent.io/cloud/current/clusters/cluster-types.html"
	srcNetworking                    = "https://docs.confluent.io/cloud/current/networking/overview.html"
	srcNetworkingPni                 = "https://docs.confluent.io/cloud/current/networking/aws-pni.html"
	srcNetworkingEgressPrivateLink   = "https://docs.confluent.io/cloud/current/networking/aws-egress-privatelink-esku.html"
	srcNetworkingEgressPrivateLinkAz = "https://docs.confluent.io/cloud/current/networking/azure-egress-privatelink-esku.html"
	srcNetworkingPrivateLinkAws      = "https://docs.confluent.io/cloud/current/networking/aws-privatelink.html"
	srcNetworkingPrivateLinkAzure    = "https://docs.confluent.io/cloud/current/networking/azure-platt.html"
	srcNetworkingPrivateLinkGcp      = "https://docs.confluent.io/cloud/current/networking/gcp-platt.html"
	srcAuthCC                        = "https://docs.confluent.io/cloud/current/security/authenticate/overview.html"
	srcSwitchoverCL                  = "https://docs.confluent.io/cloud/current/multi-cloud/cluster-linking/migrate-cc.html"
	srcReplicator                    = "https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/index.html"
	srcSchema                        = "https://docs.confluent.io/cloud/current/sr/schema-linking.html"
	srcSchemaFresh                   = "https://docs.confluent.io/cloud/current/sr/index.html"
	srcConnectors                    = "https://docs.confluent.io/cloud/current/connectors/index.html"
	srcTopics                        = "https://docs.confluent.io/cloud/current/client-apps/topics/manage.html"
)

// noSourceValues name a verdict that is "we cannot answer" or "nothing to do",
// so no page belongs on it.
var noSourceValues = []string{"Unknown", "Not set yet", "Not assessed", "Special handling", "Pending", "Start fresh"}

// ComputePlan is the top-level assembly: profile in, full plan out. Pure — no
// I/O, no mutation of the input.
func ComputePlan(p Profile) PlanResult {
	sizing := sizingBand(p)
	tc := targetCloud(p)

	// 1 & 2: band + features -> tentative cluster type (hard limits only).
	tentative := clusterType(p, sizing, tc, nil)

	// 3 & 4 & 5: networking selection. ApplyMigrationEgress lets networkingDecision
	// fold in the migration cluster-link's Egress PrivateLink Endpoint on the
	// settled result (the tentative-only netOf wiring leaves it off).
	networking := networkingDecision(p, netCtx{TC: tc, Band: sizing.Band, Tier: tentative.Tier, CrossedToPrivate: tentative.CrossedToPrivate, ApplyMigrationEgress: true})

	// Fold networking's own escalation back into cluster type (two-phase settle).
	final := tentative
	if !tentative.Dedicated && networking.ForcesDedicated {
		final = clusterType(p, sizing, tc, &forcedDedicated{
			Fired:         true,
			Reason:        networking.ForcesDedicatedReason,
			Encouragement: networking.ForcesDedicatedEncouragement,
		})
		// Re-resolve networking now that Dedicated is final — the method menu
		// differs by tier (PNI is Enterprise-only).
		networking = networkingDecision(p, netCtx{TC: tc, Band: sizing.Band, Tier: final.Tier, CrossedToPrivate: final.CrossedToPrivate, ApplyMigrationEgress: true})
	}

	sizingVerdict := buildSizingVerdict(p, sizing, final.Tier)

	auth := authDecision(p, tc)
	switchover := switchoverDecision(p, sizing, final.Tier)

	// Source-credential handling: how this application's existing source auth is
	// used given how it moves data. Carried as structured data only; the renderer
	// surfaces it once, in the shared migration-infrastructure section, rather than
	// repeating it in every application's data-migration reason.
	switchover.SourceCredentialHandling = sourceCredentialHandling(p, &switchover)

	schema := schemaDecision(p)
	connectors := connectorsDecision(p)
	topics := topicReadinessDecision(p)
	historical := historicalDataDecision(p)

	// Whether the plan is actually finished — for the "yours to run" copy.
	complete := !isOpenVerdict(switchover.Value, switchover.Held) &&
		!isOpenVerdict(schema.Value, false) &&
		!isOpenVerdict(connectors.Value, false) &&
		!isOpenVerdict(topics.Value, false) &&
		!isOpenVerdict(historical.Value, false)

	humanAssist := humanAssistDecision(p, haCtx{
		Tier:             final.Tier,
		Band:             sizing.Band,
		NetworkingMethod: networking.Value,
		DedicatedReasons: final.Causes,
		Complete:         complete,
	})

	plan := PlanResult{
		ClusterType:    final,
		Sizing:         sizingVerdict,
		Networking:     networking,
		Auth:           auth,
		Switchover:     switchover,
		Schema:         schema,
		Connectors:     connectors,
		Topics:         topics,
		HistoricalData: historical,
		HumanAssist:    humanAssist,
		Withheld:       humanAssist.Required,
	}
	attachCitations(&plan, tc)
	return plan
}

// buildSizingVerdict builds the plan-level sizing card from the sized band and the
// settled tier: the elastic-vs-fixed headline, the driver-led reason, and the
// measured-capacity audit trail. Source is wired later by attachCitations.
func buildSizingVerdict(p Profile, sizing SizingResult, tier Tier) SizingVerdict {
	driverPhrase := map[string]string{
		string(dimPartitions):  "your partition count",
		string(dimIngress):     "your ingress throughput",
		string(dimEgress):      "your egress throughput",
		string(dimRequestRate): "your request rate",
	}[sizing.Driver]
	if driverPhrase == "" {
		driverPhrase = "your partition count"
	}
	elastic := tier != TierDedicated

	sizingVerdict := SizingVerdict{
		Value:             "Fixed capacity, sized with you",
		Band:              sizing.Band,
		PartitionsDim:     sizing.PartitionsDim,
		MissingPartitions: sizing.MissingPartitions,
		Signals:           sizingSignals(p),
	}
	if elastic {
		sizingVerdict.Value = "Autoscales, nothing to provision"
	}
	if sizing.MissingPartitions {
		// No partition count or throughput scanned — nothing concrete to lead with.
		sizingVerdict.Reason = "We don't have a partition count or throughput for this cluster yet, so this is a lower bound rather than a final answer. It firms up once the sizing numbers are in. "
	} else {
		driverBare := strings.TrimPrefix(driverPhrase, "your ")
		// Lead with the actual scanned number for the driving dimension, so the
		// customer sees "(6,000 partitions)" rather than a bare label.
		scanTrigger := driverBare
		switch sizing.Driver {
		case string(dimPartitions):
			if p.PartitionsExact != nil {
				scanTrigger = formatCommas(*p.PartitionsExact) + " partitions"
			}
		case string(dimIngress):
			if p.PeakIngressMbps != nil {
				scanTrigger = formatCommas(*p.PeakIngressMbps) + " megabytes/sec ingress"
			}
		case string(dimEgress):
			if p.PeakEgressMbps != nil {
				scanTrigger = formatCommas(*p.PeakEgressMbps) + " megabytes/sec egress"
			}
		}
		// "The most demanding signal" implies several were compared; that's only true
		// when a throughput signal was measured alongside the partition count. With
		// partition count as the sole signal, say so plainly instead.
		measuredThroughput := p.PeakIngressMbps != nil || p.PeakEgressMbps != nil || p.PeakRequestsPerSec != nil
		// The partition figure can be a scanned count or an answered band; only the
		// partition driver is answerable, so throughput drivers stay scan-sourced. An
		// empty driver is the partition fallback (driverPhrase defaults to the partition
		// count), so it follows the partition provenance too.
		sizeAnswered := (sizing.Driver == string(dimPartitions) || sizing.Driver == "") && p.PartitionsAnswered
		// A Serverless band is settled by the platform, so its provenance tracks the
		// source-cluster-type fact rather than the partition count.
		if sizing.ServerlessCapped {
			sizeAnswered = p.SourceClusterTypeAnswered
		}
		if measuredThroughput {
			sizingVerdict.Reason = basis(srcOr(sizeAnswered, scanTrigger)) + "we size from the most demanding signal, here your " + driverBare + ". "
		} else {
			sizingVerdict.Reason = basis(srcOr(sizeAnswered, scanTrigger)) + "we size from your " + driverBare + " (the only signal we have); measured ingress/egress can raise this. "
		}
	}
	if elastic {
		sizingVerdict.Reason += "This tier scales with your workload, so there is no capacity for you to pick."
	} else {
		sizingVerdict.Reason += "A cluster this size has fixed capacity, which we'll work out with you."
	}
	return sizingVerdict
}

// attachCitations wires each verdict's public-docs Source in place. citeFor
// suppresses the page on a withheld plan or an unanswerable verdict; networking is
// deliberately NOT gated on withheld (the method is still right on a withheld plan).
func attachCitations(plan *PlanResult, tc string) {
	citeFor := func(value, url string) string {
		if plan.Withheld {
			return ""
		}
		if contains(noSourceValues, value) {
			return ""
		}
		return url
	}

	plan.ClusterType.Source = citeFor(string(plan.ClusterType.Value), srcClusterType)

	switch plan.Schema.Kind {
	case SchemaKindLinking:
		plan.Schema.Source = citeFor(plan.Schema.Value, srcSchema)
	case SchemaKindReplicator:
		plan.Schema.Source = citeFor(plan.Schema.Value, srcReplicator)
	case SchemaKindFresh:
		plan.Schema.Source = citeFor(plan.Schema.Value, srcSchemaFresh)
	case SchemaKindGlueBulk:
		// Glue bulk re-registration has no dedicated public page; cite the general
		// Schema Registry docs (the same page Fresh uses) so its table row carries a
		// [docs] link like its siblings.
		plan.Schema.Source = citeFor(plan.Schema.Value, srcSchemaFresh)
	}

	plan.Auth.Source = citeFor(plan.Auth.Value, srcAuthCC)

	if plan.Connectors.Kind != ConnectorsKindNone && plan.Connectors.Kind != ConnectorsKindNotAssessed {
		plan.Connectors.Source = citeFor(plan.Connectors.Value, srcConnectors)
	}

	plan.Networking.Source = networkingSource(plan.Networking.Value, tc)

	// Docs on the remaining recommendations, where a public page applies. citeFor
	// suppresses the link on a withheld plan or a "nothing to do / not assessed"
	// value. Sizing hinges on cluster-type capacity (eCKU, per-tier caps), so it
	// cites the cluster-types page; topics cites topic management on Confluent Cloud
	// (settings, replication factor, retention); historical-data backfill is a
	// Cluster Linking topic.
	plan.Sizing.Source = citeFor(plan.Sizing.Value, srcClusterType)
	plan.Topics.Source = citeFor(plan.Topics.Value, srcTopics)
	// Backfill doc follows the data-movement mechanism: Replicator on that path,
	// Cluster Linking otherwise (so a Replicator cluster doesn't link to CL docs).
	switch {
	case plan.Switchover.Replicator:
		plan.HistoricalData.Source = citeFor(plan.HistoricalData.Value, srcReplicator)
	case plan.Switchover.StartFresh:
		// Start fresh copies no data over no cluster link, so there is no backfill doc to
		// cite; the Cluster Linking migrate doc would be misleading here.
		plan.HistoricalData.Source = ""
	default:
		plan.HistoricalData.Source = citeFor(plan.HistoricalData.Value, srcSwitchoverCL)
	}

	if plan.Switchover.Replicator {
		plan.Switchover.Source = citeFor(plan.Switchover.Value, srcReplicator)
	} else {
		plan.Switchover.Source = citeFor(plan.Switchover.Value, srcSwitchoverCL)
	}
	if plan.Switchover.Alternative != nil {
		if plan.Switchover.Alternative.Replicator {
			plan.Switchover.Alternative.Source = citeFor(plan.Switchover.Alternative.Value, srcReplicator)
		} else {
			plan.Switchover.Alternative.Source = citeFor(plan.Switchover.Alternative.Value, srcSwitchoverCL)
		}
	}
}

// isOpenVerdict reports whether a verdict is still open (no answer yet).
func isOpenVerdict(value string, held bool) bool {
	return held || contains(openVerdictValues, value)
}

// networkingSource keys the citation by cloud and by which endpoint the value
// names. Not gated on withheld.
func networkingSource(value, tc string) string {
	switch {
	case strings.Contains(value, "Egress PrivateLink"):
		if tc == "Azure" {
			return srcNetworkingEgressPrivateLinkAz
		}
		return srcNetworkingEgressPrivateLink
	case strings.Contains(value, "PNI"):
		return srcNetworkingPni
	case strings.Contains(value, "PrivateLink") || strings.Contains(value, "Private Service Connect"):
		switch tc {
		case "Azure":
			return srcNetworkingPrivateLinkAzure
		case "GCP":
			return srcNetworkingPrivateLinkGcp
		default:
			return srcNetworkingPrivateLinkAws
		}
	default:
		return srcNetworking
	}
}
