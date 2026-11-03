package plan

import (
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

func find(qs []ResolvedQuestion, key string) *ResolvedQuestion {
	for i := range qs {
		if qs[i].Key == key {
			return &qs[i]
		}
	}
	return nil
}

func fp(v float64) *float64 { return &v }

// Tokens/booleans resolve to the engine's internal strings.
func TestInputs_TokenResolution(t *testing.T) {
	raw := map[string]any{
		"private_networking_required": true,      // -> public_endpoints_ok "No"
		"move_existing_data":          true,      // -> needs_data_migration "Yes"
		"downtime_tolerance":          "minutes", // -> "Minutes per service"
		"schema_registry":             "cp-enterprise-7.1",
		"schema_strategy":             "migrate",
		"target_auth":                 []any{"mtls", "api-keys"},
		"connector_destination":       "confluent-managed",
	}
	in, warnings := resolveDeclared(raw)
	if len(warnings) != 0 {
		t.Fatalf("unexpected warnings: %v", warnings)
	}
	if in.PublicEndpointsOK != "No" {
		t.Errorf("private_networking_required=true -> PublicEndpointsOK=%q, want No", in.PublicEndpointsOK)
	}
	if in.NeedsDataMigration != "Yes" {
		t.Errorf("move_existing_data=true -> NeedsDataMigration=%q, want Yes", in.NeedsDataMigration)
	}
	if in.DowntimeTolerance != "Minutes per service" {
		t.Errorf("downtime_tolerance token -> %q", in.DowntimeTolerance)
	}
	if in.SourceSRType != "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later" {
		t.Errorf("schema_registry token -> %q", in.SourceSRType)
	}
	if len(in.TargetIdentityModel) != 2 || in.TargetIdentityModel[0] != "mTLS" || in.TargetIdentityModel[1] != "API keys (SASL/PLAIN)" {
		t.Errorf("target_auth multi -> %v", in.TargetIdentityModel)
	}
	if in.ConnectorDestination != "Move to Confluent-managed" {
		t.Errorf("connector_destination token -> %q", in.ConnectorDestination)
	}
}

// An unknown token is reported (and skipped), not silently mis-parsed.
func TestInputs_UnknownTokenWarns(t *testing.T) {
	in, warnings := resolveDeclared(map[string]any{"downtime_tolerance": "instant"})
	if in.DowntimeTolerance != "" {
		t.Errorf("unknown token should not set a value, got %q", in.DowntimeTolerance)
	}
	if len(warnings) != 1 {
		t.Fatalf("want 1 warning, got %v", warnings)
	}
}

// Answers round-trip back to tokens for display, and status is classified.
func TestQuestions_StatusAndConditionals(t *testing.T) {
	p := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK", PartitionsExact: fp(1000)}
	qs := resolveQuestions(p, IntakeInputs{})

	if q := find(qs, "private_networking_required"); q == nil || q.Status != "open_required" {
		t.Errorf("private_networking_required = %+v, want open_required", q)
	}
	if q := find(qs, "exceeds_standard_limits"); q == nil || q.Status != "open_optional" || q.Value != "false" {
		t.Errorf("exceeds_standard_limits = %+v, want open_optional/false", q)
	}
	if find(qs, "exceeds_enterprise_limits") != nil {
		t.Errorf("exceeds_enterprise_limits should not apply on Band 1")
	}
	if find(qs, "schema_reachable_to_cc") != nil {
		t.Errorf("schema_reachable_to_cc should not apply yet")
	}
	if find(qs, "connects_today") == nil {
		t.Errorf("connects_today should apply on the default private path")
	}

	// An answered question round-trips to its token.
	in := IntakeInputs{DowntimeTolerance: "Minutes per service"}
	if q := find(resolveQuestions(p, in), "downtime_tolerance"); q == nil || q.Status != "answered" || q.Value != "minutes" {
		t.Errorf("answered downtime -> %+v, want answered/minutes", q)
	}
}

func TestQuestions_ConditionalReveal(t *testing.T) {
	p := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK", PartitionsExact: fp(1000),
		SourceSRType: "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later", SchemaStrategy: "Migrate my existing schemas"}
	qs := resolveQuestions(p, IntakeInputs{SourceSRType: p.SourceSRType, SchemaStrategy: p.SchemaStrategy})
	if q := find(qs, "schema_reachable_to_cc"); q == nil || q.Status != "open_required" {
		t.Errorf("reachability follow-up should be revealed as open_required, got %+v", q)
	}
}

