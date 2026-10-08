package engine

import (
	"strings"
	"testing"
)

const gatewayFallbackPrefix = "Zero downtime through Confluent Cloud Gateway, as this plan sets it up with one route, needs every client on one sign-in method the Gateway supports (SASL/SCRAM, SASL/PLAIN, mTLS, or no authentication). Your clients use "

func gatewayFallbackSentence(methods, closing string) string {
	return gatewayFallbackPrefix + methods + ", so this plan uses a Cluster Linking cutover without the Gateway instead." + closing
}

// The Gateway path is offered only for exactly one of SCRAM, PLAIN or mTLS (or a source
// with no auth answered, or plaintext only). Everything else, Kerberos included, falls
// back to a coordinated cutover and says why.
func TestSwitchover_GatewayNeedsOneSupportedSignInMethod(t *testing.T) {
	gw := func(auths []string, mut ...func(*Profile)) SwitchoverResult {
		return swOf(func(p *Profile) {
			p.SourceAuthTypes = auths
			p.DowntimeTolerance = "Zero downtime"
			for _, m := range mut {
				m(p)
			}
		})
	}
	for _, a := range []string{authSCRAM, authSASLPlain, authMTLS, authUnauth} {
		sw := gw([]string{a})
		if !sw.GatewayMediated || strings.Contains(sw.Reason, gatewayFallbackPrefix) {
			t.Errorf("%s: want the Gateway path, got %+v", a, sw)
		}
	}
	if sw := gw(nil); !sw.GatewayMediated {
		t.Errorf("unanswered auth keeps the Gateway: %+v", sw)
	}
	cases := []struct {
		auths []string
		names string
	}{
		{[]string{authSCRAM, authMTLS}, "SASL/SCRAM and mTLS"},
		{[]string{authKerberos}, "Kerberos (GSSAPI)"},
		{[]string{authSCRAM, authSASLPlain, authKerberos}, "SASL/SCRAM, SASL/PLAIN, and Kerberos (GSSAPI)"},
		{[]string{authMTLS, authUnauth}, "mTLS and no authentication"},
	}
	for _, c := range cases {
		sw := gw(c.auths)
		if sw.GatewayMediated || sw.TechAssist {
			t.Errorf("%v: must fall back to a coordinated cutover: %+v", c.auths, sw)
		}
		if !strings.Contains(sw.Reason, gatewayFallbackSentence(c.names, "")) {
			t.Errorf("%v: reason lacks the fallback sentence:\n%s", c.auths, sw.Reason)
		}
		if strings.Contains(sw.Reason, "AWS IAM is not one") {
			t.Errorf("%v: must not use the IAM note:\n%s", c.auths, sw.Reason)
		}
	}
	// Escalation with no Gateway-needing answer falls back too.
	esc := gw([]string{authSCRAM, authSASLPlain}, func(p *Profile) {
		p.DowntimeTolerance = "Minutes per service"
		p.ClientCoordinationBurden = "Hard (many clients and teams)"
	})
	if esc.GatewayMediated || !strings.Contains(esc.Reason, gatewayFallbackSentence("SASL/SCRAM and SASL/PLAIN", "")) {
		t.Errorf("escalation: %+v", esc)
	}
	// IAM keeps its own note.
	iam := gw([]string{authAWSIAM})
	if iam.GatewayMediated || !strings.Contains(iam.Reason, "AWS IAM is not one") || strings.Contains(iam.Reason, gatewayFallbackPrefix) {
		t.Errorf("IAM: %+v", iam)
	}
}

// The closing sentence of the fallback follows the cutover style.
func TestSwitchover_GatewayFallbackClosingFollowsStyle(t *testing.T) {
	cases := []struct {
		downtime, closing string
	}{
		{"A scheduled window, all at once", " Clients pause briefly while you cut them over together."},
		{"A scheduled window, one service at a time", " Each service pauses briefly at its cutover."},
		{"Minutes per service", ""},
		{"Seconds per service", ""},
		{"Zero downtime", ""},
	}
	for _, c := range cases {
		sw := swOf(func(p *Profile) {
			p.SourceAuthTypes = []string{authKerberos}
			p.DowntimeTolerance = c.downtime
			p.ClientCoordinationBurden = "Hard (many clients and teams)"
		})
		if want := gatewayFallbackSentence("Kerberos (GSSAPI)", c.closing); !strings.Contains(sw.Reason, want) {
			t.Errorf("%s: reason lacks %q:\n%s", c.downtime, want, sw.Reason)
		}
		if c.closing == "" && strings.Contains(sw.Reason, "pauses briefly") {
			t.Errorf("%s: must not close with a pause sentence:\n%s", c.downtime, sw.Reason)
		}
	}
}

