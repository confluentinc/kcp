package plan

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

// renderSteps runs the per-app schema+connector step builder and returns the
// rendered markdown, so tests can assert what a cluster's steps say.
func renderSteps(cp ClusterPlan) string {
	md := markdown.New()
	n := 0
	step := func(title string) string { n++; return "Step " + itoa(n) + ": " + title }
	perAppDataOps(md, cp, "kcp-state.json", step)
	pendingDataOpsNote(md, cp) // mirrors renderMigrationInfra: steps, then the held-steps note
	return md.String()
}

func schemaPlan(value string) engine.PlanResult {
	kind := map[string]engine.SchemaKind{
		"Schema Linking":                     engine.SchemaKindLinking,
		"Glue bulk re-registration":          engine.SchemaKindGlueBulk,
		"Start with a fresh Schema Registry": engine.SchemaKindFresh,
		"Replicator":                         engine.SchemaKindReplicator,
		"Special handling":                   engine.SchemaKindSpecial,
		"Schemaless":                         engine.SchemaKindSchemaless,
		"Pending":                            engine.SchemaKindPending,
	}[value]
	return engine.PlanResult{Schema: engine.SchemaResult{Value: value, Kind: kind}}
}

// A single-app cluster whose Schema settles as a migration shows one unqualified
// schema step (no app-name scoping, since there is only the implicit app).
func TestSchemaStep_SingleAppMigrates(t *testing.T) {
	out := renderSteps(ClusterPlan{Plan: schemaPlan("Schema Linking")})
	if !strings.Contains(out, "migrate your schemas.") {
		t.Errorf("expected an unqualified schema step, got:\n%s", out)
	}
	if strings.Contains(out, " for `") {
		t.Errorf("single app should not scope by name:\n%s", out)
	}
}

// A pending Schema verdict produces no concrete schema step (no command), but is
// surfaced in the "Still to come" note with the question that unblocks it.
func TestSchemaStep_SingleAppPendingShowsNoStep(t *testing.T) {
	cp := ClusterPlan{
		Plan:       schemaPlan("Pending"),
		Contingent: map[string][]string{nodeSchema: {"schema_strategy"}},
	}
	out := renderSteps(cp)
	if strings.Contains(out, "kcp create-asset migrate-schemas") {
		t.Errorf("pending schema should produce no command, got:\n%s", out)
	}
	if !strings.Contains(out, "Still to come") || !strings.Contains(out, "migrate your schemas, once you answer `schema_strategy`") {
		t.Errorf("pending schema should appear in the Still to come note, got:\n%s", out)
	}
}

// A schemaless app has no schema work, so no step.
func TestSchemaStep_SchemalessShowsNoStep(t *testing.T) {
	if out := renderSteps(ClusterPlan{Plan: schemaPlan("Schemaless")}); strings.Contains(out, "migrate your schemas") {
		t.Errorf("schemaless should show no step, got:\n%s", out)
	}
}

// Two apps that both migrate schemas share one unqualified step (naming every app
// would be noise).
func TestSchemaStep_MultiAppAllMigrate(t *testing.T) {
	cp := ClusterPlan{Apps: []AppPlan{
		{Name: "orders", Plan: schemaPlan("Schema Linking")},
		{Name: "billing", Plan: schemaPlan("Glue bulk re-registration")},
	}}
	out := renderSteps(cp)
	if !strings.Contains(out, "migrate your schemas.") {
		t.Errorf("all-migrate should be one unqualified step, got:\n%s", out)
	}
	if strings.Contains(out, " for `") {
		t.Errorf("all-migrate should not scope by name, got:\n%s", out)
	}
}

// When apps diverge (one migrates, one starts fresh) the step is scoped to the
// migrating app by name; the fresh app contributes no schema step.
func TestSchemaStep_MultiAppDivergentStrategies(t *testing.T) {
	cp := ClusterPlan{Apps: []AppPlan{
		{Name: "orders", Plan: schemaPlan("Schema Linking")},
		{Name: "billing", Plan: schemaPlan("Start with a fresh Schema Registry")},
	}}
	out := renderSteps(cp)
	if !strings.Contains(out, "migrate your schemas for `orders`.") {
		t.Errorf("divergent strategies should scope to `orders`, got:\n%s", out)
	}
	if strings.Contains(out, "billing") {
		t.Errorf("fresh-registry app should not appear in the schema step, got:\n%s", out)
	}
}

// A settled migrating app plus a pending app: the step scopes to the settled app
// and notes the pending one rather than dropping it.
func TestSchemaStep_MultiAppSettledPlusPending(t *testing.T) {
	cp := ClusterPlan{Apps: []AppPlan{
		{Name: "orders", Plan: schemaPlan("Schema Linking")},
		{Name: "billing", Plan: schemaPlan("Pending"), Contingent: map[string][]string{nodeSchema: {"schema_strategy"}}},
	}}
	out := renderSteps(cp)
	if !strings.Contains(out, "migrate your schemas for `orders`.") {
		t.Errorf("should scope the settled step to the migrating app, got:\n%s", out)
	}
	if !strings.Contains(out, "migrate your schemas for `billing`, once you answer `schema_strategy`") {
		t.Errorf("should note the pending app in the Still to come note, got:\n%s", out)
	}
}

