package plan

import (
	"strings"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
)

// stepsInputs is a complete, private, data-moving scanless plan-inputs.yaml body;
// overrides replace or add a key, and an empty value drops it.
func stepsInputs(overrides map[string]string) string {
	keys := []string{"use_case_breadth", "move_existing_data", "private_networking_required", "schema_registry", "schema_strategy",
		"downtime_tolerance", "kafka_version", "partition_band", "tiered_storage", "topics_have_custom_settings",
		"self_managed_connectors", "cc_egress_required", "connects_today", "source_platform", "source_auth"}
	vals := map[string]string{
		"use_case_breadth": "few-teams", "move_existing_data": "true", "private_networking_required": "true",
		"schema_registry": "none", "schema_strategy": "schemaless", "downtime_tolerance": "minutes",
		"kafka_version": "3.0-plus", "partition_band": "under-2500", "tiered_storage": "false",
		"topics_have_custom_settings": "false", "self_managed_connectors": "false", "cc_egress_required": "false",
		"connects_today": "privatelink", "source_platform": "msk", "source_auth": "[scram]",
	}
	for k, v := range overrides {
		if _, ok := vals[k]; !ok {
			keys = append(keys, k)
		}
		vals[k] = v
	}
	var b strings.Builder
	b.WriteString("clusters:\n  your-cluster:\n")
	for _, k := range keys {
		if vals[k] == "" {
			continue // an empty override drops the answer
		}
		b.WriteString("    " + k + ": " + vals[k] + "\n")
	}
	return b.String()
}

// stepsMarkdown renders the scanless plan.md for the given answers.
func stepsMarkdown(t *testing.T, overrides map[string]string) string {
	t.Helper()
	declared, _, err := ParseDeclaredInputs([]byte(stepsInputs(overrides)))
	if err != nil {
		t.Fatal(err)
	}
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	return RenderEnginePlanMarkdown(BuildEnginePlan(ScanlessState(), declared, "", fixed))
}

// stepsSection is the "Migration steps" section of a plan.md.
func stepsSection(t *testing.T, md string) string {
	t.Helper()
	_, rest, ok := strings.Cut(md, "### Migration steps")
	if !ok {
		t.Fatalf("no migration steps section:\n%s", md)
	}
	section, _, _ := strings.Cut(rest, "\n## Questions")
	return section
}

// A jump-cluster plan creates the PrivateLink endpoint before the link step that takes
// its ID, names it in the networking value, and enables offset sync on both links.
func TestMigrationSteps_JumpClusterPrivateLinkAndOffsetSync(t *testing.T) {
	md := stepsMarkdown(t, map[string]string{"source_auth": "[iam]"})
	steps := stepsSection(t, md)
	creds := strings.Index(steps, "**Step 2: prepare your source credentials.**")
	privateLink := strings.Index(steps, "**Step 3: set up a PrivateLink endpoint for the jump cluster.**")
	link := strings.Index(steps, "**Step 4: set up the jump cluster and its cluster links.**")
	if creds < 0 || privateLink < creds || link < privateLink {
		t.Fatalf("want credentials, then PrivateLink endpoint, then the link step (got %d, %d, %d):\n%s", creds, privateLink, link, steps)
	}
	for _, want := range []string{
		"Create an ingress PrivateLink Gateway and a PrivateLink Access Point for your Confluent Cloud environment, with a VPC endpoint in the jump cluster's VPC, then use that endpoint's bootstrap address for the jump cluster's link",
		"https://docs.confluent.io/cloud/current/networking/aws-platt.html",
		"for `<your-vpce-id>` and the PrivateLink **bootstrap endpoint** for `<cc-bootstrap-endpoint>` in Step 4",
		"the PrivateLink (access point) bootstrap endpoint (not the PNI one) and the VPC endpoint from Step 3",
		"`kafka-configs --bootstrap-server <jump-cluster-broker>:9092 --alter --cluster-link <your-link-name>-msk-cp --add-config-file <your-link-config-file>`",
		"`confluent kafka link configuration update <your-link-name> --config <your-link-config-file>`",
		"Enable consumer offset sync on both cluster links (from your MSK cluster to the jump cluster, and from the jump cluster to Confluent Cloud): set consumer.offset.sync.enable=true and list the consumer groups to migrate in consumer.offset.group.filters on each, so consumer offsets come across.",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("jump-cluster steps missing %q:\n%s", want, steps)
		}
	}
	// target-infra rejects --cluster-id with --cluster-type, so the PrivateLink step must not emit it.
	if strings.Contains(steps, "--cluster-id <cc-cluster-id> --cluster-type") || strings.Contains(steps, "--env-id <cc-env-id> --cluster-id") {
		t.Errorf("PrivateLink step must not emit a target-infra command that combines --cluster-id and --cluster-type:\n%s", steps)
	}
	if !strings.Contains(md, "PNI + PrivateLink for the jump cluster") {
		t.Errorf("networking value should name the jump cluster's PrivateLink:\n%s", md)
	}
}

