package engine

// Confluent product-fact constants. Every band edge below is a published
// Confluent capacity ceiling read off the tier tables and the eCKU caps — no
// rate, ratio, or price appears here or anywhere in this package. Cost never
// reaches the engine.

// eCKU caps (PRD: 3,000 partitions per eCKU; PrivateLink caps at 10 eCKU on
// Enterprise, PNI at 32).
const (
	eCKUPartitions     = 3000
	privateLinkCapECKU = 10
	pniCapECKU         = 32
)

// Tier is a Confluent Cloud cluster tier.
type Tier string

const (
	TierBasic      Tier = "Basic"
	TierStandard   Tier = "Standard"
	TierEnterprise Tier = "Enterprise"
	TierDedicated  Tier = "Dedicated"
)

// tierCapacity is one row of the published "maximum limits per cluster" table.
type tierCapacity struct {
	IngressMbps    int
	EgressMbps     int
	Partitions     int
	RequestsPerSec int
	ACLs           int
	Connections    int
	ECKU           int
}

// tierMax — published per-cluster maxima (docs.confluent.io cluster-types, the
// "maximum limits per cluster" table). CAPACITY ceilings, not price crossovers.
var tierMax = map[Tier]tierCapacity{
	TierBasic:      {IngressMbps: 250, EgressMbps: 750, Partitions: 1500, RequestsPerSec: 5000, ACLs: 1000, Connections: 1000, ECKU: 50},
	TierStandard:   {IngressMbps: 250, EgressMbps: 750, Partitions: 2500, RequestsPerSec: 15000, ACLs: 1000, Connections: 10000, ECKU: 10},
	TierEnterprise: {IngressMbps: 1920, EgressMbps: 5760, Partitions: 96000, RequestsPerSec: 240000, ACLs: 4000, Connections: 576000, ECKU: 32},
	TierDedicated:  {IngressMbps: 9120, EgressMbps: 27360, Partitions: 100000, RequestsPerSec: 2280000, ACLs: 10000, Connections: 2736000, ECKU: 252},
}

// eCKUCapacity — per-unit capacity ("limits for a single billing unit"). Note
// how far apart the tiers sit per eCKU: a Standard eCKU carries 250 partitions,
// an Enterprise one carries 3,000.
var eCKUCapacity = map[Tier]tierCapacity{
	TierBasic:      {IngressMbps: 5, EgressMbps: 15, Partitions: 30, RequestsPerSec: 100},
	TierStandard:   {IngressMbps: 25, EgressMbps: 75, Partitions: 250, RequestsPerSec: 1500},
	TierEnterprise: {IngressMbps: 60, EgressMbps: 180, Partitions: eCKUPartitions, RequestsPerSec: 7500},
}

// dim identifies a sizing dimension. The string values are the customer-facing
// driver labels ("request rate", not "requestsPerSec") because they surface
// directly in the sizing copy.
type dim string

const (
	dimPartitions  dim = "partitions"
	dimIngress     dim = "ingress"
	dimEgress      dim = "egress"
	dimRequestRate dim = "request rate"
)

// bandEdges — two edges, three bands, per dimension. Edge 1→2 is Standard's
// published ceiling; edge 2→3 is Enterprise at 10 eCKU (the PrivateLink cap).
// Derived from tierMax and eCKUCapacity so sizing and networking run off one
// scale rather than two.
var bandEdges = map[dim][]int{
	dimPartitions:  {tierMax[TierStandard].Partitions, privateLinkCapECKU * eCKUPartitions},
	dimIngress:     {tierMax[TierStandard].IngressMbps, privateLinkCapECKU * eCKUCapacity[TierEnterprise].IngressMbps},
	dimEgress:      {tierMax[TierStandard].EgressMbps, privateLinkCapECKU * eCKUCapacity[TierEnterprise].EgressMbps},
	dimRequestRate: {tierMax[TierStandard].RequestsPerSec, privateLinkCapECKU * eCKUCapacity[TierEnterprise].RequestsPerSec},
}

// bandXL is the band above the top edge — above the Enterprise cap. Derived,
// never typed: len(edges)+1.
const bandXL = 3 // == len(bandEdges[dimPartitions]) + 1; asserted in sizing_test.go

// sharedTierMaxBand is the highest band a shared (Basic/Standard) tier can serve
// on a public endpoint — the band whose upper edge IS Standard's published
// ceiling. Asserted, not hand-tuned.
const sharedTierMaxBand = 1

