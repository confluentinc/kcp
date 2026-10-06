package engine

// Exported fact predicates over a Profile, for callers (e.g. the Open-Question
// layer) that need to evaluate a question's conditional visibility the same way
// the engine does. Thin wrappers over the internal logic so there is one source
// of truth.

// WillBePrivate reports whether the plan lands on private networking — either
// because the customer requires it, or because the workload crosses to Enterprise
// from the public-acceptable path (size, mTLS, or a declared Standard breach).
func WillBePrivate(p Profile) bool { return willBePrivate(p) }

// RequiresPrivate reports whether the customer's networking requirement is
// private (public endpoints not acceptable). Unlike WillBePrivate it ignores a
// cross to private by size, mTLS, or a Standard breach.
func RequiresPrivate(p Profile) bool { return requiresPrivate(p) }

// IsOnPremSource reports whether the source runs on-premises or outside any cloud
// network, so cloud-network connection options (VPC peering, PrivateLink) don't apply.
func IsOnPremSource(p Profile) bool { return p.SourceCloud == "On-prem or other" }

// IsServerless reports whether the source is MSK Serverless.
func IsServerless(p Profile) bool { return p.isServerless() }

// ClusterLinkingFloor names the Cluster Linking version floor that applies to a
// source type: Kafka 2.4 for MSK and Apache Kafka, Confluent Platform 5.4 for
// Confluent Platform, and the full list when the source type is unknown.
func ClusterLinkingFloor(t SourceType) string {
	switch t {
	case SourceMSK, SourceApacheKafka:
		return "Kafka 2.4 and inter-broker protocol (IBP) 2.8"
	case SourceConfluentPlatform:
		return "Confluent Platform 5.4 and inter-broker protocol (IBP) 2.8"
	}
	return "Kafka 2.4, Confluent Platform 5.4, inter-broker protocol (IBP) 2.8"
}

// IsMSK reports whether the source is Amazon MSK (the zero-value SourceType is
// MSK). Used by the question layer to gate MSK-only questions.
func IsMSK(p Profile) bool { return p.isMSK() }

// IsOSKorCP reports whether the source is self-managed Apache Kafka or Confluent
// Platform. Used by the question layer to gate the OSK/CP-only questions.
func IsOSKorCP(p Profile) bool { return p.isOSKorCP() }

// HasBackfillableHistory reports whether there is enough retained history to make
// the consumer-history question and a backfill plan worth surfacing.
func HasBackfillableHistory(p Profile) bool { return hasBackfillableHistory(p) }

// Band returns the sized band (1..BandXL) for the profile.
func Band(p Profile) int { return sizingBand(p).Band }

// BandXL is the top band, above the Enterprise cap (a handoff).
const BandXL = bandXL

// SharedTierMaxBand is the highest band a shared tier can serve.
const SharedTierMaxBand = sharedTierMaxBand

// TargetCloudOf resolves the destination cloud (defaults to source/AWS).
func TargetCloudOf(p Profile) string { return targetCloud(p) }

// StandardLimitsQuestion / EnterpriseLimitsQuestion are the generated
// tier-ceiling questions (named from the tier tables so they cannot drift).
func StandardLimitsQuestion() string   { return standardLimitsQuestion() }
func EnterpriseLimitsQuestion() string { return enterpriseLimitsQuestion() }

// willBePrivate: private required, or crossed to private from the public path.
func willBePrivate(p Profile) bool {
	if requiresPrivate(p) {
		return true
	}
	// GCP's outbound private connection is Dedicated-only, so a connectors-egress Yes
	// forces a private plan even for a customer who accepts public endpoints.
	// clusterType cannot report that crossing (networkingDecision escalates the tier
	// afterwards), so it is read here from the same answer.
	if targetCloud(p) == "GCP" && p.CCEgressRequired == "Yes" {
		return true
	}
	ct := clusterType(p, sizingBand(p), targetCloud(p), nil)
	return ct.CrossedToPrivate
}