// The Glue schema step prints a migrate-schemas command with the scanned registry
// name and region filled in.
func TestSchemaStep_GlueCommandFromScan(t *testing.T) {
	cp := ClusterPlan{
		Plan:           schemaPlan("Glue bulk re-registration"),
		MigrationInfra: MigrationInfra{CCType: "commercial"},
		SchemaSource:   &SchemaSourceRef{GlueRegistry: "orders-registry", GlueRegion: "us-east-1"},
	}
	out := renderSteps(cp)
	for _, want := range []string{
		"kcp create-asset migrate-schemas",
		"--glue-registry orders-registry",
		"--region us-east-1",
		"--cc-type commercial",
		"--cc-sr-rest-endpoint",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("glue schema step missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--url") {
		t.Errorf("glue step must not use --url:\n%s", out)
	}
}

// The Schema Linking step prints a migrate-schemas --url command, filled from the
// scanned Confluent Schema Registry when known.
func TestSchemaStep_SchemaLinkingCommand(t *testing.T) {
	cp := ClusterPlan{
		Plan:           schemaPlan("Schema Linking"),
		MigrationInfra: MigrationInfra{CCType: "commercial"},
		SchemaSource:   &SchemaSourceRef{ConfluentURL: "https://sr.internal:8081"},
	}
	out := renderSteps(cp)
	if !strings.Contains(out, "--url https://sr.internal:8081") {
		t.Errorf("schema-linking step should carry the scanned SR url:\n%s", out)
	}
	if strings.Contains(out, "--glue-registry") {
		t.Errorf("schema-linking step must not use --glue-registry:\n%s", out)
	}
}

// With no scanned registry, the command still renders as a runnable shape with a
// placeholder rather than being dropped.
func TestSchemaStep_GlueCommandPlaceholderWhenNoScan(t *testing.T) {
	cp := ClusterPlan{
		Plan:           schemaPlan("Glue bulk re-registration"),
		MigrationInfra: MigrationInfra{CCType: "commercial"},
	}
	if out := renderSteps(cp); !strings.Contains(out, "--glue-registry <glue-registry-name>") {
		t.Errorf("expected a placeholder registry, got:\n%s", out)
	}
}

// A tech-assist schema (Replicator) has no self-serve migrate-schemas command, but a
// reader still needs to move their schemas — so it renders a schema STEP that names
// Replicator and hands off to a specialist, rather than being skipped entirely.
func TestSchemaStep_ReplicatorTechAssistStep(t *testing.T) {
	out := renderSteps(ClusterPlan{Plan: schemaPlan("Replicator")})
	if !strings.Contains(out, "migrate your schemas") {
		t.Errorf("a Replicator (tech-assist) schema should show a schema step, got:\n%s", out)
	}
	if !strings.Contains(out, "Confluent Replicator") {
		t.Errorf("the Replicator schema step should name Confluent Replicator, got:\n%s", out)
	}
	if !strings.Contains(out, "[Talk to a person](#talk-to-a-person)") {
		t.Errorf("the Replicator schema step should hand off to a specialist, got:\n%s", out)
	}
	if strings.Contains(out, "kcp create-asset migrate-schemas") {
		t.Errorf("the Replicator schema step has no self-serve migrate-schemas command, got:\n%s", out)
	}
}

// "Special handling" (a third-party / unidentified registry) also has no self-serve
// command; it renders a schema STEP that says the move is yours to run and hands off.
func TestSchemaStep_SpecialHandlingTechAssistStep(t *testing.T) {
	out := renderSteps(ClusterPlan{Plan: schemaPlan("Special handling")})
	if !strings.Contains(out, "migrate your schemas") {
		t.Errorf("a Special-handling schema should show a schema step, got:\n%s", out)
	}
	if !strings.Contains(out, "[Talk to a person](#talk-to-a-person)") {
		t.Errorf("the Special-handling schema step should hand off to a specialist, got:\n%s", out)
	}
	if strings.Contains(out, "kcp create-asset migrate-schemas") {
		t.Errorf("the Special-handling schema step has no self-serve migrate-schemas command, got:\n%s", out)
	}
}

func TestAccessControlNote(t *testing.T) {
	note := func(auths []string, kind string, startFresh bool) string {
		cp := ClusterPlan{effectiveSourceAuths: auths, MigrationInfra: MigrationInfra{Kind: kind}}
		cp.Plan.Switchover.StartFresh = startFresh
		md := markdown.New()
		accessControlNote(md, cp)
		return md.String()
	}
	// Pure IAM: points at the migrate-acls iam command and the Authentication
	// recommendation above, which carries the specifics.
	if s := note([]string{SourceAuthIAM}, "cluster_link", false); !strings.Contains(s, "`kcp create-asset migrate-acls iam`") || strings.Contains(s, "migrate-acls kafka") {
		t.Errorf("IAM note = %q", s)
	}
	// Mixed IAM + SASL/SCRAM: names both commands.
	if s := note([]string{SourceAuthIAM, SourceAuthSCRAM}, "cluster_link", false); !strings.Contains(s, "`kcp create-asset migrate-acls iam` and `kcp create-asset migrate-acls kafka`") {
		t.Errorf("IAM+SCRAM should mention both: %q", s)
	}
	// IAM alongside any other ACL-bearing method names both commands.
	for _, other := range []string{SourceAuthMTLS, SourceAuthSASLPlain, SourceAuthKerberos} {
		if s := note([]string{SourceAuthIAM, other}, "cluster_link", false); !strings.Contains(s, "`kcp create-asset migrate-acls iam` and `kcp create-asset migrate-acls kafka`") {
			t.Errorf("IAM+%s should mention both: %q", other, s)
		}
	}
	// Kerberos alongside unauthenticated is ACL-bearing, not unauthenticated-only.
	if s := note([]string{SourceAuthKerberos, SourceAuthUnauth}, "cluster_link", false); strings.Contains(s, "no authorization to carry over") || !strings.Contains(s, "`kcp create-asset migrate-acls kafka`") {
		t.Errorf("Kerberos+unauth note = %q", s)
	}
	// Unauthenticated only: no authorization to carry over.
	if s := note([]string{SourceAuthUnauth}, "cluster_link", false); !strings.Contains(s, "no authorization to carry over") {
		t.Errorf("unauth note = %q", s)
	}
	// SCRAM (or mTLS): points at the migrate-acls kafka command.
	if s := note([]string{SourceAuthSCRAM}, "cluster_link", false); !strings.Contains(s, "`kcp create-asset migrate-acls kafka`") {
		t.Errorf("scram note = %q", s)
	}
	// Start-fresh reframes the timing (no cutover/mirror).
	if s := note([]string{SourceAuthSCRAM}, "start_fresh", true); !strings.Contains(s, "before you point your clients at the new cluster") {
		t.Errorf("start-fresh timing = %q", s)
	}
}

func connectorsPlan(value string) engine.PlanResult {
	kind := map[string]engine.ConnectorsKind{
		"Rebuild as Confluent-managed connectors":       engine.ConnectorsKindManaged,
		"Run them yourself on your own Connect cluster": engine.ConnectorsKindSelfManaged,
		"None to move": engine.ConnectorsKindNone,
		"Not assessed": engine.ConnectorsKindNotAssessed,
	}[value]
	return engine.PlanResult{Connectors: engine.ConnectorsResult{Value: value, Kind: kind}}
}

// renderMigration runs the full migration-steps renderer for a cluster.
func renderMigration(cp ClusterPlan) string {
	md := markdown.New()
	renderMigrationInfra(md, cp, "kcp-state.json")
	return md.String()
}

// enterpriseClusterLinkPlan is a settled Enterprise cluster-link plan (type 2),
// for exercising the migration-steps gating.
func enterpriseClusterLinkPlan() ClusterPlan {
	return ClusterPlan{
		Arn: "arn:aws:kafka:us-east-1:1:cluster/c/abc",
		Plan: engine.PlanResult{
			ClusterType: engine.ClusterTypeResult{Value: engine.TierEnterprise, Tier: engine.TierEnterprise},
			Networking:  engine.NetworkingResult{Value: "PNI"},
			Auth:        engine.AuthResult{Value: "API keys (SASL/PLAIN)"},
			Switchover:  engine.SwitchoverResult{Value: "Pending", Held: true},
		},
		MigrationInfra: MigrationInfra{Type: 2, CCType: "commercial", Rationale: "Private source with SASL/SCRAM.", Label: "external outbound cluster link"},
	}
}

// The mechanism is settled when Data migration is not held, or held only on the
// cutover-style question; any other pending driver leaves it unsettled.
func TestMechanismSettled(t *testing.T) {
	if !mechanismSettled(ClusterPlan{}) {
		t.Error("no contingent should be settled")
	}
	if !mechanismSettled(ClusterPlan{Contingent: map[string][]string{nodeDataMigration: {"downtime_tolerance"}}}) {
		t.Error("downtime-only should be settled (style, not mechanism)")
	}
	if mechanismSettled(ClusterPlan{Contingent: map[string][]string{nodeDataMigration: {"move_existing_data"}}}) {
		t.Error("move_existing_data pending should be unsettled")
	}
	if mechanismSettled(ClusterPlan{Contingent: map[string][]string{nodeDataMigration: {"downtime_tolerance", "move_existing_data"}}}) {
		t.Error("any non-style driver should be unsettled")
	}
}

// Infra pending holds the whole section (no target cluster can be described).
func TestMigrationSteps_InfraPendingHolds(t *testing.T) {
	cp := ClusterPlan{Contingent: map[string][]string{nodeClusterType: {"use_case_breadth"}}}
	out := renderMigration(cp)
	if !strings.Contains(out, "once the infrastructure plan above is settled") {
		t.Errorf("expected an infra-pending hold:\n%s", out)
	}
	if strings.Contains(out, "Step 1") {
		t.Errorf("no steps should render while infra is pending:\n%s", out)
	}
}

// Infra settled but a mechanism-changing answer pending: the target cluster step
// shows with a note, but not the data-movement (link/cutover) steps.
func TestMigrationSteps_MechanismPendingShowsClusterOnly(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Contingent = map[string][]string{nodeDataMigration: {"move_existing_data"}}
	out := renderMigration(cp)
	if !strings.Contains(out, "create the target cluster") {
		t.Errorf("should show the target cluster step:\n%s", out)
	}
	if !strings.Contains(out, "data-movement and cutover steps become available") {
		t.Errorf("should note the pending data-movement steps:\n%s", out)
	}
	if strings.Contains(out, "build the migration link") {
		t.Errorf("must not show the link step while the mechanism is pending:\n%s", out)
	}
}

// Infra settled and Data migration held only on cutover style (downtime): the full
// cluster-link steps show, because the mechanism itself is known.
func TestMigrationSteps_StylePendingShowsFullSteps(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Contingent = map[string][]string{nodeDataMigration: {"downtime_tolerance"}}
	out := renderMigration(cp)
	if !strings.Contains(out, "build the migration link") {
		t.Errorf("a style-only hold should still show the link step:\n%s", out)
	}
	if !strings.Contains(out, "run the cutover") {
		t.Errorf("should show the cutover step:\n%s", out)
	}
}

// A plain (non-Gateway) Cluster Linking cutover has no `kcp migration` tool to run —
// it's walked through by hand — so the plan must never mention it.
func TestClusterLinkCutover_PlainHasNoKcpMigration(t *testing.T) {
	out := renderMigration(enterpriseClusterLinkPlan())
	if strings.Contains(out, "kcp migration") {
		t.Errorf("a plain Cluster Linking cutover must not mention kcp migration, got:\n%s", out)
	}
	if !strings.Contains(out, "promote the mirror topics") {
		t.Errorf("expected the plain cutover's mirror-promotion wording, got:\n%s", out)
	}
}

// A Gateway-mediated cutover is driven by `kcp migration`, the CPC Gateway cutover
// tool, so the plan must walk through its stages.
func TestClusterLinkCutover_GatewayMediatedUsesKcpMigration(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	out := renderMigration(cp)
	for _, want := range []string{
		"Deploy the Confluent Gateway with Confluent for Kubernetes and route your clients through it",
		"`GatewayMigration` manifest",
		"`kcp migration lag-check --migration-yaml <your-gateway-migration.yaml>`",
		"`kcp migration execute --migration-yaml <your-gateway-migration.yaml>`",
		"`--dry-run`",
		"point it at the **Confluent Gateway**",
		"**Backing out:** before `execute`, you can back out by leaving clients on the source. Once `execute` promotes the mirror topics, rolling back needs a new cluster link in the other direction.",
		"`confluent kafka link configuration update <your-link-name> --config <your-link-config-file>`",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("gateway-mediated cutover missing %q, got:\n%s", want, out)
		}
	}
	// The Gateway path must not tell clients to bypass the Gateway or the user to
	// repoint at the source.
	for _, bad := range []string{"new Confluent Cloud **bootstrap endpoint**", "point your clients at the still-running source"} {
		if strings.Contains(out, bad) {
			t.Errorf("gateway-mediated cutover must not say %q, got:\n%s", bad, out)
		}
	}
}

