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
	// Pure IAM on a cluster-link path: IAM policies only, bare RBAC (the Step 3 note glosses it).
	if s := note([]string{SourceAuthIAM}, "cluster_link", false); !strings.Contains(s, "Map your AWS IAM policies to Confluent Cloud RBAC role bindings") || strings.Contains(s, "role-based access control (RBAC)") {
		t.Errorf("IAM cluster-link note = %q", s)
	}
	// IAM on a non-cluster-link path (Replicator): no Step 3, so RBAC is glossed inline.
	if s := note([]string{SourceAuthIAM}, "replicator", false); !strings.Contains(s, "role-based access control (RBAC)") {
		t.Errorf("IAM replicator should gloss RBAC: %q", s)
	}
	// Mixed IAM + SASL/SCRAM: names both authorization systems.
	if s := note([]string{SourceAuthIAM, SourceAuthSCRAM}, "cluster_link", false); !strings.Contains(s, "AWS IAM policies and the Kafka ACLs") {
		t.Errorf("IAM+SCRAM should mention both: %q", s)
	}
	// Unauthenticated only: no authorization to carry over.
	if s := note([]string{SourceAuthUnauth}, "cluster_link", false); !strings.Contains(s, "no authorization to carry over") {
		t.Errorf("unauth note = %q", s)
	}
	// SCRAM (or mTLS): Kafka ACLs.
	if s := note([]string{SourceAuthSCRAM}, "cluster_link", false); !strings.Contains(s, "Map your source ACLs") {
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
	if strings.Contains(out, "self-managed") {
		t.Errorf("msk-only cluster should not emit a self-managed command:\n%s", out)
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
	linkStep(md, cp, "kcp-state.json", step)
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