// A plan with no jump cluster has no PrivateLink step and enables offset sync on the one link.
func TestMigrationSteps_NoJumpClusterNoPrivateLinkStep(t *testing.T) {
	steps := stepsSection(t, stepsMarkdown(t, nil))
	if strings.Contains(steps, "PrivateLink endpoint for the jump cluster") || strings.Contains(steps, "both cluster links") {
		t.Errorf("a direct link has no jump-cluster steps:\n%s", steps)
	}
	if !strings.Contains(steps, "enable consumer offset sync on the cluster link: set consumer.offset.sync.enable=true and list the consumer groups to migrate in consumer.offset.group.filters (it defaults to none), so consumer offsets come across too.") {
		t.Errorf("plain link should enable offset sync on the one link:\n%s", steps)
	}
}

// The source-credentials step (where a SASL/SCRAM listener is added) comes before the
// link step, and its references name the right step.
func TestMigrationSteps_CredentialsBeforeLink(t *testing.T) {
	steps := stepsSection(t, stepsMarkdown(t, map[string]string{
		"source_platform": "apache-kafka", "source_cloud": "aws", "source_auth": "[sasl-plain]",
	}))
	creds := strings.Index(steps, "**Step 2: prepare your source credentials.**")
	link := strings.Index(steps, "**Step 3: build the migration link.**")
	if creds < 0 || link < creds {
		t.Fatalf("want credentials (Step 2) before the link step (Step 3):\n%s", steps)
	}
	if !strings.Contains(steps, "in place before you apply Step 3, the migration link") {
		t.Errorf("credentials note should point at the link step by number:\n%s", steps)
	}
	if !strings.Contains(steps, "(see Step 2 above)") {
		t.Errorf("link placeholder should point back at the credentials step by number:\n%s", steps)
	}
	if strings.Contains(steps, "migration-link step") || strings.Contains(steps, "source-credentials step above") {
		t.Errorf("unresolved step references:\n%s", steps)
	}
}

// Type 4 (a jump cluster) signs in to the source with SCRAM, so a Kerberos, mTLS, SASL/PLAIN or
// unauthenticated source gets no "jump cluster instead of a listener" alternative.
func TestMigrationSteps_NoJumpAlternativeForKerberosOrPlaintext(t *testing.T) {
	for _, auth := range []string{"[kerberos]", "[unauth]", "[mtls]", "[sasl-plain]"} {
		steps := stepsSection(t, stepsMarkdown(t, map[string]string{
			"source_platform": "apache-kafka", "source_cloud": "aws", "source_auth": auth,
		}))
		if strings.Contains(steps, "Alternative:") || strings.Contains(steps, "--type 4") || strings.Contains(steps, "jump cluster") {
			t.Errorf("%s: unexpected jump-cluster alternative:\n%s", auth, steps)
		}
	}
}