// The plain cutover enables offset sync before cutover and its steps follow the
// cutover style: all clients at once, or service by service.
func TestClusterLinkCutover_PlainStepsFollowStyle(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.CutoverStyle = "Cluster Linking, service by service (Stop-Restart-Repeat)"
	out := renderMigration(cp)
	for _, want := range []string{"Before you cut over, enable consumer offset sync on the cluster link: set consumer.offset.sync.enable=true and list the consumer groups to migrate in consumer.offset.group.filters (it defaults to none), so consumer offsets come across too.", "Cut over one service at a time", "kafka link configuration update"} {
		if !strings.Contains(out, want) {
			t.Errorf("per-service cutover missing %q, got:\n%s", want, out)
		}
	}
	if strings.Contains(out, "when you create the link") || strings.Contains(out, "stop your producers and consumers, wait for the mirror") {
		t.Errorf("per-service cutover must not carry all-at-once steps, got:\n%s", out)
	}
	cp.Plan.Switchover.CutoverStyle = "Cluster Linking, all at once (Restart-All-At-Once)"
	if out := renderMigration(cp); !strings.Contains(out, "stop your producers and consumers, wait for the mirror") {
		t.Errorf("all-at-once cutover missing all-at-once steps, got:\n%s", out)
	}
}

// The connector start cue matches the cutover style: a plain Cluster Linking
// cutover has no `kcp migration` tool to point at, so it says to wait until the
// mirror topics are promoted; a Gateway-mediated one names the command that
// promotes them.
func TestConnectorsStep_StartCueMatchesCutoverStyle(t *testing.T) {
	base := ClusterPlan{
		Arn:             "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		Plan:            connectorsPlan("Rebuild as Confluent-managed connectors"),
		ConnectorSource: &ConnectorSource{MSKConnect: true},
	}
	if out := renderSteps(base); !strings.Contains(out, "after you promote the mirror topics") || strings.Contains(out, "kcp migration") {
		t.Errorf("plain cluster-linking start cue wrong, got:\n%s", out)
	}
	gw := base
	gw.Plan.Switchover.GatewayMediated = true
	if out := renderSteps(gw); !strings.Contains(out, "after `kcp migration execute` promotes the mirror topics") {
		t.Errorf("gateway-mediated start cue wrong, got:\n%s", out)
	}
}

