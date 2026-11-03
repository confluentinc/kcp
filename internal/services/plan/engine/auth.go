package engine

import "strings"

// Shared predicates read by more than one decision block: target cloud, source
// auth methods, the mTLS signals, and the private-networking requirement.

// targetCloud resolves the destination cloud. MSK is AWS, so an MSK source with
// no explicit target defaults to AWS.
func targetCloud(p Profile) string {
	if p.TargetCloud != "" {
		return p.TargetCloud
	}
	// Fall back to the source cloud as the default target — but only when the source
	// itself runs in a public cloud. An on-prem / "other" source names no valid target
	// cloud, so it defaults to AWS (Confluent Cloud's standard default) rather than
	// leaking "On-prem or other" into the target and its networking vocabulary.
	switch p.SourceCloud {
	case "AWS", "Azure", "GCP":
		return p.SourceCloud
	case "On-prem or other":
		return "AWS"
	}
	if p.SourcePlatform == "Amazon MSK" {
		return "AWS"
	}
	return ""
}

// sourceAuthMethods returns the source auth methods. Serverless supports exactly
// one (AWS IAM), so it is forced rather than read — a profile must not describe a
// cluster that cannot exist.
func sourceAuthMethods(p Profile) []string {
	if p.isServerless() {
		return []string{authAWSIAM}
	}
	return p.SourceAuthTypes
}

func authHas(p Profile, method string) bool {
	for _, m := range sourceAuthMethods(p) {
		if m == method {
			return true
		}
	}
	return false
}

// authPreservable reports whether Confluent Cloud can preserve the source auth
// as-is — only mTLS (OAuth is not modelled as a source method here).
func authPreservable(p Profile) bool {
	return authHas(p, authMTLS)
}

// targetHasMtls reports whether mTLS was chosen as the target identity model.
func targetHasMtls(p Profile) bool {
	for _, m := range p.TargetIdentityModel {
		if m == "mTLS" {
			return true
		}
	}
	return false
}

// mtlsNeeded fires only on a STATED answer: the source already uses mTLS, or mTLS
// was picked as the target identity model. mTLS on Confluent Cloud is Enterprise
// and Dedicated only, so this gates the cluster type. An unanswered auth question
// invents nothing.
func mtlsNeeded(p Profile) bool {
	return authHas(p, authMTLS) || targetHasMtls(p)
}

// requiresPrivate asks for the hard requirement, not the current state (everyone
// on MSK is private today, so "what do you use now?" returns ~100% private).
// public_endpoints_ok is read first; requires_private and the legacy
// source_accessibility follow. Unanswered defaults to private.
func requiresPrivate(p Profile) bool {
	switch p.PublicEndpointsOK {
	case "Yes":
		return false
	case "No":
		return true
	}
	switch p.RequiresPrivateField {
	case "Yes":
		return true
	case "No":
		return false
	}
	return p.SourceAccessibility != "Public"
}

// privateAnsweredField reports whether the public/private fork was actually
// answered (rather than defaulted).
func privateAnsweredField(p Profile) bool {
	return p.PublicEndpointsOK == "Yes" || p.PublicEndpointsOK == "No" ||
		p.RequiresPrivateField == "Yes" || p.RequiresPrivateField == "No" ||
		p.SourceAccessibility == "Private" || p.SourceAccessibility == "Public"
}

// ── Block 4 — Auth ──────────────────────────────────────────────────────────

// How data moves, which decides how existing source credentials are handled.
const (
	viaLink       = "cluster-link"
	viaReplicator = "replicator"
	viaNone       = "none"
)

// replicationVia reads the switchover verdict to classify how data moves. A nil
// verdict (or a held one) defaults to Cluster Linking.
func replicationVia(sw *SwitchoverResult) string {
	if sw == nil {
		return viaLink
	}
	if sw.Replicator {
		return viaReplicator
	}
	if sw.StartFresh {
		return viaNone
	}
	return viaLink
}

