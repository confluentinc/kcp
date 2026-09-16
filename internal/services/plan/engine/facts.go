package engine

// Exported fact predicates over a Profile, for callers (e.g. the Open-Question
// layer) that need to evaluate a question's conditional visibility the same way
// the engine does. Thin wrappers over the internal logic so there is one source
// of truth.

// WillBePrivate reports whether the plan lands on private networking — either
// because the customer requires it, or because the workload crosses to Enterprise
// from the public-acceptable path (size, mTLS, or a declared Standard breach).
func WillBePrivate(p Profile) bool { return willBePrivate(p) }

// IsServerless reports whether the source is MSK Serverless.
func IsServerless(p Profile) bool { return p.isServerless() }

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
	ct := clusterType(p, sizingBand(p), targetCloud(p), nil)
	return ct.CrossedToPrivate
}
