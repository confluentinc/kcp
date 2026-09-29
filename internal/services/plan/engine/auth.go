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
func sourceAuthHandling(method string, p Profile, via string) CredHandling {
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

	// via == viaLink
	switch method {
	case "SASL/SCRAM":
		return CredHandling{Method: method, Note: "No change. Cluster Linking uses your SCRAM credentials as-is."}
	case authMTLS:
		// The migration link signs in over SASL/SCRAM (kcp's migration-infra has no mTLS
		// link type, although Cluster Linking itself supports mTLS), so an mTLS-only source adds a SASL/SCRAM listener for the link to
		// use — this must agree with the migration-link step, not claim "no change".
		return CredHandling{Method: method, Note: "Cluster Linking itself can sign in with mTLS, but the migration link kcp generates signs in over SASL/SCRAM, so if your source is mTLS-only, add a SASL/SCRAM listener and create a SCRAM user for the link, in place before you apply the migration-link step above. Your application clients keep their mTLS certificates unchanged, and the listener is only for the migration — you can remove it after cutover."}
	case "API keys (SASL/PLAIN)":
		// kcp's generated link authenticates with SASL/SCRAM (ScramLoginModule), not PLAIN,
		// so a SASL/PLAIN-only source adds a SASL/SCRAM listener for the link — same as mTLS.
		// This must agree with the migration-link step, not claim "no change".
		return CredHandling{Method: method, Note: "The migration link signs in over SASL/SCRAM, so if your source is SASL/PLAIN-only, add a SASL/SCRAM listener and create a SCRAM user for the link, in place before you apply the migration-link step above. Your application clients keep their SASL/PLAIN credentials unchanged, and the listener is only for the migration — you can remove it after cutover."}
	case "None / plaintext":
		// Confluent Cloud Cluster Linking has no unauthenticated path, so an unauthenticated
		// source adds a SASL/SCRAM listener for the link (same as mTLS/PLAIN); separately, its
		// clients must adopt an auth method on Confluent Cloud after cutover.
		return CredHandling{Method: method, Note: "The migration link signs in over SASL/SCRAM — Confluent Cloud Cluster Linking has no unauthenticated path — so add a SASL/SCRAM listener and create a SCRAM user on your source for the link, in place before you apply the migration-link step above; you can remove that listener after cutover. Separately, your unauthenticated clients need a supported auth method on Confluent Cloud after cutover, which has no unauthenticated equivalent."}
	case authKerberos:
		// A Confluent Cloud (destination-initiated) cluster link cannot use Kerberos to
		// the source at all — Confluent Cloud's broker policy only accepts
		// PLAIN/SCRAM/OAUTHBEARER — so, unlike mTLS/PLAIN/unauthenticated, there is no
		// "add a listener for the link" fix on the source's own cluster; the listener
		// goes on a cluster only the link uses.
		return CredHandling{Method: method, Note: "Kerberos can't be used by a Cluster Link from Confluent Cloud. Add a SASL/SCRAM listener on your source cluster that only the link uses. Your clients can keep Kerberos until cutover."}
	case authAWSIAM:
		if p.isServerless() {
			return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, which is why this plan runs a jump cluster." + iamMap}
		}
		if authHas(p, authSCRAM) {
			// The source already exposes SASL/SCRAM, so the link uses that path (its note
			// above); nothing on the MSK cluster changes for the migration, and there is no
			// separate IAM step. Existing IAM clients just re-key on Confluent Cloud.
			return CredHandling{Method: method, Note: "Nothing to do for IAM here. The cluster link uses your existing SASL/SCRAM path (above), so your MSK cluster is unchanged for the migration; your IAM clients get new Confluent Cloud credentials when you point them over." + iamMap}
		}
		// Pure IAM source: a cluster link can't carry IAM, so the plan runs a jump cluster
		// (migration-infra type 5). We recommend the jump cluster because it leaves the
		// production source untouched; adding a SASL/SCRAM listener plus a direct link is
		// the alternative.
		return CredHandling{Method: method, Note: "AWS IAM credentials cannot cross a cluster link, so the link needs another way in. We recommend a jump cluster: a temporary Kafka cluster in your own account that reads MSK over your existing IAM and re-exposes your topics on SASL/SCRAM for the link. It leaves your production MSK cluster and its clients untouched — you run it only for the length of the migration, then remove it — and it reaches your Confluent Cloud cluster over an Egress PrivateLink Endpoint, which your cluster can use while its own networking stays on PNI. The alternative is to add a SASL/SCRAM listener on your MSK cluster used only by the link: less to set up, since the link then connects to MSK directly, but it enables SASL/SCRAM on your production cluster and adds credentials to manage. MSK runs IAM and SASL/SCRAM side by side, so your existing IAM clients keep working either way." + iamMap}
	default:
		// An unrecognised method must never assert the link works as-is ("No
		// change.") — Cluster Linking's supported source-auth set is narrow, so
		// silence here would wrongly promise compatibility.
		return CredHandling{Method: method, Note: "Check with your Confluent team whether Cluster Linking can use " + method + "."}
	}
}