// sourceAuthHandling is the per-method note on how the customer's existing source
// credentials are used during data movement, given the mechanism.
func sourceAuthHandling(method string, p Profile, via string, lp *linkPath, gateway bool) CredHandling {
	// This used to restate the whole IAM->RBAC story inline on every AWS IAM auth
	// path. That content now lives once, on authDecision's access-control notes
	// (accessControlNotes below), which settle with infra rather than any one
	// application's switchover mechanism. Kept as a short pointer so an IAM
	// source's per-method note doesn't say the same thing twice.
	const iamMap = " Your plan's authentication recommendation covers how to recreate your AWS IAM permissions in Confluent Cloud."

	if via == viaNone {
		note := "Nothing is replicated, so your source credentials aren't involved; your clients get new Confluent Cloud credentials when you point them over."
		if method == authAWSIAM {
			note += iamMap
		}
		return CredHandling{Method: method, Note: note}
	}

	if via == viaReplicator {
		switch method {
		case authAWSIAM:
			return CredHandling{Method: method, Note: "No change to your MSK authentication. Replicator runs on Kafka Connect in your own account, so it reaches MSK with your existing AWS IAM credentials. No SCRAM listener and no jump cluster are needed on this plan." + iamMap}
		case "None / plaintext":
			if gateway {
				return CredHandling{Method: method, Note: GatewayAnonymousACLSentence}
			}
			return CredHandling{Method: method, Note: "Move these clients to a supported auth method before cutover. Confluent Cloud has no unauthenticated equivalent."}
		case authKerberos:
			// Replicator runs on Kafka Connect in the customer's own environment, so it
			// reads the source with the existing Kerberos keytab — unlike Cluster Linking,
			// which cannot use Kerberos at all (see the viaLink case below).
			return CredHandling{Method: method, Note: "No change. Replicator runs on Kafka Connect in your own environment and reads your source with your existing Kerberos keytab."}
		}
		label := "credentials"
		switch method {
		case "SASL/SCRAM":
			label = "SCRAM credentials"
		case authMTLS:
			label = "mTLS certificates"
		case "API keys (SASL/PLAIN)":
			label = "SASL/PLAIN credentials"
		}
		return CredHandling{Method: method, Note: "No change. Replicator runs in your own account and reads your source with your existing " + label + "."}
	}

	// via == viaLink. The link path comes from MigrationInfraDecision (lp), the same
	// decision the migration-infra step renders, so these notes can't disagree with it.
	switch method {
	case "SASL/SCRAM":
		return CredHandling{Method: method, Note: "No change. Cluster Linking uses your SCRAM credentials as-is. " + linkACLSentence}
	case authMTLS:
		// The migration link signs in over SASL/SCRAM (kcp's migration-infra has no mTLS
		// link type, although Cluster Linking itself supports mTLS).
		const lead = "Cluster Linking itself can sign in with mTLS, but the migration link kcp generates signs in over SASL/SCRAM"
		return CredHandling{Method: method, Note: lp.note(p, lead, "if your source is mTLS-only", "Your application clients keep their mTLS certificates unchanged")}
	case "API keys (SASL/PLAIN)":
		// kcp's generated link authenticates with SASL/SCRAM (ScramLoginModule), not PLAIN.
		const lead = "Cluster Linking can sign in with SASL/PLAIN, but the link kcp generates signs in over SASL/SCRAM only."
		const keep = "Your application clients keep their SASL/PLAIN credentials unchanged."
		switch {
		case lp.specialist && lp.existingListener(p) != "":
			return CredHandling{Method: method, Note: "The link signs in with your existing " + lp.existingListener(p) + ". " + keep}
		case lp.specialist:
			return CredHandling{Method: method, Note: lead + " The link's sign-in method is designed with you as part of the specialist-led setup. " + keep}
		case authHas(p, authSCRAM):
			return CredHandling{Method: method, Note: keep + " The link signs in over your source's SASL/SCRAM listener."}
		case lp.jump:
			return CredHandling{Method: method, Note: lead + " The link goes through the jump cluster this plan runs, so your source needs no new listener. " + keep}
		case lp.listenerAsked:
			return CredHandling{Method: method, Note: lead + " The link uses the SASL/SCRAM listener described above. " + keep}
		}
		lp.listenerAsked = true
		return CredHandling{Method: method, Note: lead + " Create a SASL/PLAIN link by hand, or add SCRAM to use kcp's generated link. " + keep + " To use kcp's generated link, add a SASL/SCRAM listener and create a SCRAM user for the link, in place before you apply " + MigrationLinkStepRef + "; you can remove the listener after cutover. " + linkACLSentence}
	case "None / plaintext":
		// kcp plans the cluster link with authentication, so the link signs
		// in over SASL/SCRAM; separately, the clients must adopt an auth method on
		// Confluent Cloud after cutover.
		const lead = "The migration link signs in over SASL/SCRAM, because we plan the cluster link with authentication"
		tail := " Separately, your unauthenticated clients need a supported auth method on Confluent Cloud after cutover, which has no unauthenticated equivalent."
		if gateway {
			tail = " " + GatewayAnonymousACLSentence
		}
		switch {
		case lp.specialist && !lp.networkOnly:
			return CredHandling{Method: method, Note: lead + ", so " + specialistLinkNote + "." + tail}
		case authHas(p, authSCRAM):
			return CredHandling{Method: method, Note: lead + ", so it uses your existing SASL/SCRAM listener." + tail}
		case lp.jump:
			return CredHandling{Method: method, Note: lead + ", so it goes through the jump cluster this plan runs and your source needs no new listener." + tail}
		case lp.listenerAsked:
			return CredHandling{Method: method, Note: lead + ", so it uses the SASL/SCRAM listener described above." + tail}
		}
		lp.listenerAsked = true
		return CredHandling{Method: method, Note: lead + ", so add a SASL/SCRAM listener and create a SCRAM user on your source for the link, in place before " + lp.linkStepRef() + "; you can remove that listener after cutover. " + linkACLSentence + tail}
	case authKerberos:
		// A Confluent Cloud (destination-initiated) cluster link cannot use Kerberos to
		// the source at all (Confluent Cloud's broker policy only accepts
		// PLAIN/SCRAM/OAUTHBEARER), so unlike mTLS/PLAIN/unauthenticated there is no
		// "add a listener for the link" fix on the source's own cluster; the listener
		// goes on a cluster only the link uses.
		const lead = "Kerberos can't be used by a cluster link from Confluent Cloud"
		const tail = " Your clients can keep Kerberos until cutover."
		switch {
		case lp.specialist && !lp.networkOnly:
			return CredHandling{Method: method, Note: lead + ", so " + specialistLinkNote + "." + tail}
		case authHas(p, authSCRAM):
			return CredHandling{Method: method, Note: lead + ", so the link signs in with your existing SASL/SCRAM listener instead." + tail}
		case lp.specialist && lp.existingListener(p) != "":
			return CredHandling{Method: method, Note: lead + ", so the link signs in with your existing " + lp.existingListener(p) + " instead." + tail}
		case lp.jump:
			return CredHandling{Method: method, Note: lead + ", so the link goes through the jump cluster this plan runs and your source needs no new listener." + tail}
		case lp.listenerAsked:
			return CredHandling{Method: method, Note: lead + ", so the link signs in with the SASL/SCRAM listener described above." + tail}
		}
		lp.listenerAsked = true
		if lp.specialist {
			// The specialist sets up the link, so kcp's generated link isn't involved.
			return CredHandling{Method: method, Note: lead + ". Add a SASL/SCRAM listener on your source cluster that only the link uses, and create a SCRAM user for the link, in place before " + lp.linkStepRef() + ". " + linkACLSentence + tail}
		}
		return CredHandling{Method: method, Note: lead + ". Add a SASL/SCRAM listener on your source cluster that only the link uses, and create a SCRAM user for the link, in place before you apply " + MigrationLinkStepRef + ". kcp's generated link signs in over SASL/SCRAM only, so choose SCRAM to use it; a SASL/PLAIN or mTLS link is created by hand. " + linkACLSentence + tail}
	case authAWSIAM:
		if lp.specialist {
			return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, so " + specialistLinkNote + "." + iamMap}
		}
		if p.isServerless() {
			return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, which is why this plan runs a jump cluster." + iamMap}
		}
		if authHas(p, authSCRAM) {
			// The source already exposes SASL/SCRAM, so the link uses that path (its note
			// above); nothing on the MSK cluster changes for the migration, and there is no
			// separate IAM step. Existing IAM clients just re-key on Confluent Cloud.
			return CredHandling{Method: method, Note: "Nothing to do for IAM here. The cluster link uses your existing SASL/SCRAM path (above), so your MSK cluster is unchanged for the migration; your IAM clients get new Confluent Cloud credentials when you point them over." + iamMap}
		}
		if !lp.jump {
			// Public MSK endpoints: kcp's jump-cluster types (4 and 5) are for private
			// sources only, so the migration-infra choice is the direct SASL/SCRAM link
			// (type 1) and the source needs a SASL/SCRAM listener for it.
			if lp.listenerAsked {
				return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, and your MSK brokers are public, where kcp's migration infrastructure links over SASL/SCRAM only (its jump cluster is for private sources), so the link uses the SASL/SCRAM listener described above. Your existing IAM clients keep working." + iamMap}
			}
			lp.listenerAsked = true
			return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, and your MSK brokers are public, where kcp's migration infrastructure links over SASL/SCRAM only (its jump cluster is for private sources). Add a SASL/SCRAM listener on your MSK cluster and create a SCRAM user for the link, in place before you apply " + MigrationLinkStepRef + ". MSK runs IAM and SASL/SCRAM side by side, so your existing IAM clients keep working, and you can remove the listener after cutover. " + linkACLSentence + iamMap}
		}
		// Pure IAM source: a cluster link can't carry IAM, so the plan runs a jump cluster
		// (migration-infra type 5). We recommend the jump cluster because it leaves the
		// production source untouched; adding a SASL/SCRAM listener plus a direct link is
		// the alternative.
		jumpEndpoint := "through a PrivateLink VPC endpoint in your AWS account, so it works while your cluster's own networking stays on PNI"
		switch targetCloud(p) {
		case "Azure":
			jumpEndpoint = "through a Private Link endpoint in your network"
		case "GCP":
			jumpEndpoint = "through a Private Service Connect endpoint in your network"
		}
		return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, which is why this plan runs a jump cluster. The alternative is to add a SASL/SCRAM listener on your MSK cluster that only the link uses. A jump cluster connects out to your Confluent Cloud cluster " + jumpEndpoint + "." + iamMap}
	default:
		// An unrecognised method must never assert the link works as-is ("No
		// change.") — Cluster Linking's supported source-auth set is narrow, so
		// silence here would wrongly promise compatibility.
		return CredHandling{Method: method, Note: "Check with your Confluent team whether Cluster Linking can use " + method + "."}
	}
}

