package engine

// Data-migration mechanism resolution. The full switchover verdict is built on
// top of this; networking needs only the one fact resolveMechanism settles —
// whether the migration would ever pull data over a Cluster Link — so these
// helpers live here, shared by both blocks.

// Connection-method vocabulary (how the customer reaches their cluster today).
const (
	connectsSameVPC     = "Same VPC"
	connectsPeered      = "Peered"
	connectsPrivateLink = "PrivateLink"
	connectsOther       = "Other"
)

// connectsToday reads the stated connection method, normalising a legacy
// source_topology answer to the closest mechanism when the direct field is
// unset.
func connectsToday(p Profile) string {
	if p.ConnectsToday != "" {
		return p.ConnectsToday
	}
	t := p.SourceTopology
	switch {
	case t == "":
		return ""
	case t == "Single VPC":
		return connectsSameVPC
	case containsAnyFold(t, "peer", "hub", "transit gateway"):
		return connectsPeered
	default:
		return connectsOther
	}
}

// clusterLinkingAvailable — Cluster Linking needs a Dedicated or Enterprise
// destination.
func clusterLinkingAvailable(tier Tier) bool {
	return tier == TierEnterprise || tier == TierDedicated
}

// needsDataMigration — data comes with the workload unless the customer
// explicitly says No.
func needsDataMigration(p Profile) bool {
	return p.NeedsDataMigration != "No"
}

// mechanismResult names how data moves, and (for Replicator) why.
type mechanismResult struct {
	Mechanism string // "start-fresh" | "jump-cluster" | "replicator" | "cluster-linking"
	Why       string // for replicator: "serverless-tier" | "tier" | "ibp" | "kafka-version"
}

// resolveMechanism decides how existing data moves. needsDataAnswer "" means
// "unset" and falls back to needs_data_migration; any other value settles it
// directly (anything but "No" is "needs data").
func resolveMechanism(p Profile, tier Tier, needsDataAnswer string) mechanismResult {
	needsData := needsDataMigration(p)
	if needsDataAnswer != "" {
		needsData = needsDataAnswer != "No"
	}
	// Cluster Linking floor: below Kafka 2.4, OR 2.4-2.9 with inter-broker
	// protocol < 2.8 (IBP is assumed ≥ 2.8 unless the user says otherwise).
	ibpBelow := p.KafkaVersion == "2.4-2.9" && p.InterBrokerProtocol == "No"
	belowFloor := p.KafkaVersion == "Older than 2.4" || ibpBelow

	if !needsData {
		return mechanismResult{Mechanism: "start-fresh"}
	}
	// A government cloud target has no Cluster Linking at all, so existing data
	// moves over Replicator — matching what schemaDecision and MigrationInfraDecision
	// already do for gov. (Latent: TargetIsGovCloud has no intake path yet, so this
	// is never reached today; the guard keeps switchover consistent if it ever is.)
	if isGovCloud(p) {
		return mechanismResult{Mechanism: "replicator", Why: "gov"}
	}
	if p.isServerless() {
		if tier != "" && clusterLinkingAvailable(tier) {
			return mechanismResult{Mechanism: "jump-cluster"}
		}
		return mechanismResult{Mechanism: "replicator", Why: "serverless-tier"}
	}
	if tier != "" && !clusterLinkingAvailable(tier) {
		return mechanismResult{Mechanism: "replicator", Why: "tier"}
	}
	if belowFloor {
		why := "kafka-version"
		if ibpBelow {
			why = "ibp"
		}
		return mechanismResult{Mechanism: "replicator", Why: why}
	}
	return mechanismResult{Mechanism: "cluster-linking"}
}

// mechanismUsesClusterLink — the one fact infra needs from switchover logic:
// would this ever pull data over a Cluster Link? Both Cluster Linking proper and
// the Serverless jump cluster are links underneath; Replicator and start-fresh
// are not.
func mechanismUsesClusterLink(p Profile, tier Tier, needsDataAnswer string) bool {
	m := resolveMechanism(p, tier, needsDataAnswer)
	return m.Mechanism == "cluster-linking" || m.Mechanism == "jump-cluster"
}

// clusterLinkNeedsPrivateEgress reports whether the migration Cluster Link needs a
// private outbound path to the source: it moves data over a link AND the source is
// reached privately. A public source is reached over the internet, so no Egress
// PrivateLink Endpoint (or GCP egress PSC) is added for the link — keeping the
// networking verdict consistent with the migration-infra type (public source =>
// type 1, no private networking to the source).
func clusterLinkNeedsPrivateEgress(p Profile, tier Tier, needsDataAnswer string) bool {
	return mechanismUsesClusterLink(p, tier, needsDataAnswer) && p.SourcePublicAccess != "Yes"
}
