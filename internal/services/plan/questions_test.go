package plan

import (
	"os"
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
	if find(qs, "connects_today") != nil || find(qs, "cc_egress_required") != nil {
		t.Errorf("private-only follow-ups should not apply on a public plan")
	}
}

func TestQuestions_ServerlessDropsDowntime(t *testing.T) {
	p := engine.Profile{MSKClusterType: engine.MSKServerless, SourcePlatform: "Amazon MSK"}
	if find(resolveQuestions(p, IntakeInputs{}), "downtime_tolerance") != nil {
		t.Errorf("downtime_tolerance should not apply on Serverless")
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