// linkPath is how the migration link reaches the source, taken from the chosen
// MigrationInfraDecision so the per-method notes agree with the migration-infra
// step. listenerAsked records that a note already asked for a SASL/SCRAM listener,
// so later notes point back to it instead of repeating the ask.
type linkPath struct {
	jump          bool
	specialist    bool // migration-infra type 0: the link's sign-in is designed with a specialist
	networkOnly   bool // the specialist owns the link's networking only; plaintext and Kerberos sources still need a SCRAM listener
	listenerAsked bool
}

// MigrationLinkStepRef is how a source-credentials note refers to the migration-link
// step, which follows it. plan.md swaps it for the step's number.
const MigrationLinkStepRef = "the migration-link step below"

// linkACLSentence is the source-side access the link's user needs, per the Cluster Linking
// security docs: READ and DESCRIBE_CONFIGS on topics to mirror them, DESCRIBE on consumer groups
// (and topics, which READ covers) for offset sync. DESCRIBE on the cluster is only for ACL sync,
// which the migration link does not do.
const linkACLSentence = "The link's user needs READ and DESCRIBE_CONFIGS on the topics you mirror, and DESCRIBE on the consumer groups."

// linkStepRef is how a note refers to the step that sets up the link: the specialist's, or
// the migration-link step below.
func (lp *linkPath) linkStepRef() string {
	if lp.specialist {
		return "the specialist sets up the cluster link"
	}
	return "you apply " + MigrationLinkStepRef
}