func TestQuestions_PublicRemovesPrivateFollowups(t *testing.T) {
	p := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK", PartitionsExact: fp(1000), PublicEndpointsOK: "Yes"}
	qs := resolveQuestions(p, IntakeInputs{PublicEndpointsOK: "Yes"})
	if find(qs, "connects_today") != nil {
		t.Errorf("private-only follow-ups should not apply on a public plan")
	}
	// The egress need is asked on the public path too: it changes the plan (Enterprise
	// with an Egress PrivateLink Endpoint, or Dedicated on GCP).
	if find(qs, "cc_egress_required") == nil {
		t.Errorf("cc_egress_required should apply on a public plan")
	}
}

// A Serverless source reads downtime tolerance only when its data moves over the
// jump-cluster link (a private plan); a public plan lands on Standard and Replicator.
func TestQuestions_ServerlessDowntimeFollowsPrivatePath(t *testing.T) {
	pub := engine.Profile{MSKClusterType: engine.MSKServerless, SourcePlatform: "Amazon MSK", PublicEndpointsOK: "Yes"}
	if find(resolveQuestions(pub, IntakeInputs{PublicEndpointsOK: "Yes"}), "downtime_tolerance") != nil {
		t.Errorf("downtime_tolerance should not apply on a public Serverless plan")
	}
	priv := engine.Profile{MSKClusterType: engine.MSKServerless, SourcePlatform: "Amazon MSK", PublicEndpointsOK: "No"}
	if find(resolveQuestions(priv, IntakeInputs{PublicEndpointsOK: "No"}), "downtime_tolerance") == nil {
		t.Errorf("downtime_tolerance should apply on a private Serverless plan")
	}
}

func TestInputs_PerClusterAndScanOverride(t *testing.T) {
	yaml := `
clusters:
  my-cluster:
    private_networking_required: true
    downtime_tolerance: minutes
    source_auth: [scram]          # override the scanned auth
    kafka_version: older          # override the scanned version
`
	tmp := t.TempDir() + "/pi.yaml"
	if err := os.WriteFile(tmp, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	d, warnings, err := LoadDeclaredInputs(tmp)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("load: err=%v warnings=%v", err, warnings)
	}
	in := d.For("my-cluster")
	if in.PublicEndpointsOK != "No" || in.DowntimeTolerance != "Minutes per service" {
		t.Errorf("declared: %+v", in)
	}
	if len(in.OvSourceAuth) != 1 || in.OvSourceAuth[0] != "SASL/SCRAM" {
		t.Errorf("source_auth override = %v", in.OvSourceAuth)
	}
	if in.OvKafkaVersion != "Older than 2.4" {
		t.Errorf("kafka_version override = %q", in.OvKafkaVersion)
	}
}