// On a Gateway-mediated plan the Auth reason must not tell clients to move to API keys at
// cutover: they keep their credentials through the Gateway and move afterwards.
func TestPlan_GatewayAuthReasonKeepsCredentials(t *testing.T) {
	for _, src := range []string{"[scram]", "[mtls]"} {
		md := stepsMarkdown(t, map[string]string{"downtime_tolerance": "zero", "source_auth": src})
		if strings.Contains(md, "move to API keys (SASL/PLAIN) at cutover") {
			t.Errorf("%s: gateway plan still moves clients to API keys at cutover", src)
		}
		if !strings.Contains(md, "keep their current credentials") {
			t.Errorf("%s: gateway auth reason missing credential-keeping copy", src)
		}
	}
}

// A Gateway cutover leaves client credentials alone (the Gateway swaps them) and lists
// the Gateway's backend secrets as a prerequisite.
func TestMigrationSteps_GatewayKeepsClientCredentials(t *testing.T) {
	steps := stepsSection(t, stepsMarkdown(t, map[string]string{"downtime_tolerance": "zero"}))
	if strings.Contains(steps, "switch its security config") {
		t.Errorf("Gateway clients keep their source credentials:\n%s", steps)
	}
	for _, want := range []string{
		"it keeps its source credentials, and the Gateway swaps them for Confluent Cloud credentials (`auth: swap`)",
		"backend secrets",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("Gateway steps missing %q:\n%s", want, steps)
		}
	}
}

// Start fresh moves producers first, drains consumers on the old cluster, then moves
// them from the earliest offset, and ends by retiring the source.
func TestMigrationSteps_StartFreshCutoverAndDecommission(t *testing.T) {
	steps := stepsSection(t, stepsMarkdown(t, map[string]string{"move_existing_data": "false"}))
	for _, want := range []string{
		"Move your producers to the new cluster first, let your consumers finish reading the old cluster (lag zero), then move them, starting from the earliest offset. The old cluster then ages out over its retention window.",
		"**Step 4: decommission.** Retire your source cluster once your consumers have finished reading it.",
	} {
		if !strings.Contains(steps, want) {
			t.Errorf("start-fresh steps missing %q:\n%s", want, steps)
		}
	}
}

// Each self-serve mechanism ends with a decommission step that tears down only what it
// stood up.
func TestMigrationSteps_DecommissionPerMechanism(t *testing.T) {
	for _, tc := range []struct {
		name      string
		overrides map[string]string
		want      string
		notWant   string
	}{
		{"private cluster link", map[string]string{"source_auth": "[scram]"},
			"Delete the cluster link and the Egress PrivateLink Endpoint, then retire your source cluster.", "keep it"},
		{"connector egress", map[string]string{"cc_egress_required": "true"},
			"Delete the cluster link and the Egress PrivateLink Endpoint (keep it if your connectors still use it), then retire your source cluster.", ""},
		{"jump cluster", map[string]string{"source_auth": "[iam]"},
			"Delete both cluster links, the jump cluster, and its PrivateLink endpoint, then retire your source cluster.", ""},
		{"specialist-wired link", map[string]string{"source_platform": "apache-kafka", "source_cloud": "azure", "source_auth": "[scram]"},
			"Delete the cluster link and the Egress PrivateLink Endpoint, then retire your source cluster.", ""},
		{"replicator", map[string]string{"kafka_version": "older"},
			"Stop Replicator and its Connect worker, then retire your source cluster.", ""},
	} {
		steps := stepsSection(t, stepsMarkdown(t, tc.overrides))
		_, last, ok := strings.Cut(steps, "decommission.** ")
		if !ok || !strings.HasPrefix(last, tc.want) {
			t.Errorf("%s: decommission step = %q, want %q\n%s", tc.name, last, tc.want, steps)
			continue
		}
		if tc.notWant != "" && strings.Contains(strings.SplitN(last, "\n", 2)[0], tc.notWant) {
			t.Errorf("%s: decommission step should not mention %q: %q", tc.name, tc.notWant, last)
		}
	}
}