// existingListener names the listener a network-only specialist plan's link signs in with
// when the source has Kerberos beside an mTLS or SASL/PLAIN listener, which a cluster link can
// use. Empty otherwise.
func (lp *linkPath) existingListener(p Profile) string {
	if !lp.networkOnly || !authHas(p, authKerberos) || authHas(p, authSCRAM) {
		return ""
	}
	switch {
	case authHas(p, "API keys (SASL/PLAIN)"):
		return "SASL/PLAIN listener"
	case authHas(p, authMTLS):
		return "mTLS listener"
	}
	return ""
}

const specialistLinkNote = "the link's sign-in method is designed with you as part of the specialist-led setup"

// note is the shared mTLS / SASL/PLAIN note. lead names why the link can't use the
// method as-is, cond is the "if your source is X-only" condition, and keep is the
// sentence about the application clients.
func (lp *linkPath) note(p Profile, lead, cond, keep string) string {
	switch {
	case lp.specialist && lp.existingListener(p) != "":
		return "The link signs in with your existing " + lp.existingListener(p) + ". " + keep + "."
	case lp.specialist:
		return lead + ", so " + specialistLinkNote + ". " + keep + "."
	case authHas(p, authSCRAM):
		return lead + ", which your source already offers. " + keep + "."
	case lp.jump:
		return lead + ", so the link goes through the jump cluster this plan runs and your source needs no new listener. " + keep + "."
	case lp.listenerAsked:
		return lead + ", so the link uses the SASL/SCRAM listener described above. " + keep + "."
	}
	lp.listenerAsked = true
	return lead + ", so " + cond + ", add a SASL/SCRAM listener and create a SCRAM user for the link, in place before you apply " + MigrationLinkStepRef + ". " + keep + ", and the listener is only for the migration — you can remove it after cutover. " + linkACLSentence
}

