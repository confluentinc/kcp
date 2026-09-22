package engine

// MigrationInfraChoice is the migration-infrastructure topology decision: which
// `create-asset migration-infra --type N` fits the source's accessibility and
// authentication, given the target tier. Type 0 means no standard type fits and a
// specialist designs it. This is a pure decision (no I/O, no scan types); the plan
// layer renders the concrete command from it.
type MigrationInfraChoice struct {
	Type            int    `json:"type"` // 1-5, or 0 when undetermined
	Label           string `json:"label"`
	Rationale       string `json:"rationale"`
	Alternative     string `json:"alternative,omitempty"`      // an alternative topology worth noting
	AlternativeType int    `json:"alternative_type,omitempty"` // the --type N of that alternative
	// JumpCluster is true for types 4 and 5 (a jump cluster in the customer VPC),
	// which need extra provisioning inputs the plain cluster-link types do not.
	JumpCluster bool `json:"jump_cluster,omitempty"`
	// SpecialistClusterLinkable applies only to a Type-0 (specialist-designed) outcome:
	// true when Cluster Linking is available so the standard Cluster Linking cutover
	// steps still apply and only the link's authentication is designed with a specialist
	// (mTLS / undetected source auth on a supported tier); false when Cluster Linking
	// isn't available at all (Confluent Cloud for Government). It lets the render pick
	// the right specialist path from a typed field instead of re-deriving the product
	// rule. Not serialized (a render discriminator, not part of the plan record).
	SpecialistClusterLinkable bool `json:"-"`
}

// migration-infra type numbers, named so the decision reads clearly.
const (
	miPublicSCRAM      = 1 // public source, SASL/SCRAM cluster link
	miPrivateOutSCRAM  = 2 // private source, external outbound cluster link (SASL/SCRAM), Enterprise-only
	miPrivateOutPlain  = 3 // private source, external outbound cluster link (unauthenticated), Enterprise-only
	miPrivateJumpSCRAM = 4 // private source, jump cluster (SASL/SCRAM)
	miPrivateJumpIAM   = 5 // private source, jump cluster (IAM), MSK only
)

// MigrationInfraDecision selects the migration-infra topology from source public
// access, source auth, and the target tier.
//
// The rules mirror the create-asset constraints:
//   - A government target has no Cluster Linking, so nothing is auto-mapped.
//   - A Dedicated target is a specialist-led migration, so it is not auto-mapped
//     (a Dedicated recommendation already routes the whole plan to a specialist).
//   - A public source uses a direct SASL/SCRAM cluster link (type 1).
//   - A private (Enterprise) source is reached by an external outbound link
//     (type 2 SASL/SCRAM, type 3 unauthenticated) or a jump cluster (type 5 for
//     IAM, or type 4 SASL/SCRAM as a fallback).
//   - SASL/SCRAM is preferred over AWS IAM for the link (IAM cannot cross a link
//     directly and needs a jump cluster).
func MigrationInfraDecision(p Profile, tier Tier) MigrationInfraChoice {
	if p.TargetIsGovCloud == "Yes" {
		// Government has no Cluster Linking at all — the specialist designs the whole path.
		return specialistInfra("Confluent Cloud for Government does not offer Cluster Linking, which every migration-infra type relies on, so we design the migration path with a specialist.", false)
	}
	if tier == TierDedicated {
		// Plan-layer-unreachable (a Dedicated plan is withheld before infra is mapped);
		// kept as a tested contract of this exported pure function.
		return specialistInfra("A Dedicated target is a specialist-led migration, so the migration infrastructure is designed with you rather than auto-mapped.", false)
	}

	if p.SourcePublicAccess == "Yes" {
		return MigrationInfraChoice{
			Type:      miPublicSCRAM,
			Label:     "Public source endpoints with SASL/SCRAM cluster link",
			Rationale: "Your source exposes public broker endpoints, so the cluster link reaches it over the public internet using your source's SASL/SCRAM credentials, with no private networking to stand up.",
		}
	}

	switch {
	case authHas(p, authSCRAM):
		return MigrationInfraChoice{
			Type:            miPrivateOutSCRAM,
			Label:           "Private source with external outbound cluster link (SASL/SCRAM)",
			Rationale:       "Your source is private and its brokers use SASL/SCRAM, so the cluster link reaches it privately, over an external outbound endpoint, using your source's SASL/SCRAM credentials.",
			Alternative:     "a jump cluster in your " + privateNetNoun(p) + " (SASL/SCRAM), if Confluent Cloud can't reach your brokers over an egress endpoint",
			AlternativeType: miPrivateJumpSCRAM,
		}
	case authHas(p, authUnauth):
		// Confluent Cloud Cluster Linking has no unauthenticated path (it needs an
		// authenticated, TLS-based source connection), so an unauthenticated source cannot be
		// linked over plaintext. Add a SASL/SCRAM listener for the link (type 2), jump cluster
		// (type 4) as the alternative; the source's clients also need an auth method on CC after
		// cutover. (Public Confluent Cloud docs never list PLAINTEXT as a source-link protocol.)
		return MigrationInfraChoice{
			Type:            miPrivateOutSCRAM,
			Label:           "Private source with external outbound cluster link (SASL/SCRAM)",
			Rationale:       "Your source is private and unauthenticated, so add a SASL/SCRAM listener on it for the migration link to use — Confluent Cloud Cluster Linking has no unauthenticated path, so the link needs an authenticated SASL/SCRAM connection. Your clients also need a supported auth method on Confluent Cloud after cutover, which has no unauthenticated equivalent.",
			Alternative:     "a jump cluster in your " + privateNetNoun(p) + " (SASL/SCRAM), if you would rather not add a SASL/SCRAM listener to your source",
			AlternativeType: miPrivateJumpSCRAM,
		}
	case authHas(p, authAWSIAM):
		choice := MigrationInfraChoice{
			Type:        miPrivateJumpIAM,
			Label:       "Private source with jump cluster (IAM)",
			Rationale:   "Your source is private and authenticates with AWS IAM, which a cluster link can't carry directly, so a jump cluster in your VPC bridges IAM to Confluent Cloud. The jump cluster reads MSK over your existing IAM, so your production source stays untouched.",
			JumpCluster: true,
		}
		// MSK Serverless is IAM-only, so you cannot add a SASL/SCRAM listener; that
		// alternative only exists for Provisioned MSK.
		if !p.isServerless() {
			choice.Alternative = "add a SASL/SCRAM listener on your MSK cluster and use a direct external outbound cluster link (SASL/SCRAM) — less infrastructure, but it enables SASL/SCRAM on your production cluster"
			choice.AlternativeType = miPrivateOutSCRAM
		}
		return choice
	case authHas(p, authMTLS):
		return scramListenerLinkChoice(p, "mTLS", "mTLS certificates")
	case authHas(p, authSASLPlain):
		return scramListenerLinkChoice(p, "SASL/PLAIN", "SASL/PLAIN credentials")
	default:
		// Genuinely-undetected source auth on a supported tier: Cluster Linking IS
		// available, only the link's authentication is designed with a specialist, so the
		// standard Cluster Linking cutover steps still apply.
		return specialistInfra("Your source authentication isn't detected, so we design the migration link with you; the standard Cluster Linking cutover steps still apply.", true)
	}
}

