package engine

import (
	"strings"
	"testing"
)

// Replicator changes offsets and start-fresh topics are empty, so self-managed Connect
// carries only source connectors with the utility; Cluster Linking keeps the resume wording.
func TestConnectorsDecision_SelfManagedOffsetsFollowDataMovement(t *testing.T) {
	p := Profile{SelfManagedConnectors: strptr("Yes")}
	const resume = "so it resumes from its last offsets"
	const fresh = "Use the Connect Migration Utility with stop_create_latest_offset for source connectors only. This mode needs Kafka Connect on Apache Kafka 3.6 or Confluent Platform 7.6 or later, which has the offsets REST API. Create sink connectors without carried-over offsets, since the topics on Confluent Cloud have new offsets."
	for _, via := range []string{"", viaLink} {
		r := connectorsDecision(p, via).Reason
		if !strings.Contains(r, resume) || strings.Contains(r, fresh) {
			t.Errorf("via %q: want resume wording only: %q", via, r)
		}
	}
	for _, via := range []string{viaNone} {
		r := connectorsDecision(p, via).Reason
		if !strings.Contains(r, fresh) || strings.Contains(r, resume) {
			t.Errorf("via %q: want new-offsets wording only: %q", via, r)
		}
	}
}

// Generated MSK Connect Terraform has no offsets block, so a cluster-link plan says to add one.
func TestConnectorsDecision_MSKSinkOffsetsBlock(t *testing.T) {
	p := Profile{MSKConnectPresent: strptr("Yes")}
	const add = "Add an offsets block to each sink connector's generated definition before you apply it."
	if r := connectorsDecision(p, viaLink).Reason; !strings.Contains(r, add) {
		t.Errorf("link reason lacks the offsets block step: %q", r)
	}
	// Replicator also adds it, once, after the sink offsets sentence; start fresh has no such step.
	if r := connectorsDecision(p, viaReplicator).Reason; strings.Count(r, add) != 1 || strings.Index(r, add) < strings.Index(r, replicatorSinkOffsetsSentence) {
		t.Errorf("replicator reason must carry the offsets block step once, after the sink offsets: %q", r)
	}
	if r := connectorsDecision(p, viaNone).Reason; strings.Contains(r, add) {
		t.Errorf("start-fresh reason has the offsets step: %q", r)
	}
}

// Every note that has the customer create a new SCRAM user for the link also says what that user needs.
func TestSourceAuthHandling_NewSCRAMUserNotesCarryACLs(t *testing.T) {
	kafka := Profile{SourcePlatform: "Apache Kafka"}
	for _, m := range []string{authKerberos, authSASLPlain, authMTLS, "None / plaintext"} {
		note := sourceAuthHandling(m, kafka, viaLink, &linkPath{}, false).Note
		if !strings.Contains(note, "SCRAM user") || !strings.Contains(note, linkACLSentence) {
			t.Errorf("%s note lacks the ACL sentence: %q", m, note)
		}
	}
	msk := Profile{SourcePlatform: "Amazon MSK", MSKClusterType: MSKProvisioned, SourcePublicAccess: "Yes"}
	if note := sourceAuthHandling(authAWSIAM, msk, viaLink, &linkPath{}, false).Note; !strings.Contains(note, linkACLSentence) {
		t.Errorf("public MSK IAM note lacks the ACL sentence: %q", note)
	}
	// Once the listener has been described, later notes point back to it and do not repeat the ask.
	lp := &linkPath{listenerAsked: true}
	if note := sourceAuthHandling(authKerberos, kafka, viaLink, lp, false).Note; strings.Contains(note, linkACLSentence) {
		t.Errorf("follow-up note repeats the ACL sentence: %q", note)
	}
}

// The Dedicated-for-mTLS sentence is GCP-only; Azure and AWS Enterprise carry mTLS.
func TestAppendPublicFallbackReason_MTLSDedicatedSentenceIsGCPOnly(t *testing.T) {
	const dedicated = "Staying public with mTLS would mean a Dedicated cluster"
	const lead = "mTLS authentication needs an Enterprise cluster, which runs on private networking."
	for _, tc := range []string{"AWS", "Azure", "GCP"} {
		p := baseProfile(func(p *Profile) { p.TargetCloud = tc; p.TargetIdentityModel = []string{"mTLS"} })
		net := &NetworkingResult{}
		appendPublicFallbackReason(p, net, tc)
		if !strings.Contains(net.Reason, lead) {
			t.Errorf("%s: reason lacks the mTLS lead: %q", tc, net.Reason)
		}
		if got := strings.Contains(net.Reason, dedicated); got != (tc == "GCP") {
			t.Errorf("%s: Dedicated sentence present = %v: %q", tc, got, net.Reason)
		}
		if net.PublicFallback != nil {
			t.Errorf("%s: mTLS must not offer a public Standard swap", tc)
		}
	}
}

// Replicator gives the topics new offsets, so sinks are created with offsets set from
// the time the old ones stopped; start-fresh keeps "without carried-over offsets".
func TestConnectorsDecision_ReplicatorSinkOffsets(t *testing.T) {
	p := Profile{SelfManagedConnectors: strptr("Yes")}
	const want = replicatorSinkOffsetsSentence
	if r := connectorsDecision(p, viaReplicator).Reason; !strings.Contains(r, want) || strings.Contains(r, "without carried-over offsets") {
		t.Errorf("replicator reason: %q", r)
	}
	if r := connectorsDecision(p, viaNone).Reason; !strings.Contains(r, "without carried-over offsets") || strings.Contains(r, "Replicator gives") {
		t.Errorf("start-fresh reason: %q", r)
	}
}

// Wherever the plan recommends the utility's stop_create_latest_offset mode it states the
// Connect version it needs, and a utility-moved Replicator plan says sinks are created
// from generated definitions that need an offsets block (kept self-managed sinks don't).
func TestConnectorsDecision_UtilityVersionAndReplicatorOffsetsBlock(t *testing.T) {
	const version = "This mode needs Kafka Connect on Apache Kafka 3.6 or Confluent Platform 7.6 or later, which has the offsets REST API."
	const block = "Add an offsets block to each sink connector's generated definition before you apply it."
	p := Profile{SelfManagedConnectors: strptr("Yes")}
	for _, via := range []string{"", viaLink, viaReplicator, viaNone} {
		if r := connectorsDecision(p, via).Reason; strings.Count(r, version) != 1 {
			t.Errorf("via %q: want the version sentence once: %q", via, r)
		}
	}
	if r := connectorsDecision(p, viaReplicator).Reason; strings.Count(r, block) != 1 {
		t.Errorf("utility-moved Replicator plan lacks the offsets block step: %q", r)
	}
	p.ConnectorDestination = "Keep self-managed"
	r := connectorsDecision(p, viaReplicator).Reason
	if strings.Contains(r, block) || strings.Contains(r, version) {
		t.Errorf("kept self-managed Replicator plan applies no generated definition: %q", r)
	}
}
