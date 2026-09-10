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
	if p.SourceCloud != "" {
		return p.SourceCloud
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
	const iamMap = " Your AWS IAM policies don't carry over automatically. Confluent Cloud uses role-based access control (RBAC) role bindings, which you set up separately (we can help scope this)."

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
		return CredHandling{Method: method, Note: "No change. Cluster Linking uses your mTLS certificates as-is."}
	case "API keys (SASL/PLAIN)":
		return CredHandling{Method: method, Note: "No change. Cluster Linking uses your SASL/PLAIN credentials as-is."}
	case "None / plaintext":
		return CredHandling{Method: method, Note: "Nothing to prepare on your source — the cluster link reads it over plaintext with no credentials — but your unauthenticated clients will need a supported auth method on Confluent Cloud after cutover, which has no unauthenticated equivalent."}
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
		return CredHandling{Method: method, Note: "No change."}
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
	var setup []string
	if contains(picked, "mTLS") && !preservable {
		setup = append(setup, "mTLS needs a certificate authority uploaded to the cluster")
	}
	if contains(picked, "OAuth") {
		setup = append(setup, "OAuth needs an identity provider configured in your environment")
	}
	if len(setup) > 0 {
		reasonParts = append(reasonParts, "Setup: "+strings.Join(setup, "; ")+".")
	}
	reasonParts = append(reasonParts, "All credentials land as Confluent Cloud service accounts.")

	return AuthResult{
		Value:  value,
		Reason: strings.Join(reasonParts, " "),
		Why:    authWhy,
		Action: "Set up " + value,
	}
}