// scramListenerLinkChoice is the migration-infra for a private source whose auth
// (mTLS or SASL/PLAIN) kcp's generated link cannot carry directly. kcp's migration-infra
// links over SASL/SCRAM only — there is no mTLS or SASL/PLAIN link type (create-asset's
// MigrationType has no such value, and the generated link hardcodes ScramLoginModule) —
// so an <auth>-only source adds a SASL/SCRAM listener for the link to use while its
// application clients keep their existing auth. A jump cluster (type 4) is the
// alternative for anyone who would rather not touch the source. (Confluent Cloud Cluster
// Linking does support mTLS/PLAIN sources natively, but kcp has no create-asset command
// that emits such a link, so it is not offered as an auto-mapped type.)
func scramListenerLinkChoice(p Profile, authName, keepNoun string) MigrationInfraChoice {
	return MigrationInfraChoice{
		Type:            miPrivateOutSCRAM,
		Label:           "Private source with external outbound cluster link (SASL/SCRAM)",
		Rationale:       "Your source is private and authenticates with " + authName + ", so if it is " + authName + "-only, add a SASL/SCRAM listener for the migration link to use — kcp's migration infrastructure links over SASL/SCRAM, and your application clients keep their " + keepNoun + ". The link then reaches your source privately over an external outbound endpoint.",
		Alternative:     "a jump cluster in your " + privateNetNoun(p) + " (SASL/SCRAM), if you would rather not add a SASL/SCRAM listener to your source",
		AlternativeType: miPrivateJumpSCRAM,
	}
}

// privateNetNoun is the customer's private-network noun for the target cloud —
// VPC (AWS), VNet (Azure), VPC network (GCP) — matching the render layer's
// privateNetworkNoun. MSK (always AWS) resolves to VPC, so MSK copy is unchanged.
func privateNetNoun(p Profile) string {
	switch targetCloud(p) {
	case "Azure":
		return "VNet"
	case "GCP":
		return "VPC network"
	default:
		return "VPC"
	}
}

func specialistInfra(reason string, clusterLinkable bool) MigrationInfraChoice {
	return MigrationInfraChoice{Type: 0, Label: "Determined with a specialist", Rationale: reason, SpecialistClusterLinkable: clusterLinkable}
}

// IsJumpClusterType reports whether a migration-infra type runs a jump cluster
// (types 4 and 5), which need extra provisioning inputs the direct-link types
// (1, 2, 3) do not. Used to render the right follow-up for an alternative type.
func IsJumpClusterType(t int) bool { return t == miPrivateJumpSCRAM || t == miPrivateJumpIAM }