// privateLinkCapBand — band above which a workload exceeds Enterprise's 10 eCKU
// PrivateLink cap. Equal to bandXL on the default path.
const privateLinkCapBand = 3

// bandLabels — the categorical option strings the intake UI offers, ascending by
// band. Kept for the banded-label path (a customer/plan-inputs answer given as a
// label rather than a raw number).
var bandLabels = map[dim][]string{
	dimPartitions:  {"Under 2,500", "2,500–30,000", "Over 30,000"},
	dimIngress:     {"Under 250 MBps", "250–600 MBps", "Over 600 MBps"},
	dimEgress:      {"Under 750 MBps", "750–1,800 MBps", "Over 1,800 MBps"},
	dimRequestRate: {"Under 15,000/s", "15,000–75,000/s", "Over 75,000/s"},
}

// legacyLabelBands maps superseded answer labels to the band that preserves
// their original DECISION (positional mapping cannot, once the band count
// changes), so older plan-inputs still resolve to the right band.
var legacyLabelBands = map[dim]map[string]int{
	dimPartitions: {
		"Under 600": 1, "600–2,500": 1,
		"Under 3,000": 2, "3,000–30,000": 2,
		"Under 2,500":   1,
		"600–30,000":    2,
		"Under 30,000":  2,
		"30,000–96,000": 3, "Over 96,000": 3,
	},
	dimIngress: {
		"Under 25 MB/s": 1, "25–250 MB/s": 1,
		"Under 20 MB/s": 1, "20–600 MB/s": 2,
		"25–600 MB/s": 2, "Under 600 MB/s": 2,
		"Under 250 MB/s": 1, "250–600 MB/s": 2, "600–1,920 MB/s": 3, "Over 1,920 MB/s": 3,
	},
	dimEgress: {
		"Under 28 MB/s": 1, "28–750 MB/s": 1,
		"Under 60 MB/s": 1, "60–1,800 MB/s": 2,
		"28–1,800 MB/s": 2, "Under 1,800 MB/s": 2,
		"Under 750 MB/s": 1, "750–1,800 MB/s": 2, "1,800–5,760 MB/s": 3, "Over 5,760 MB/s": 3,
	},
	dimRequestRate: {
		"Under 3,500/s": 1, "3,500–15,000/s": 1,
		"15,000–150,000/s": 3, "150,000–500,000/s": 3, "Over 500,000/s": 3,
		"3,500–75,000/s":   2,
		"75,000–240,000/s": 3, "Over 240,000/s": 3,
	},
}

// MSK source cluster types.
const (
	MSKProvisioned = "Provisioned"
	MSKServerless  = "Serverless"
)

// Source auth method strings. These are the vocabulary the intake offers and
// what the profile carries; the mTLS value gates the cluster type.
const (
	authMTLS   = "TLS client certificates (mTLS)"
	authAWSIAM = "AWS IAM"
	authSCRAM  = "SASL/SCRAM"
	authUnauth = "None / plaintext"
)

// tierSLA — the uptime SLAs each tier offers, ascending. Basic and Standard are
// disjoint (Basic offers only 99.5%, Standard's floor is 99.9%), which is what
// makes the choice between them an availability commitment, not a preference.
var tierSLA = map[Tier][]string{
	TierBasic:      {"99.5%"},
	TierStandard:   {"99.9%", "99.99%"},
	TierEnterprise: {"99.9%", "99.99%"},
	TierDedicated:  {"99.95%", "99.99%"},
}

// tierCaps describes what a tier can do on the network: whether it serves a
// public and/or private endpoint, and whether it is reachable self-serve.
type tierCaps struct {
	Public    bool
	Private   bool
	SelfServe bool
}

// tierCapabilities — the private/public split as a real capability, not prose.
// Enterprise is private-only; Basic/Standard are public-only; Dedicated does
// both but is never self-serve. PNI is Enterprise-only (and AWS-only, enforced
// in the networking decision).
var tierCapabilities = map[Tier]tierCaps{
	TierBasic:      {Public: true, Private: false, SelfServe: true},
	TierStandard:   {Public: true, Private: false, SelfServe: true},
	TierEnterprise: {Public: false, Private: true, SelfServe: true},
	TierDedicated:  {Public: true, Private: true, SelfServe: false},
}