// sourceCredentialHandling is one note per source auth method, given the
// switchover mechanism and the target tier (which MigrationInfraDecision needs).
// sw may be nil (defaults to the Cluster Linking path).
func sourceCredentialHandling(p Profile, sw *SwitchoverResult, tier Tier) []CredHandling {
	via := replicationVia(sw)
	methods := sourceAuthMethods(p)
	choice := MigrationInfraDecision(p, tier)
	lp := &linkPath{jump: choice.JumpCluster, specialist: choice.Type == 0, networkOnly: choice.Type == 0 && choice.SpecialistClusterLinkable}
	out := make([]CredHandling, 0, len(methods))
	for _, m := range methods {
		out = append(out, sourceAuthHandling(m, p, via, lp, sw != nil && sw.GatewayMediated))
	}
	return out
}

// AuthResult is the infra auth verdict: the TARGET client-auth method(s) only.
type AuthResult struct {
	Value   string `json:"value"`
	Pending bool   `json:"pending,omitempty"` // set at JSON marshal when a deciding question is unanswered; Value is blanked
	Reason  string `json:"reason"`
	Why     string `json:"why"`
	Action  string `json:"action"`
	Source  string `json:"source,omitempty"`
}

// authDecision settles the target client-auth method. target_identity_model is a
// multi-select; empty cascades to preserve mTLS (if the source uses it), else API
// keys. tc is unused (kept for call-site stability).
// accessControlNotes: a cluster link cannot sync ACLs, IAM policies or Confluent
// Platform RBAC role bindings from a non-Confluent-Cloud source, so nothing
// before this told a customer their source's access control had to be
// recreated by hand — clients cut over with valid credentials and hit
// authorization errors. Called from authDecision below, so it settles with
// infra, before any application's switchover mechanism, and reaches every
// plan including start-fresh.
func accessControlNotes(p Profile) []string {
	var notes []string
	methods := sourceAuthMethods(p)
	hasNonIamNonPlaintext := false
	for _, m := range methods {
		if m != authAWSIAM && m != authUnauth {
			hasNonIamNonPlaintext = true
			break
		}
	}
	// Kafka ACLs, e.g. SASL/SCRAM, mTLS, SASL/PLAIN or Kerberos sources (Kerberos
	// is an ACL-bearing method same as the rest; it changes how the source is
	// reached, not whether its ACLs need recreating). Guarded off MSK Serverless
	// explicitly, though sourceAuthMethods already forces ['AWS IAM'] there, so
	// this can never actually fire on it either way.
	if hasNonIamNonPlaintext && !p.isServerless() {
		notes = append(notes, "Your Kafka ACLs don't carry over with your data. Recreate them in Confluent Cloud: `kcp create-asset migrate-acls kafka` turns the ACLs from your kcp scan into Terraform, with a Confluent Cloud service account and its ACLs for each principal. Review the generated files before you apply them.")
	}
	// AWS IAM policies. AWS IAM only exists on Amazon MSK, so this is MSK-only
	// with no separate platform check needed.
	if authHas(p, authAWSIAM) {
		notes = append(notes, "Your AWS IAM policies don't carry over with your data. `kcp create-asset migrate-acls iam` reads the IAM policies of the roles and users you name and turns their Kafka actions into Confluent Cloud ACLs, as Terraform. It reads only the first resource in each policy statement and ignores conditions, so check each generated ACL against the policy it came from.")
	}
	// Confluent Platform RBAC. migrate-acls (both modes) reads ACLs only, never
	// RBAC role bindings, and Confluent Cloud's own roles aren't a one-to-one
	// match for CP's.
	if p.SourcePlatform == "Confluent Platform" {
		notes = append(notes, "If your Confluent Platform cluster uses RBAC role bindings, recreate them in Confluent Cloud by hand. `migrate-acls` reads ACLs only, and Confluent Cloud's roles differ from Confluent Platform's.")
	}
	return notes
}