// 64 auth subsets x 3 downtime answers x 2 coordination burdens x 2 size bands: the
// number of combinations that lose the Gateway path is 297.
func TestSwitchover_GatewayScopeSweep(t *testing.T) {
	methods := []string{authSCRAM, authSASLPlain, authMTLS, authKerberos, authAWSIAM, authUnauth}
	downtimes := []string{"Zero downtime", "Seconds per service", "Minutes per service"}
	coords := []string{"Easy (few clients, one team)", "Hard (many clients and teams)"}
	bands := []int{1, privateLinkCapBand}
	lost := 0
	for mask := 0; mask < 1<<len(methods); mask++ {
		var auths []string
		hasIAM := false
		for i, m := range methods {
			if mask&(1<<i) != 0 {
				auths = append(auths, m)
				hasIAM = hasIAM || m == authAWSIAM
			}
		}
		for _, d := range downtimes {
			for _, c := range coords {
				for _, b := range bands {
					at := func(a []string) SwitchoverResult {
						p := baseProfile(func(p *Profile) {
							p.SourceAuthTypes = a
							p.DowntimeTolerance = d
							p.ClientCoordinationBurden = c
						})
						return switchoverDecision(p, SizingResult{Band: b}, "")
					}
					base, got := at(nil), at(auths)
					if got.GatewayMediated && !base.GatewayMediated {
						t.Fatalf("%v %s %s band %d: gained the Gateway", auths, d, c, b)
					}
					if base.GatewayMediated && !got.GatewayMediated && !hasIAM {
						lost++
					}
					if hasIAM && got.GatewayMediated {
						t.Fatalf("%v: IAM must not use the Gateway", auths)
					}
				}
			}
		}
	}
	if lost != 297 {
		t.Errorf("combinations that lose the Gateway = %d, want 297", lost)
	}
}

// A Gateway plan for plaintext clients names the one mapped key and drops the move-clients note.
func TestGatewayPlaintext_AnonymousKey(t *testing.T) {
	p := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authUnauth} })
	r := authDecision(p, "AWS", true)
	if !strings.Contains(r.Reason, "Your clients keep connecting without credentials; the Gateway signs them in to Confluent Cloud with one mapped API key.") || strings.Contains(r.Reason, "keep their current credentials") {
		t.Errorf("plaintext Gateway auth reason: %q", r.Reason)
	}
	for _, via := range []string{viaLink, viaReplicator} {
		n := sourceAuthHandling(authUnauth, p, via, &linkPath{jump: true}, true).Note
		if !strings.Contains(n, GatewayAnonymousACLSentence) || strings.Contains(n, "Move these clients") || strings.Contains(n, "need a supported auth method") {
			t.Errorf("%s gateway plaintext note: %q", via, n)
		}
	}
	if n := sourceAuthHandling(authUnauth, p, viaReplicator, &linkPath{}, false).Note; !strings.Contains(n, "Move these clients") {
		t.Errorf("non-Gateway plaintext keeps its note: %q", n)
	}
}

// A SCRAM Gateway plan says clients' SCRAM credentials live in a secret store; the other
// Gateway methods do not.
func TestAuthDecision_GatewaySCRAMSecretStore(t *testing.T) {
	const scram = "The Gateway checks each client's SCRAM credentials against a secret store, so register them through the Gateway's registration route before you route clients through it."
	p := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authSCRAM} })
	if r := authDecision(p, "AWS", true).Reason; !strings.Contains(r, scram) {
		t.Errorf("SCRAM Gateway Auth lacks the secret-store sentence: %q", r)
	}
	if r := authDecision(p, "AWS", false).Reason; strings.Contains(r, "secret store") {
		t.Errorf("non-Gateway SCRAM must not mention a secret store: %q", r)
	}
	p.SourceAuthTypes = []string{authSASLPlain}
	if r := authDecision(p, "AWS", true).Reason; strings.Contains(r, "secret store") {
		t.Errorf("SASL/PLAIN Gateway plan must not mention a secret store: %q", r)
	}
}