// A schema migration that leaves the Confluent Cloud Schema Registry in IMPORT mode is
// closed out at cutover; one that doesn't mention it isn't.
func TestMigrationSteps_SchemaCutoverNote(t *testing.T) {
	const exporter = "At cutover, stop the schema exporter and set the Confluent Cloud Schema Registry back to READWRITE, so producers can register new schemas."
	linking := map[string]string{
		"schema_registry": "cp-enterprise-7.1", "schema_strategy": "migrate", "schema_reachable_to_cc": "yes",
	}
	if steps := stepsSection(t, stepsMarkdown(t, linking)); !strings.Contains(steps, exporter) {
		t.Errorf("Schema Linking cutover should stop the exporter:\n%s", steps)
	}
	replicator := map[string]string{
		"schema_registry": "cp-enterprise-7.1", "schema_strategy": "migrate", "schema_reachable_to_cc": "no",
	}
	if steps := stepsSection(t, stepsMarkdown(t, replicator)); !strings.Contains(steps, "At cutover, stop Replicator and set the Confluent Cloud Schema Registry back to READWRITE") {
		t.Errorf("Replicator schema cutover should stop Replicator:\n%s", steps)
	}
	if steps := stepsSection(t, stepsMarkdown(t, nil)); strings.Contains(steps, "READWRITE") {
		t.Errorf("a schemaless plan has no registry to reopen:\n%s", steps)
	}
}

// A cluster with an open required question that no verdict waits on (source_platform)
// is not "Ready", in the summary, the section status, and plan.json.
func TestPlanState_OpenRequiredQuestionIsNotReady(t *testing.T) {
	declared, _, err := ParseDeclaredInputs([]byte(stepsInputs(map[string]string{"source_platform": "", "source_auth": ""})))
	if err != nil {
		t.Fatal(err)
	}
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(ScanlessState(), declared, "", fixed)
	cp := ep.Clusters[0]
	if !hasOpenRequired(cp.Questions, "source_platform") {
		t.Fatalf("test setup: source_platform should be open")
	}
	if cp.InfraState == StateReady || cp.AppState == StateReady {
		t.Errorf("states = %q / %q, want neither Ready while a required question is open", cp.InfraState, cp.AppState)
	}
	md := RenderEnginePlanMarkdown(ep)
	if strings.Contains(md, "| Ready |") || strings.Contains(md, "\n**Ready**\n") {
		t.Errorf("no half should read Ready:\n%s", md)
	}
}

// The mechanism placeholder only says "your scan didn't record one" for a scanned run.
func TestLinkStep_ScramMechanismPlaceholderIsScanAware(t *testing.T) {
	cp := ClusterPlan{
		ClusterID: "kafka", SourcePlatform: "Apache Kafka", effectiveSourceAuths: []string{SourceAuthSCRAM},
		MigrationInfra: MigrationInfra{Type: 2, CCType: "commercial", Label: "Private source", Rationale: "Private."},
	}
	render := func(state string) string {
		md := markdown.New()
		n := 0
		linkStep(md, cp, state, func(title string) string { n++; return "Step " + itoa(n) + ": " + title }, linkRefs{})
		return md.String()
	}
	if got := render("kcp-state.json"); !strings.Contains(got, "(your scan didn't record one)") {
		t.Errorf("scanned run should say the scan recorded none:\n%s", got)
	}
	got := render("")
	if strings.Contains(got, "scan") || !strings.Contains(got, "the mechanism your source's SASL/SCRAM listener uses.") {
		t.Errorf("scanless run must not mention a scan:\n%s", got)
	}
}

// A link that needs no Egress PrivateLink Endpoint (for example a public source)
// decommissions just the link.
func TestDecommissionStep_LinkWithoutEgressEndpoint(t *testing.T) {
	cp := ClusterPlan{MigrationInfra: MigrationInfra{Type: 1}}
	md := markdown.New()
	decommissionStep(md, cp, func(title string) string { return "**Step 9: " + title + "**" })
	if got := md.String(); !strings.Contains(got, "Delete the cluster link, then retire your source cluster.") || strings.Contains(got, "Egress") {
		t.Errorf("got %q", got)
	}
}