func TestInputs_DefaultsWithClusterOverride(t *testing.T) {
	yaml := `
defaults:
  private_networking_required: true
  downtime_tolerance: minutes
clusters:
  cluster-a: {}                       # inherits defaults
  cluster-b:
    downtime_tolerance: zero          # overrides just this field
`
	tmp := t.TempDir() + "/pi.yaml"
	if err := os.WriteFile(tmp, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	d, warnings, err := LoadDeclaredInputs(tmp)
	if err != nil || len(warnings) != 0 {
		t.Fatalf("load: err=%v warnings=%v", err, warnings)
	}
	// cluster-a inherits both defaults.
	a := d.For("cluster-a")
	if a.PublicEndpointsOK != "No" || a.DowntimeTolerance != "Minutes per service" {
		t.Errorf("cluster-a should inherit defaults: %+v", a)
	}
	// cluster-b keeps the inherited networking default but overrides downtime.
	b := d.For("cluster-b")
	if b.PublicEndpointsOK != "No" {
		t.Errorf("cluster-b should inherit networking default: %+v", b)
	}
	if b.DowntimeTolerance != "Zero downtime" {
		t.Errorf("cluster-b should override downtime: %q", b.DowntimeTolerance)
	}
	// An undeclared cluster still gets the fleet defaults.
	if got := d.For("cluster-unknown"); got.PublicEndpointsOK != "No" {
		t.Errorf("undeclared cluster should get defaults: %+v", got)
	}
}

// Kerberos (GSSAPI) is offered as a source-auth option only for a self-managed
// Apache Kafka / Confluent Platform source — MSK has no Kerberos support.
func TestQuestions_KerberosOSKOnly(t *testing.T) {
	msk := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK", PartitionsExact: fp(1000)}
	if q := find(resolveQuestions(msk, IntakeInputs{}), "source_auth"); q == nil || containsStr(q.Tokens, "kerberos") {
		t.Errorf("MSK source_auth tokens = %+v, must not offer kerberos", q)
	}

	osk := engine.Profile{SourceType: engine.SourceApacheKafka, SourcePlatform: "Apache Kafka", PartitionsExact: fp(1000)}
	if q := find(resolveQuestions(osk, IntakeInputs{}), "source_auth"); q == nil || !containsStr(q.Tokens, "kerberos") {
		t.Errorf("OSK source_auth tokens = %+v, want kerberos offered", q)
	}
}

// An Apache Kafka / Confluent Platform scan can't be Amazon MSK, so "msk" is not
// offered as a source_platform option there; a scanless run still offers it.
func TestSourcePlatform_MSKOptionHiddenOnOSKScan(t *testing.T) {
	var q question
	for _, c := range catalog {
		if c.Key == "source_platform" {
			q = c
		}
	}
	osk := engine.Profile{SourceType: engine.SourceApacheKafka}
	for _, tok := range q.visibleTokens(osk) {
		if tok == "msk" {
			t.Errorf("OSK scan must not offer msk, got %v", q.visibleTokens(osk))
		}
	}
	scanless := engine.Profile{Scanless: true}
	if got := q.visibleTokens(scanless); len(got) != 3 {
		t.Errorf("scanless should offer all platforms, got %v", got)
	}
}

// In a fleet whose clusters' sources differ, source_platform is not asked at the
// all_clusters level (a fleet-wide answer would fail validation for the other type).
func TestAllQuestionsUnion_SourcePlatformOnlyWhenEveryClusterAsksIt(t *testing.T) {
	sp := ResolvedQuestion{Key: "source_platform", Required: true, Status: "open_required"}
	other := ResolvedQuestion{Key: "use_case_breadth"}
	mixed := &EnginePlan{Clusters: []ClusterPlan{
		{Key: "msk", Questions: []ResolvedQuestion{other}},
		{Key: "osk", Questions: []ResolvedQuestion{sp, other}},
	}}
	for _, q := range allQuestionsUnion(mixed) {
		if q.Key == "source_platform" {
			t.Errorf("mixed fleet must not list source_platform under all_clusters")
		}
	}
	uniform := &EnginePlan{Clusters: []ClusterPlan{
		{Key: "a", Questions: []ResolvedQuestion{sp, other}},
		{Key: "b", Questions: []ResolvedQuestion{sp, other}},
	}}
	found := false
	for _, q := range allQuestionsUnion(uniform) {
		found = found || q.Key == "source_platform"
	}
	if !found {
		t.Errorf("uniform fleet should keep source_platform under all_clusters")
	}
}

// The target_cloud default follows the resolved source cloud, like the engine's own
// fallback, so the default shown (and written back) never disagrees with the plan.
func TestTargetCloudDefaultFollowsSourceCloud(t *testing.T) {
	cases := []struct {
		name string
		p    engine.Profile
		want string
	}{
		{"msk", engine.Profile{SourcePlatform: "Amazon MSK", SourceCloud: "AWS"}, "aws"},
		{"ak-azure", engine.Profile{SourceType: engine.SourceApacheKafka, SourcePlatform: "Apache Kafka", SourceCloud: "Azure"}, "azure"},
		{"cp-gcp", engine.Profile{SourceType: engine.SourceConfluentPlatform, SourcePlatform: "Confluent Platform", SourceCloud: "GCP"}, "gcp"},
		{"on-prem", engine.Profile{SourceType: engine.SourceApacheKafka, SourcePlatform: "Apache Kafka", SourceCloud: "On-prem or other"}, "aws"},
		{"unknown", engine.Profile{SourceType: engine.SourceApacheKafka, SourcePlatform: "Apache Kafka"}, "aws"},
	}
	for _, c := range cases {
		q := findResolved(resolveQuestions(c.p, IntakeInputs{}), "target_cloud")
		if q == nil || q.Value != c.want {
			t.Errorf("%s: target_cloud default = %+v, want %q", c.name, q, c.want)
		}
	}
	// A declared answer wins over the default.
	q := findResolved(resolveQuestions(engine.Profile{SourcePlatform: "Amazon MSK", SourceCloud: "AWS"}, IntakeInputs{TargetCloud: "Azure"}), "target_cloud")
	if q == nil || q.Value != "azure" || q.defaultVal != "aws" {
		t.Errorf("declared target_cloud = %+v, want value azure with default aws", q)
	}
}

// A single-choice question takes one value: a list used to be coerced to its first
// element, silently dropping the rest, so it is a hard error. A multi question still
// takes a list, and a one-element list is fine.
func TestParseDeclaredInputs_SingleValueExpected(t *testing.T) {
	_, warns, err := ParseDeclaredInputs([]byte("clusters:\n  c:\n    target_cloud: [aws, gcp]\n"))
	if err != nil {
		t.Fatal(err)
	}
	if len(warns) != 1 || !strings.Contains(warns[0], "target_cloud: expects a single value, got a list [aws, gcp]") {
		t.Errorf("target_cloud list: got %v", warns)
	}
	_, warns, _ = ParseDeclaredInputs([]byte("clusters:\n  c:\n    target_cloud: [azure]\n    target_auth: [api-keys, oauth]\n"))
	if len(warns) != 0 {
		t.Errorf("one-element list and a multi question should be accepted, got %v", warns)
	}
}

// connects_today refines an explicit private-networking requirement on an AWS
// target. It is skipped for an on-prem source and for a public-OK plan that only
// crosses to private by size.
func TestQuestions_ConnectsTodayOnlyForExplicitPrivateOnCloudSource(t *testing.T) {
	asked := func(p engine.Profile, in IntakeInputs) bool {
		return find(resolveQuestions(p, in), "connects_today") != nil
	}
	priv := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK", PublicEndpointsOK: "No"}
	if !asked(priv, IntakeInputs{PublicEndpointsOK: "No"}) {
		t.Errorf("connects_today should be asked when private networking is required")
	}
	onprem := engine.Profile{SourceType: engine.SourceApacheKafka, SourcePlatform: "Apache Kafka", SourceCloud: "On-prem or other", PublicEndpointsOK: "No"}
	if asked(onprem, IntakeInputs{PublicEndpointsOK: "No", SourceCloud: "On-prem or other"}) {
		t.Errorf("connects_today should not be asked for an on-prem source")
	}
	crossed := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK", PublicEndpointsOK: "Yes", ExceedsStandardLimits: "Yes"}
	if asked(crossed, IntakeInputs{PublicEndpointsOK: "Yes", ExceedsStandardLimits: "Yes"}) {
		t.Errorf("connects_today should not be asked for a public-OK plan that crosses to private by size")
	}
}

// schema_strategy does not offer "migrate" when the source has no Schema Registry.
func TestSchemaStrategy_MigrateHiddenWithoutRegistry(t *testing.T) {
	q, ok := questionByKey("schema_strategy")
	if !ok {
		t.Fatal("schema_strategy question missing")
	}
	has := func(p engine.Profile) bool {
		for _, o := range q.visibleOpts(p) {
			if o.Token == "migrate" {
				return true
			}
		}
		return false
	}
	if has(engine.Profile{SourceSRType: engineSRNone}) {
		t.Error("migrate should be hidden when schema_registry is none")
	}
	if !has(engine.Profile{SourceSRType: engineSRGlue}) {
		t.Error("migrate should be offered when a registry exists")
	}
}

// A start-fresh plan copies no history, so tiered_storage is not asked.
func TestTieredStorage_HiddenOnStartFresh(t *testing.T) {
	var q question
	for _, c := range allQuestions() {
		if c.Key == "tiered_storage" {
			q = c
		}
	}
	p := engine.Profile{SourcePlatform: "Amazon MSK", MSKClusterType: engine.MSKProvisioned}
	if rq := resolveOne(q, p, IntakeInputs{}); rq.Status != "open_required" {
		t.Errorf("migrating data: status = %q, want open_required", rq.Status)
	}
	p.NeedsDataMigration = "No"
	if q.Applies(p) {
		t.Error("start fresh: tiered_storage should not apply")
	}
}