// Every Replicator connector plan carries the sink offsets sentence and the source
// offsets sentence, once each, as plain text with the docs URL in parentheses.
func TestConnectorsDecision_ReplicatorOffsetsOnEveryConnectorPlan(t *testing.T) {
	const sink = "At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, print each partition's offset for that time with kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group <any-unused-group-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --dry-run, then create the connector with those offsets; see Create connectors with offsets (https://docs.confluent.io/cloud/current/connectors/offsets.html)."
	const selfManaged = "At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, set the new sink's consumer group to that time, then create the sink connector: kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group connect-<connector-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --execute. Replicator keeps each record's timestamp, so the sink resumes where the old one stopped; a few records around that time may be delivered twice."
	const source = "Source connectors don't carry offsets over, so check where each one should start before you apply it."
	for _, dest := range []string{"Move to Confluent-managed", "Keep self-managed"} {
		for _, p := range []Profile{
			{MSKConnectPresent: strptr("Yes"), ConnectorDestination: dest},
			{SelfManagedConnectors: strptr("Yes"), ConnectorDestination: dest},
			{MSKConnectPresent: strptr("Yes"), SelfManagedConnectors: strptr("Yes"), ConnectorDestination: dest},
		} {
			r := connectorsDecision(p, viaReplicator).Reason
			want := sink
			if dest == "Keep self-managed" {
				want = selfManaged
				if strings.Contains(r, "offsets.html") {
					t.Errorf("self-managed connectors must not link the managed offsets page: %q", r)
				}
			}
			// A generated definition is applied for MSK Connect, and for self-managed connectors moved to Confluent-managed.
			if wantBlock := dest == "Move to Confluent-managed"; strings.Count(r, generatedSinkOffsetsBlockSentence) != btoi(wantBlock) {
				t.Errorf("%s %+v: offsets-block sentence count wrong:\n%s", dest, p, r)
			}
			// The utility carries source offsets, so a self-managed-only move doesn't say they don't carry over.
			wantSource := btoi(dest != "Move to Confluent-managed" || p.MSKConnectPresent != nil)
			if strings.Count(r, want) != 1 || strings.Count(r, source) != wantSource || strings.Count(r, "carry offsets over") != wantSource {
				t.Errorf("%s %+v: want the sink sentence once and the source sentence %d times:\n%s", dest, p, wantSource, r)
			}
			if strings.Contains(r, "](") {
				t.Errorf("reasons use no markdown links: %q", r)
			}
		}
	}
	p := Profile{SelfManagedConnectors: strptr("Yes")}
	if r := connectorsDecision(p, viaReplicator).Reason; strings.Contains(r, "carry offsets over") || !strings.Contains(r, "stop_create_latest_offset") {
		t.Errorf("utility-moved self-managed Replicator plan must not say source offsets don't carry: %q", r)
	}
	p.ConnectorDestination = "Keep self-managed"
	if r := connectorsDecision(p, viaReplicator).Reason; !strings.Contains(r, source) || strings.Contains(r, "stop_create_latest_offset") {
		t.Errorf("kept self-managed Replicator plan lacks the source sentence: %q", r)
	}
	if r := connectorsDecision(p, viaNone).Reason; strings.Contains(r, "Replicator gives") {
		t.Errorf("start-fresh must not say Replicator: %q", r)
	}
}

// A size-only cross from public to private adds nothing on Azure or Google Cloud, and
// offers no public swap there; AWS keeps its sentence.
func TestAppendPublicFallbackReason_SizeOnlyCrossIsAWSOnly(t *testing.T) {
	for _, tc := range []string{"AWS", "Azure", "GCP"} {
		p := baseProfile(func(p *Profile) { p.TargetCloud = tc })
		net := &NetworkingResult{}
		appendPublicFallbackReason(p, net, tc)
		if tc == "AWS" {
			if !strings.Contains(net.Reason, "We planned private because it is the better fit at this size") && !strings.Contains(net.Reason, "private networking is the better fit") {
				t.Errorf("AWS lost its size-crossing sentence: %q", net.Reason)
			}
			continue
		}
		if net.Reason != "" || net.PublicFallback != nil || net.PublicFallbackCertain {
			t.Errorf("%s: size-only cross must add nothing: %+v", tc, net)
		}
	}
}