// gatewaySCRAMSentence is the Auth note for a Gateway plan whose clients use SCRAM:
// the Gateway checks their credentials against a secret store.
const gatewaySCRAMSentence = "The Gateway checks each client's SCRAM credentials against a secret store, so register them through the Gateway's registration route before you route clients through it."

// gatewayMTLSSentence is the Auth note for a Gateway plan whose clients use mTLS:
// the Gateway terminates TLS and swaps to an API key, so Confluent Cloud never sees
// the client certificates.
const gatewayMTLSSentence = "Through the Gateway, Confluent Cloud sees the API key the Gateway maps each client to, not the client's certificate, so map each client to its own API key and grant ACLs to that key's service account. Upload your CA and set up identity pools only if clients will later connect to Confluent Cloud directly."

// gatewayPlaintextAuthSentence is the auth reason for a Gateway plan whose clients send no
// credentials: the Gateway maps them all to one Confluent Cloud API key (ANONYMOUS principal).
const gatewayPlaintextAuthSentence = "Your clients keep connecting without credentials; the Gateway signs them in to Confluent Cloud with one mapped API key."

// GatewayAnonymousACLSentence is the access note for the one key the Gateway maps plaintext clients to.
const GatewayAnonymousACLSentence = "The Gateway maps all of these clients to one Confluent Cloud API key (the ANONYMOUS principal), so grant that key's service account ACLs that cover every client."