// sourceCredentialHandling is one note per source auth method, given the
// switchover mechanism. sw may be nil (defaults to the Cluster Linking path).
func sourceCredentialHandling(p Profile, sw *SwitchoverResult) []CredHandling {
	via := replicationVia(sw)
	methods := sourceAuthMethods(p)
	out := make([]CredHandling, 0, len(methods))
	for _, m := range methods {
		out = append(out, sourceAuthHandling(m, p, via))
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
		notes = append(notes, "Your Kafka ACLs don't carry over with your data. Recreate them in Confluent Cloud: kcp create-asset migrate-acls kafka turns the ACLs from your kcp scan into Terraform, with a Confluent Cloud service account and its ACLs for each principal. Review the generated files before you apply them.")
	}
	// AWS IAM policies. AWS IAM only exists on Amazon MSK, so this is MSK-only
	// with no separate platform check needed.
	if authHas(p, authAWSIAM) {
		notes = append(notes, "Your AWS IAM policies don't carry over with your data. kcp create-asset migrate-acls iam reads the IAM policies of the roles and users you name and turns their Kafka actions into Confluent Cloud ACLs, as Terraform. It reads only the first resource in each policy statement and ignores conditions, so check each generated ACL against the policy it came from.")
	}
	// Confluent Platform RBAC. migrate-acls (both modes) reads ACLs only, never
	// RBAC role bindings, and Confluent Cloud's own roles aren't a one-to-one
	// match for CP's.
	if p.SourcePlatform == "Confluent Platform" {
		notes = append(notes, "If your Confluent Platform cluster uses RBAC role bindings, recreate them in Confluent Cloud by hand. migrate-acls reads ACLs only, and Confluent Cloud's roles differ from Confluent Platform's.")
	}
	return notes
}

func authDecision(p Profile, tc string) AuthResult {
	preservable := authPreservable(p)

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
	} else {
		sentence := "After cutover, your clients authenticate to Confluent Cloud with " + strings.Join(labels, " or ") + "."
		if len(p.TargetIdentityModel) > 0 {
			sentence = basis(ans("target authentication")) + "after cutover, your clients authenticate to Confluent Cloud with " + strings.Join(labels, " or ") + "."
		} else {
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
	}
	// One-time cluster/environment setup for the methods that need it. mTLS
	// always needs its own setup, preserved or not: Confluent Cloud still needs
	// the customer's certificate authority uploaded to the organization and a
	// certificate identity pool matching their client certificates, whether or
	// not the certificates themselves carry over.
	var setup []string
	if contains(picked, "mTLS") {
		setup = append(setup, "mTLS needs your certificate authority uploaded to your Confluent Cloud organization and a certificate identity pool that matches your client certificates")
	}
	if contains(picked, "OAuth") {
		setup = append(setup, "OAuth needs an identity provider and an identity pool configured in your Confluent Cloud organization")
	}
	if len(setup) > 0 {
		reasonParts = append(reasonParts, "Setup: "+strings.Join(setup, "; ")+".")
	}

	// Where credentials land, named per picked method rather than one blanket
	// claim: API keys are the only method that actually maps to a Confluent
	// Cloud service account. mTLS and OAuth both map to identity pools instead.
	if contains(picked, "API keys (SASL/PLAIN)") {
		reasonParts = append(reasonParts, "Your applications' API keys belong to Confluent Cloud service accounts.")
	}
	var identityMethods []string
	if contains(picked, "mTLS") {
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
		// access-control notes below, matching the reference decision engine.
		shortLabels := make([]string, len(picked))
		for i, m := range picked {
			if m == "API keys (SASL/PLAIN)" {
				shortLabels[i] = "API keys"
			} else {
				shortLabels[i] = m
			}
		}
		reasonParts = append(reasonParts, "Confluent Cloud doesn't support Kerberos. Before cutover, move these clients to "+joinOr(shortLabels)+".")
	}

	// Access control: grant permissions to the identity pool, not the generated
	// service account, when the target picks include mTLS and/or OAuth.
	// identityMethods above is already exactly that set, so this can't drift
	// from the landing sentence it pairs with.
	accessNotes := accessControlNotes(p)
	if len(identityMethods) > 0 {
		accessNotes = append(accessNotes, "Your clients will sign in with "+strings.Join(identityMethods, " and ")+
			", so grant these permissions to the matching identity pools rather than to the generated service accounts.")
	}
	reasonParts = append(reasonParts, accessNotes...)

	return AuthResult{
		Value:  value,
		Reason: strings.Join(reasonParts, " "),
		Why:    authWhy,
		Action: "Set up " + value,
	}
}