// A cluster rebuilding MSK Connect connectors as Confluent-managed prints the
// `migrate-connectors msk` command with the shared CC flags.
func TestConnectorsStep_MSKConnectCommand(t *testing.T) {
	cp := ClusterPlan{
		Arn:             "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		Plan:            connectorsPlan("Rebuild as Confluent-managed connectors"),
		ConnectorSource: &ConnectorSource{MSKConnect: true},
	}
	out := renderSteps(cp)
	for _, want := range []string{
		"kcp create-asset migrate-connectors msk",
		"--state-file kcp-state.json",
		"arn:aws:kafka",
		"--cc-environment-id",
		"--cc-cluster-id",
		"--cc-api-key",
		"--cc-api-secret",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("msk-connect step missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "migrate-connectors self-managed") {
		t.Errorf("msk-only cluster should not emit a self-managed command:\n%s", out)
	}
}

// Connector definitions are generated before cutover but applied only after the mirror
// topics are promoted. Only self-managed Kafka Connect gets the Connect Migration Utility;
// MSK Connect gets the generated definitions and a note on how offsets behave.
func TestConnectorsStep_ApplyAfterPromoteAndUtilityOnlyForSelfManaged(t *testing.T) {
	const apply = "Generate these Confluent-managed connector definitions now, but apply them only after you promote the mirror topics, so they don't start early"
	const utility = "use the Connect Migration Utility with `stop_create_latest_offset`"
	cp := ClusterPlan{
		Arn:             "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		Plan:            connectorsPlan("Rebuild as Confluent-managed connectors"),
		ConnectorSource: &ConnectorSource{MSKConnect: true},
	}
	cp.Plan.Switchover = engine.SwitchoverResult{}
	out := renderSteps(cp)
	for _, want := range []string{apply, "Confluent-managed sink connectors read with their own consumer group", "Source connectors don't carry offsets over"} {
		if !strings.Contains(out, want) {
			t.Errorf("MSK Connect step missing %q:\n%s", want, out)
		}
	}
	for _, bad := range []string{utility, "connector-utility", "leave them **stopped**"} {
		if strings.Contains(out, bad) {
			t.Errorf("MSK Connect step must not contain %q:\n%s", bad, out)
		}
	}
	cp.SourcePlatform = enginePlatformOSK
	cp.ConnectorSource = &ConnectorSource{SelfManaged: true}
	out = renderSteps(cp)
	if !strings.Contains(out, utility) || !strings.Contains(out, "Use one creation path per connector: the utility or generated definitions, not both.") || strings.Contains(out, "sink connectors read with") {
		t.Errorf("self-managed step should recommend the utility with one creation path:\n%s", out)
	}
	cp.ConnectorSource = &ConnectorSource{MSKConnect: true, SelfManaged: true}
	out = renderSteps(cp)
	if !strings.Contains(out, "the utility for self-managed Connect, the generated definitions for MSK Connect, not both for the same connector.") {
		t.Errorf("mixed fleet step missing the per-source creation path:\n%s", out)
	}
}

// MSK Serverless has no Kafka Admin API scan, so migrate-topics would find no topics: the
// plan has the customer create the mirror topics by hand or auto-create them on the link.
func TestTopicsStep_ServerlessMirrorIsManual(t *testing.T) {
	cp := ClusterPlan{Arn: "arn:aws:kafka:us-east-1:1:cluster/orders/abc", IsServerless: true}
	md := markdown.New()
	topicsStep(md, cp, "", migrateTopicsModeMirror, func(s string) string { return "**Step 1: " + s + "**" })
	out := md.String()
	for _, want := range []string{"confluent kafka mirror create <topic> --link <your-link-name>", "auto-create mirror topics"} {
		if !strings.Contains(out, want) {
			t.Errorf("serverless topics step missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "--mode mirror") {
		t.Errorf("serverless topics step still emits migrate-topics --mode mirror:\n%s", out)
	}
	md = markdown.New()
	topicsStep(md, cp, "", migrateTopicsModeNew, func(s string) string { return s })
	if out := md.String(); !strings.Contains(out, "confluent kafka topic create <topic> --partitions <n>") || strings.Contains(out, "--mode new") {
		t.Errorf("serverless start-fresh topics step:\n%s", out)
	}
	cp.IsServerless = false
	md = markdown.New()
	topicsStep(md, cp, "", migrateTopicsModeMirror, func(s string) string { return s })
	if !strings.Contains(md.String(), "--mode mirror") {
		t.Errorf("provisioned topics step lost migrate-topics --mode mirror:\n%s", md.String())
	}
}

// A Glue migration closes with the serializer switch; a start-fresh plan has no mirrored
// Glue-format history to mention.
func TestSchemaCutoverNote_Glue(t *testing.T) {
	cp := ClusterPlan{Plan: schemaPlan("Glue bulk re-registration")}
	md := markdown.New()
	schemaCutoverNote(md, cp)
	const first = "At cutover, switch your clients from the AWS Glue Schema Registry serializers to Confluent Schema Registry serializers."
	const second = "Messages already mirrored keep their Glue format, so consumers that read that history still need the Glue deserializer."
	if out := md.String(); !strings.Contains(out, first+" "+second) {
		t.Errorf("glue cutover note = %q", out)
	}
	cp.Plan.Switchover.StartFresh = true
	md = markdown.New()
	schemaCutoverNote(md, cp)
	if out := md.String(); !strings.Contains(out, first) || strings.Contains(out, second) {
		t.Errorf("start-fresh glue cutover note = %q", out)
	}
}

// Self-managed connectors get the `self-managed` subcommand with --source-type.
func TestConnectorsStep_SelfManagedCommand(t *testing.T) {
	cp := ClusterPlan{
		Arn:             "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		Plan:            connectorsPlan("Rebuild as Confluent-managed connectors"),
		ConnectorSource: &ConnectorSource{SelfManaged: true},
	}
	out := renderSteps(cp)
	if !strings.Contains(out, "kcp create-asset migrate-connectors self-managed") {
		t.Errorf("expected the self-managed subcommand:\n%s", out)
	}
	if !strings.Contains(out, "--source-type msk") {
		t.Errorf("self-managed command needs --source-type:\n%s", out)
	}
	if strings.Contains(out, "migrate-connectors msk \\") {
		t.Errorf("self-managed-only cluster should not emit the msk subcommand:\n%s", out)
	}
}

// A cluster running both runtimes prints both subcommands.
func TestConnectorsStep_BothRuntimes(t *testing.T) {
	cp := ClusterPlan{
		Arn:             "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		Plan:            connectorsPlan("Rebuild as Confluent-managed connectors"),
		ConnectorSource: &ConnectorSource{MSKConnect: true, SelfManaged: true},
	}
	out := renderSteps(cp)
	if !strings.Contains(out, "migrate-connectors msk") || !strings.Contains(out, "migrate-connectors self-managed") {
		t.Errorf("both runtimes should print both subcommands:\n%s", out)
	}
}

// Keeping connectors self-managed (running your own Connect cluster) has no
// migrate-connectors command, so it produces no step.
func TestConnectorsStep_KeepSelfManagedNoStep(t *testing.T) {
	cp := ClusterPlan{
		Plan:            connectorsPlan("Run them yourself on your own Connect cluster"),
		ConnectorSource: &ConnectorSource{MSKConnect: true},
	}
	if out := renderSteps(cp); strings.Contains(out, "rebuild your connectors") {
		t.Errorf("keep-self-managed should show no migrate-connectors step:\n%s", out)
	}
}

// "None to move" produces no connector step.
func TestConnectorsStep_NoneToMoveNoStep(t *testing.T) {
	if out := renderSteps(ClusterPlan{Plan: connectorsPlan("None to move")}); strings.Contains(out, "rebuild your connectors") {
		t.Errorf("none-to-move should show no step:\n%s", out)
	}
}

// A withheld app is excluded from the step scope entirely (it has no automated plan).
func TestSchemaStep_MultiAppWithheldExcluded(t *testing.T) {
	cp := ClusterPlan{Apps: []AppPlan{
		{Name: "orders", Plan: schemaPlan("Schema Linking")},
		{Name: "shared-fabric", Plan: engine.PlanResult{Withheld: true}},
	}}
	out := renderSteps(cp)
	// orders is the only non-withheld app, so it's "every app" -> unqualified.
	if !strings.Contains(out, "migrate your schemas.") {
		t.Errorf("withheld app should be excluded, leaving one unqualified step, got:\n%s", out)
	}
	if strings.Contains(out, "shared-fabric") {
		t.Errorf("withheld app must not appear in a step, got:\n%s", out)
	}
}

// renderLink runs the migration-link step builder and returns the rendered
// markdown, so tests can assert what the alternative-type note says.
func renderLink(cp ClusterPlan) string {
	md := markdown.New()
	n := 0
	step := func(title string) string { n++; return "Step " + itoa(n) + ": " + title }
	linkStep(md, cp, "kcp-state.json", step, linkRefs{})
	return md.String()
}

// TestLinkStep_AlternativeTypeBranch checks the two alternative-type notes: a
// direct-link alternative (from a jump type) says it connects directly with no
// jump cluster, while a jump-cluster alternative (from a direct type) calls out the
// extra jump inputs (A6).
func TestLinkStep_AlternativeTypeBranch(t *testing.T) {
	t5 := ClusterPlan{
		ClusterID: "orders", Arn: "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		MigrationInfra: MigrationInfra{
			Type: 5, CCType: "commercial", Label: "Private source with jump cluster (IAM)",
			Rationale: "Your source is private and authenticates with AWS IAM.", JumpCluster: true,
			Alternative: "add a SASL/SCRAM listener on your MSK cluster", AlternativeType: 2,
		},
	}
	out := renderLink(t5)
	if !strings.Contains(out, "`--type 2`") || !strings.Contains(out, "connects to your source directly, with no jump cluster to run") {
		t.Errorf("type-5 direct-link alternative render wrong:\n%s", out)
	}
	t2 := ClusterPlan{
		ClusterID: "orders", Arn: "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		MigrationInfra: MigrationInfra{
			Type: 2, CCType: "commercial", Label: "Private source with external outbound cluster link (SASL/SCRAM)",
			Rationale:   "Your source is private and its brokers use SASL/SCRAM.",
			Alternative: "a jump cluster in your VPC (SASL/SCRAM)", AlternativeType: 4,
		},
	}
	out2 := renderLink(t2)
	if !strings.Contains(out2, "`--type 4`") || !strings.Contains(out2, "extra jump-cluster inputs") {
		t.Errorf("type-2 jump-cluster alternative render wrong:\n%s", out2)
	}
}

// renderTargetStep renders just the "create the target cluster" step.
func renderTargetStep(cp ClusterPlan, state string) string {
	md := markdown.New()
	targetClusterStep(md, cp, state, func(t string) string { return "Step 1: " + t })
	return md.String()
}

// target-infra builds AWS PrivateLink networking and, for an MSK source, reads the VPC
// and region from the scanned cluster. The step must only emit it where it builds
// what the plan recommends.
func TestTargetClusterStep_TargetInfraCommand(t *testing.T) {
	base := func() ClusterPlan {
		cp := enterpriseClusterLinkPlan()
		cp.Plan.Networking.Value = "PrivateLink"
		cp.targetCloud = "AWS"
		return cp
	}

	// MSK + PrivateLink on AWS: the command, with the state file path as given.
	out := renderTargetStep(base(), "scans/prod/kcp-state.json")
	for _, want := range []string{"kcp create-asset target-infra", "--state-file scans/prod/kcp-state.json", "--source-cluster-id arn:aws:kafka"} {
		if !strings.Contains(out, want) {
			t.Errorf("MSK target step missing %q:\n%s", want, out)
		}
	}

	// Apache Kafka / Confluent Platform: no MSK ARN to read, so the AWS region and VPC
	// are named directly and no state / source-cluster-id flag is passed.
	osk := base()
	osk.Arn, osk.ClusterID, osk.SourcePlatform = "", "orders", "Apache Kafka"
	out = renderTargetStep(osk, "kcp-state.json")
	for _, want := range []string{"kcp create-asset target-infra", "--aws-region <your-aws-region> --vpc-id <your-vpc-id>"} {
		if !strings.Contains(out, want) {
			t.Errorf("OSK target step missing %q:\n%s", want, out)
		}
	}
	for _, unwanted := range []string{"--source-cluster-id", "--state-file"} {
		if strings.Contains(out, unwanted) {
			t.Errorf("OSK target-infra must not carry %q:\n%s", unwanted, out)
		}
	}

	// Azure / GCP target: target-infra is AWS-only, so there is no command.
	for _, cloud := range []string{"Azure", "GCP"} {
		cp := base()
		cp.targetCloud = cloud
		out = renderTargetStep(cp, "kcp-state.json")
		if strings.Contains(out, "```") || !strings.Contains(out, "builds AWS networking only") || !strings.Contains(out, "Talk to a person") {
			t.Errorf("%s target must not emit target-infra, and must say why:\n%s", cloud, out)
		}
	}

	// PNI networking: target-infra builds PrivateLink, so it is offered only as the
	// explicit alternative, never as how to build the PNI the plan recommends.
	pni := base()
	pni.Plan.Networking.Value = "PNI + Egress PrivateLink Endpoint"
	out = renderTargetStep(pni, "kcp-state.json")
	if !strings.Contains(out, "not PNI") || !strings.Contains(out, "If you would rather use PrivateLink than PNI") {
		t.Errorf("PNI plan must explain that target-infra builds PrivateLink:\n%s", out)
	}
	if strings.Index(out, "```") < strings.Index(out, "If you would rather use PrivateLink") {
		t.Errorf("PNI plan must not show a target-infra command before the PrivateLink alternative:\n%s", out)
	}
}

// Every emitted command names the state file exactly as the plan was run with, not
// just its base name.
func TestCommands_StateFilePathAsGiven(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.SourcePlatform = ""
	path := "scans/prod/kcp-state.json"
	for name, cmd := range map[string]string{
		"target-infra":    targetInfraCommand(cp, path),
		"migration-infra": migrationInfraCommand(cp, path),
		"migrate-topics":  migrateTopicsCommand(cp, migrateTopicsModeMirror, path),
		"migrate-schemas": migrateSchemasCommand(cp, engine.SchemaKindGlueBulk, path),
		"connectors":      strings.Join(migrateConnectorsCommands(cp, ConnectorSource{MSKConnect: true, SelfManaged: true}, path), "\n"),
	} {
		if !strings.Contains(cmd, "--state-file "+path) {
			t.Errorf("%s command should use the state file path as given:\n%s", name, cmd)
		}
	}
}

func renderBeforeYouStart(cp ClusterPlan, state string) string {
	md := markdown.New()
	beforeYouStart(md, cp, state)
	return md.String()
}

// "Before you start" lists only the prerequisite scans this plan's later steps need
// and the state file doesn't already hold.
func TestBeforeYouStart(t *testing.T) {
	base := func() ClusterPlan {
		cp := enterpriseClusterLinkPlan()
		cp.topicsScanned = true
		return cp
	}
	if out := renderBeforeYouStart(base(), "kcp-state.json"); out != "" {
		t.Errorf("nothing needed, got:\n%s", out)
	}

	// Schema Linking with no registry in the state file.
	sr := base()
	sr.Plan.Schema = engine.SchemaResult{Kind: engine.SchemaKindLinking}
	out := renderBeforeYouStart(sr, "scans/kcp-state.json")
	for _, want := range []string{"Before you start", "kcp scan schema-registry --state-file scans/kcp-state.json --sr-type confluent --url <source-sr-url>"} {
		if !strings.Contains(out, want) {
			t.Errorf("schema prerequisite missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "connectors") || strings.Contains(out, "Topics") {
		t.Errorf("only the schema scan is relevant:\n%s", out)
	}
	sr.schemaRegistryScanned = true
	if out := renderBeforeYouStart(sr, "kcp-state.json"); out != "" {
		t.Errorf("a registry already in state needs no scan, got:\n%s", out)
	}

	// Both connector runtimes, and a state file with no topics.
	cn := base()
	cn.topicsScanned = false
	cn.Plan.Connectors = engine.ConnectorsResult{Kind: engine.ConnectorsKindManaged}
	cn.ConnectorSource = &ConnectorSource{MSKConnect: true, SelfManaged: true}
	out = renderBeforeYouStart(cn, "kcp-state.json")
	for _, want := range []string{"kcp scan msk-connectors --state-file kcp-state.json --cluster-arn arn:aws:kafka", "kcp scan self-managed-connectors --state-file kcp-state.json --cluster-id arn:aws:kafka", "--connect-rest-url", "Topics: `kcp scan clusters --source-type msk"} {
		if !strings.Contains(out, want) {
			t.Errorf("connector/topic prerequisites missing %q:\n%s", want, out)
		}
	}
}

// A Gateway plan with mTLS clients still swaps credentials (`auth: swap`): the
// Gateway terminates TLS, so identity passthrough can't carry a certificate to
// Confluent Cloud, and Confluent Cloud sees the API key the Gateway signs in with.
func TestClusterLinkCutover_GatewayMTLSSwaps(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	for _, auth := range []string{"API keys (SASL/PLAIN)", "SASL/SCRAM"} {
		cp.Plan.Auth.Value = auth
		out := renderMigration(cp)
		if !strings.Contains(out, "`auth: swap`") || strings.Contains(out, "passthrough") || strings.Contains(out, "presents the same certificate") {
			t.Errorf("%s: Gateway plan should use auth: swap only, got:\n%s", auth, out)
		}
		if strings.Contains(out, "certificate it presents") || strings.Contains(out, "arrive as the Gateway's certificate") {
			t.Errorf("%s: a non-mTLS Gateway plan has no certificate steps:\n%s", auth, out)
		}
		if strings.Contains(out, "authenticate to your source cluster") {
			t.Errorf("%s: the source route is a passthrough, so the Gateway holds no source credentials:\n%s", auth, out)
		}
	}
	cp.Plan.Auth.Value = "API keys (SASL/PLAIN)"
	cp.effectiveSourceAuths = []string{SourceAuthMTLS}
	out := renderMigration(cp)
	for _, want := range []string{
		"the Gateway swaps each client's certificate for that client's own API key (`auth: swap`), so Confluent Cloud sees that key's service account, not the certificate",
		"one API key per client certificate CN, which is how it swaps each client certificate",
		"On the source, clients now arrive as the Gateway's certificate, so grant that certificate's principal ACLs that cover every client.",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("mTLS Gateway plan missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Give each authentication type its own Gateway route") {
		t.Errorf("a Gateway plan has one sign-in method, so no per-type routes:\n%s", out)
	}
	if i, j := strings.Index(out, "arrive as the Gateway's certificate"), strings.Index(out, "Deploy the Confluent Gateway with"); i < 0 || j < 0 || i > j {
		t.Errorf("the source ACL step must come before the client-switch step:\n%s", out)
	}

	// A SCRAM plan lists the clients' SCRAM credentials in the Gateway secrets step.
	cp.Plan.Auth.Value = "SASL/SCRAM"
	cp.effectiveSourceAuths = []string{SourceAuthSCRAM}
	out = renderMigration(cp)
	if !strings.Contains(out, "and a SCRAM admin credential it uses to register client SCRAM users") || strings.Contains(out, "holds none") {
		t.Errorf("SCRAM Gateway plan should list the SCRAM credentials as Gateway secrets:\n%s", out)
	}
}

// A no-authentication Gateway plan keeps its clients credential-less and maps them to one key.
func TestClusterLinkCutover_GatewayNoAuthMapsToOneKey(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	cp.Plan.Auth.Value = "API keys (SASL/PLAIN)"
	cp.effectiveSourceAuths = []string{SourceAuthUnauth}
	out := renderMigration(cp)
	want := "it keeps connecting without credentials, and the Gateway signs it in to Confluent Cloud with the one mapped API key (`auth: swap`)"
	if !strings.Contains(out, want) || strings.Contains(out, "it keeps its source credentials") {
		t.Errorf("no-auth Gateway cutover line missing %q:\n%s", want, out)
	}
}

// A SCRAM Gateway plan names the registration route, its port, and the mechanism, and
// warns that the admin user needs its own swap entry.
func TestClusterLinkCutover_GatewaySCRAMRegistration(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	cp.Plan.Auth.Value = "API keys (SASL/PLAIN)"
	cp.effectiveSourceAuths = []string{SourceAuthSCRAM}
	for mech, want := range map[string]string{
		"SCRAM-SHA-256": "SCRAM-SHA-256=[password=<password>]",
		"":              "<SCRAM-SHA-256|SCRAM-SHA-512, whichever your source uses>=[password=<password>]",
	} {
		cp.scanScramMechanism = mech
		out := renderMigration(cp)
		for _, w := range []string{want, "`scram-registration-route`, port 9599", "--bootstrap-server <gateway-host>:9599 --command-config <admin.properties> --alter --add-config '" + want + "' --entity-type users --entity-name <client-user>", "SCRAM passwords can't contain `[` or `]`", "needs its own swap entry in the secret store, mapping it to a Confluent Cloud API key, or provisioning fails"} {
			if !strings.Contains(out, w) {
				t.Errorf("mechanism %q: SCRAM Gateway step missing %q", mech, w)
			}
		}
		if mech == "SCRAM-SHA-256" && strings.Contains(out, "SCRAM-SHA-512=") {
			t.Errorf("SCRAM-SHA-256 source must not show SHA-512:\n%s", out)
		}
	}
}

// Self-managed Connect on a Cluster Linking plan: sinks start after the promote and
// their consumer groups are listed in the offset sync filters.
func TestConnectorsStep_SelfManagedSinksFollowPromote(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover = engine.SwitchoverResult{Value: "Cluster Linking, all at once (Restart-All-At-Once)", CutoverStyle: "Cluster Linking, all at once (Restart-All-At-Once)"}
	cp.Plan.Connectors = connectorsPlan("Run them yourself on your own Connect cluster").Connectors
	out := renderMigration(cp)
	want := "Stop each old sink connector with its service, before you promote its topics, so its last offsets sync. Create sink connectors only after you promote their topics, and list each sink's consumer group (connect-<connector-name>) in consumer.offset.group.filters so it resumes from its synced offsets."
	if !strings.Contains(out, want) {
		t.Errorf("missing sink instruction:\n%s", out)
	}
	i, j := strings.Index(out, "Stand up your own Kafka Connect cluster"), strings.Index(out, "create your self-managed sink connectors")
	if i < 0 || j < 0 || j < i || j < strings.Index(out, "run the cutover.") {
		t.Errorf("sink creation must follow the cutover step:\n%s", out)
	}
}

// A SASL/PLAIN Gateway plan has the Gateway check each client login, so the client
// credentials go in the route's JAAS file.
func TestClusterLinkCutover_GatewaySASLPlainJAAS(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	cp.effectiveSourceAuths = []string{SourceAuthSASLPlain}
	out := renderMigration(cp)
	if !strings.Contains(out, "The Gateway checks each client's SASL/PLAIN login itself, so add every client's username and password to the route's JAAS file before you route clients through it.") || strings.Contains(out, "holds none") {
		t.Errorf("SASL/PLAIN Gateway plan missing JAAS wording:\n%s", out)
	}
}

// SCRAM Gateway plans list only the stores that support SCRAM, need write access to
// the store, and register clients after the Gateway is deployed.
func TestClusterLinkCutover_GatewaySCRAMStoreAndOrder(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	cp.effectiveSourceAuths = []string{SourceAuthSCRAM}
	out := renderMigration(cp)
	for _, w := range []string{"Store the SCRAM credentials in HashiCorp Vault, AWS Secrets Manager, or CyberArk Conjur (Azure Key Vault doesn't support SCRAM), and give the Gateway write access to that store."} {
		if !strings.Contains(out, w) {
			t.Errorf("SCRAM Gateway plan missing %q", w)
		}
	}
	if strings.Contains(out, "HashiCorp Vault, AWS Secrets Manager, or Azure Key Vault") {
		t.Errorf("SCRAM plan must not offer Azure Key Vault as a store:\n%s", out)
	}
	d, r, g, e := strings.Index(out, "Deploy the Confluent Gateway with Confluent for Kubernetes.\n"), strings.Index(out, "port 9599"), strings.Index(out, "Route your clients through the Gateway: "), strings.Index(out, "`kcp migration execute")
	if d < 0 || r < d || g < r || e < g {
		t.Errorf("want deploy < register < route < execute, got %d %d %d %d", d, r, g, e)
	}
}