func authDecision(p Profile, tc string, gatewayMediated bool) AuthResult {
	// A Gateway swap is mandatory for mTLS clients and takes one mechanism, so
	// through the Gateway the certificates are not preserved on the Confluent Cloud side.
	mtlsViaGateway := gatewayMediated && authHas(p, authMTLS)
	preservable := authPreservable(p) && !mtlsViaGateway

	picked := append([]string(nil), p.TargetIdentityModel...)
	for i, m := range picked {
		if m == "Keep the same method" { // legacy: meant "preserve mTLS"
			picked[i] = "mTLS"
		}
	}
	picked = dedupe(picked)
	if len(picked) == 0 {
		if preservable {
			picked = []string{"mTLS"}
		} else {
			picked = []string{"API keys (SASL/PLAIN)"}
		}
	}

	targetLabel := func(m string) string {
		switch m {
		case "mTLS":
			if preservable {
				return "mTLS (preserved)"
			}
			return "mTLS (SSL client certificates)"
		case "OAuth":
			return "OAuth (SASL/OAUTHBEARER)"
		default:
			return "API keys (SASL/PLAIN)"
		}
	}
	labels := make([]string, len(picked))
	for i, m := range picked {
		labels[i] = targetLabel(m)
	}
	value := strings.Join(labels, " + ")
	multi := len(labels) > 1

	var reasonParts []string
	var authWhy string
	if !multi && preservable && labels[0] == "mTLS (preserved)" {
		detected := "mTLS"
		if len(p.SourceAuthTypes) > 0 {
			detected = "source uses " + strings.Join(p.SourceAuthTypes, ", ")
		}
		reasonParts = append(reasonParts, basis(srcOr(p.AuthAnswered, detected))+"you already use a method Confluent Cloud can preserve (mTLS), so we keep it after cutover.")
		authWhy = "Keeps the credentials your clients already use."
		// With no target picked, the source's other methods have nowhere to land but
		// API keys. Kerberos is excluded: its own sentence below names where it goes.
		if len(p.TargetIdentityModel) == 0 {
			var others []string
			for _, m := range sourceAuthMethods(p) {
				switch m {
				case authSCRAM, authAWSIAM:
					others = append(others, m)
				case authSASLPlain:
					others = append(others, "SASL/PLAIN")
				case authUnauth:
					others = append(others, "plaintext")
				}
			}
			if len(others) > 0 {
				if gatewayMediated {
					reasonParts = append(reasonParts, "Clients that use "+joinAndList(others)+" keep their current credentials through the Gateway, which swaps them for Confluent Cloud credentials. Move them to API keys (SASL/PLAIN) yourself only when you are ready to connect them to Confluent Cloud directly, bypassing the Gateway.")
				} else {
					reasonParts = append(reasonParts, "Clients that use "+joinAndList(others)+" move to API keys (SASL/PLAIN) at cutover.")
				}
			}
		}
	} else {
		lands := "after cutover, your clients authenticate to Confluent Cloud with " + strings.Join(labels, " or ") + "."
		if gatewayMediated {
			// The Gateway swaps credentials (`auth: swap`), so clients keep their source
			// credentials; they only move to these if they later bypass the Gateway.
			lands = "through the Gateway, your clients keep their current credentials, which the Gateway swaps for Confluent Cloud credentials. Move them to " + strings.Join(labels, " or ") + " yourself only when you are ready to connect them to Confluent Cloud directly, bypassing the Gateway."
		}
		sentence := strings.ToUpper(lands[:1]) + lands[1:]
		plaintextGateway := gatewayMediated && authHas(p, authUnauth)
		switch {
		case plaintextGateway:
			// Fixed wording, no basis lead or baseline note.
			sentence = gatewayPlaintextAuthSentence
		case len(p.TargetIdentityModel) > 0:
			sentence = basis(ans("target authentication")) + lands
		default:
			// No target auth was chosen, so this is the default baseline, not a
			// decision made from an answer. Say so, and name the alternatives.
			sentence += " This is the default baseline; OAuth and mTLS are also available if you need them."
		}
		if multi {
			sentence += " A Confluent Cloud cluster can support several methods at once."
		}
		reasonParts = append(reasonParts, sentence)
		authWhy = "Confluent Cloud issues new credentials for this method after cutover."
		if len(p.TargetIdentityModel) == 0 {
			// No target auth was chosen, so the firm value is a default, not a decision.
			// Say it's adjustable so it doesn't read as settled while other rows are Pending.
			authWhy = "Default baseline you can change with `target_auth`; Confluent Cloud issues new credentials after cutover."
		}
		if gatewayMediated {
			// Matches the reason above: through the Gateway clients keep their credentials.
			authWhy = "Through the Gateway, your clients keep their current credentials until they connect to Confluent Cloud directly."
			if len(p.TargetIdentityModel) == 0 {
				authWhy = "Default baseline you can change with `target_auth`; through the Gateway, your clients keep their current credentials until they connect to Confluent Cloud directly."
			}
			if plaintextGateway {
				authWhy = gatewayPlaintextAuthSentence
			}
		}
	}
	// One-time cluster/environment setup for the methods that need it. mTLS
	// always needs its own setup, preserved or not: Confluent Cloud still needs
	// the customer's certificate authority uploaded to the organization and a
	// certificate identity pool matching their client certificates, whether or
	// not the certificates themselves carry over.
	var setup []string
	if contains(picked, "mTLS") && !mtlsViaGateway {
		setup = append(setup, "mTLS needs your certificate authority uploaded to your Confluent Cloud organization and a certificate identity pool that matches your client certificates")
	}
	if contains(picked, "OAuth") {
		setup = append(setup, "OAuth needs an identity provider and an identity pool set up in your Confluent Cloud organization")
	}
	if len(setup) > 0 {
		reasonParts = append(reasonParts, "Setup: "+strings.Join(setup, "; ")+".")
	}

	// Where credentials land, named per picked method rather than one blanket
	// claim: API keys are the only method that actually maps to a Confluent
	// Cloud service account. mTLS and OAuth both map to identity pools instead.
	if contains(picked, "API keys (SASL/PLAIN)") {
		if gatewayMediated && authHas(p, authUnauth) {
			reasonParts = append(reasonParts, "The API key the Gateway maps your clients to belongs to a Confluent Cloud service account.")
		} else {
			reasonParts = append(reasonParts, "Your applications' API keys belong to Confluent Cloud service accounts.")
		}
	}
	var identityMethods []string
	if contains(picked, "mTLS") && !mtlsViaGateway {
		identityMethods = append(identityMethods, "mTLS")
	}
	if contains(picked, "OAuth") {
		identityMethods = append(identityMethods, "OAuth")
	}
	if len(identityMethods) > 0 {
		reasonParts = append(reasonParts, strings.Join(identityMethods, " and ")+" clients map to Confluent Cloud identity pools.")
	}

	if authHas(p, authKerberos) {
		// Confluent Cloud never accepts Kerberos from clients (its broker policy is
		// PLAIN/SCRAM/OAUTHBEARER only), so a Kerberos source must move its clients
		// off it before cutover, whatever target method this plan lands on. Names the
		// picked target methods (the resolved picks after the default cascade above,
		// same order as `value`), not a fixed list — the customer didn't necessarily
		// pick all three. Placed after the setup/landing sentences and before the
		// access-control notes below.
		shortLabels := make([]string, len(picked))
		for i, m := range picked {
			if m == "API keys (SASL/PLAIN)" {
				shortLabels[i] = "API keys"
			} else {
				shortLabels[i] = m
			}
		}
		reasonParts = append(reasonParts, "Confluent Cloud doesn't support Kerberos. At cutover, move your Kerberos clients to "+joinOr(shortLabels)+".")
	}

	// Access control: grant permissions to the identity pool, not the generated
	// service account, when the target picks include mTLS and/or OAuth.
	// identityMethods above is already exactly that set, so this can't drift
	// from the landing sentence it pairs with.
	accessNotes := accessControlNotes(p)
	if len(identityMethods) > 0 {
		methods := strings.Join(identityMethods, " and ")
		switch {
		case contains(picked, "API keys (SASL/PLAIN)"):
			accessNotes = append(accessNotes, "Clients that sign in with "+methods+" get their permissions through the matching identity pools; clients that use API keys keep theirs on their service accounts.")
		case len(accessNotes) > 0:
			accessNotes = append(accessNotes, "Your clients will sign in with "+methods+", so grant these permissions to the matching identity pools rather than to the generated service accounts.")
		default:
			accessNotes = append(accessNotes, "Your clients will sign in with "+methods+", so grant their permissions to the matching identity pools.")
		}
	}
	// A plaintext source has no access control to carry over, and a new Confluent Cloud
	// service account or identity pool starts with no access, so clients hit authorization
	// errors at cutover unless permissions are granted first. Follows the identity-pool
	// sentence above, which keys on whether a carried-over note precedes it, and this is not one.
	if authHas(p, authUnauth) {
		accessNotes = append(accessNotes, "A plaintext source has no access control to carry over, and new Confluent Cloud service accounts or identity pools have no access by default. Grant them ACLs or role bindings before cutover.")
	}
	if mtlsViaGateway {
		accessNotes = append(accessNotes, gatewayMTLSSentence)
	}
	if gatewayMediated && authHas(p, authSCRAM) {
		accessNotes = append(accessNotes, gatewaySCRAMSentence)
	}
	reasonParts = append(reasonParts, accessNotes...)

	return AuthResult{
		Value:  value,
		Reason: strings.Join(reasonParts, " "),
		Why:    authWhy,
		Action: "Set up " + value,
	}
}