// A plaintext Gateway plan says one mapped key, not the plural application keys.
func TestAuthDecision_GatewayPlaintextOneMappedKey(t *testing.T) {
	p := baseProfile(func(p *Profile) { p.SourceAuthTypes = []string{authUnauth} })
	r := authDecision(p, "AWS", true).Reason
	if strings.Contains(r, "Your applications' API keys") || !strings.Contains(r, "The API key the Gateway maps your clients to belongs to a Confluent Cloud service account.") {
		t.Errorf("plaintext Gateway reason: %q", r)
	}
}

func btoi(b bool) int {
	if b {
		return 1
	}
	return 0
}

const streamsFallback = "Confluent Cloud Gateway client switchover isn't safe for Kafka Streams applications, so this plan uses a Cluster Linking cutover without the Gateway instead."

// A Kafka Streams / exactly-once profile never gets the Gateway path, and the auth
// fallback wins when both apply.
func TestSwitchover_KafkaStreamsSkipsGateway(t *testing.T) {
	sw := swOf(func(p *Profile) {
		p.DowntimeTolerance = "Zero downtime"
		p.SourceAuthTypes = []string{authSCRAM}
		p.EosStreams = []string{"Kafka Streams"}
	})
	if sw.GatewayMediated || sw.TechAssist || sw.Value != styleNoGateway {
		t.Errorf("Streams must fall back to a Cluster Linking cutover: %+v", sw)
	}
	if !strings.Contains(sw.Reason, streamsFallback) || !strings.Contains(sw.Reason, "One caveat: Cluster Linking does not carry Kafka Streams.") {
		t.Errorf("missing Streams sentence or caveat:\n%s", sw.Reason)
	}
	for _, d := range []string{"Seconds per service", "Zero downtime"} {
		if r := swOf(func(p *Profile) {
			p.DowntimeTolerance = d
			p.SourceAuthTypes = []string{authSCRAM}
			p.EosStreams = []string{"Kafka Streams"}
		}).Reason; strings.Count(r, "One caveat: Cluster Linking does not carry Kafka Streams.") != 1 {
			t.Errorf("%s: fallback must carry the Streams caveat once:\n%s", d, r)
		}
	}
	// Exactly-once alone doesn't gate the Gateway.
	eos := swOf(func(p *Profile) {
		p.DowntimeTolerance = "Zero downtime"
		p.SourceAuthTypes = []string{authSCRAM}
		p.EosStreams = []string{"Exactly-once or transactions"}
	})
	if !eos.GatewayMediated || strings.Contains(eos.Reason, streamsFallback) {
		t.Errorf("exactly-once only must keep the Gateway: %+v", eos)
	}
	both := swOf(func(p *Profile) {
		p.DowntimeTolerance = "Zero downtime"
		p.SourceAuthTypes = []string{authKerberos}
		p.EosStreams = []string{"Kafka Streams"}
	})
	if strings.Contains(both.Reason, streamsFallback) || !strings.Contains(both.Reason, gatewayFallbackPrefix) {
		t.Errorf("auth fallback only when both apply:\n%s", both.Reason)
	}
	if allAtOnce := swOf(func(p *Profile) {
		p.DowntimeTolerance = "Minutes per service"
		p.ClientCoordinationBurden = "Hard (many clients and teams)"
		p.EosStreams = []string{"Kafka Streams"}
	}); allAtOnce.GatewayMediated || !strings.Contains(allAtOnce.Reason, streamsFallback) {
		t.Errorf("escalation must not pick the Gateway for Streams: %+v", allAtOnce)
	}
}

// The all-at-once cutover stops consumers too before the promote.
func TestClCutoverSteps_AllAtOnceStopsConsumers(t *testing.T) {
	const tail = ", promote the mirror topics so they accept writes, then restart your clients against Confluent Cloud."
	want := "At cutover, stop your producers and consumers, wait for the mirror to catch up (lag zero) and one consumer offset sync interval" + tail
	if got := clCutoverSteps(styleAllAtOnce, false); !strings.Contains(got, want) {
		t.Errorf("got %q", got)
	}
	jump := "At cutover, stop your producers and consumers, wait for the mirror to catch up (lag zero) and two consumer offset sync intervals (offsets cross both links)" + tail
	if got := clCutoverSteps(styleAllAtOnce, true); !strings.Contains(got, jump) {
		t.Errorf("jump: got %q", got)
	}
}
