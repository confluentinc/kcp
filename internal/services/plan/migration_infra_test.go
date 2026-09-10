package plan

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

// enterprisePlan is a settled Enterprise plan result (the common target tier), for
// exercising the migration-infra wrapper. The topology matrix itself is tested in
// the engine (engine.MigrationInfraDecision).
func enterprisePlan() engine.PlanResult {
	return engine.PlanResult{ClusterType: engine.ClusterTypeResult{Value: engine.TierEnterprise, Tier: engine.TierEnterprise}}
}

func TestMigrationInfraFor(t *testing.T) {
	scram := engine.Profile{SourceAuthTypes: []string{engineAuthSCRAM}}

	// Delegates to the engine decision: private SCRAM + Enterprise -> type 2.
	if got := migrationInfraFor(scram, enterprisePlan()).Type; got != 2 {
		t.Errorf("private scram enterprise: type=%d, want 2", got)
	}

	// A withheld plan never auto-maps a type.
	if got := migrationInfraFor(scram, engine.PlanResult{Withheld: true}).Type; got != 0 {
		t.Errorf("withheld plan: type=%d, want 0", got)
	}

	// A Replicator migration (Standard/Basic target: Cluster Linking unavailable)
	// builds no cluster link, so there is no migration-infra type.
	repl := enterprisePlan()
	repl.Switchover = engine.SwitchoverResult{Value: "Confluent Replicator", Replicator: true}
	if got := migrationInfraFor(scram, repl); got.Type != 0 {
		t.Errorf("replicator migration: type=%d, want 0 (no cluster link)", got.Type)
	}
	// Start-fresh needs no migration infrastructure either.
	fresh := enterprisePlan()
	fresh.Switchover = engine.SwitchoverResult{Value: "Start fresh", StartFresh: true}
	if got := migrationInfraFor(scram, fresh); got.Type != 0 {
		t.Errorf("start-fresh: type=%d, want 0", got.Type)
	}

	// Gov cloud flips cc-type (and the engine routes it to a specialist).
	gov := migrationInfraFor(engine.Profile{SourceAuthTypes: []string{engineAuthSCRAM}, TargetIsGovCloud: "Yes"}, enterprisePlan())
	if gov.CCType != "government" {
		t.Errorf("gov cc-type = %q, want government", gov.CCType)
	}

	// The command carries the type, source, and cluster id.
	cp := ClusterPlan{ClusterID: "orders", Arn: "arn:aws:kafka:...:cluster/orders/abc",
		MigrationInfra: migrationInfraFor(scram, enterprisePlan()), Plan: enterprisePlan()}
	cmd := migrationInfraCommand(cp, "kcp-state.json")
	for _, want := range []string{"--type 2", "--source-type msk", "arn:aws:kafka", "--cc-type commercial", "--target-environment-id"} {
		if !strings.Contains(cmd, want) {
			t.Errorf("command missing %q:\n%s", want, cmd)
		}
	}

	// Scanless run (no state file): the source is only inferred, so --source-type is a
	// placeholder rather than an asserted "msk".
	scanless := migrationInfraCommand(cp, "")
	if !strings.Contains(scanless, "--source-type <msk|apache-kafka>") {
		t.Errorf("scanless command should carry a --source-type placeholder:\n%s", scanless)
	}
	if strings.Contains(scanless, "--source-type msk") {
		t.Errorf("scanless command must not assert --source-type msk:\n%s", scanless)
	}
}

// migrate-topics: mirror mode must carry --cluster-link-name and --mode mirror;
// new mode must carry --mode new and must NOT carry --cluster-link-name (the CLI
// rejects it). Both need the required cluster/target flags.
func TestMigrateTopicsCommand(t *testing.T) {
	cp := ClusterPlan{
		ClusterID:      "orders",
		Arn:            "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		MigrationInfra: MigrationInfra{CCType: "commercial"},
	}
	required := []string{"--source-type msk", "--state-file kcp-state.json", "arn:aws:kafka", "--cc-type commercial", "--target-cluster-id", "--target-rest-endpoint"}

	mirror := migrateTopicsCommand(cp, migrateTopicsModeMirror, "kcp-state.json")
	for _, want := range append([]string{"--mode mirror", "--cluster-link-name"}, required...) {
		if !strings.Contains(mirror, want) {
			t.Errorf("mirror command missing %q:\n%s", want, mirror)
		}
	}

	fresh := migrateTopicsCommand(cp, migrateTopicsModeNew, "kcp-state.json")
	if !strings.Contains(fresh, "--mode new") {
		t.Errorf("new command missing --mode new:\n%s", fresh)
	}
	if strings.Contains(fresh, "--cluster-link-name") {
		t.Errorf("new command must not carry --cluster-link-name (CLI rejects it):\n%s", fresh)
	}
	for _, want := range required {
		if !strings.Contains(fresh, want) {
			t.Errorf("new command missing %q:\n%s", want, fresh)
		}
	}
}

// A jump-cluster type (5, IAM on Serverless) must print every flag the CLI marks
// required, or the command the plan tells the user to run fails PreRunE.
func TestMigrationInfraCommand_JumpClusterFlags(t *testing.T) {
	cp := ClusterPlan{
		ClusterID:    "orders",
		Arn:          "arn:aws:kafka:us-east-1:1:cluster/orders/abc",
		IsServerless: true,
		Plan:         enterprisePlan(),
		MigrationInfra: MigrationInfra{
			Type: 5, CCType: "commercial", JumpCluster: true,
			Label: "Private source with jump cluster (IAM)",
		},
	}
	cmd := migrationInfraCommand(cp, "kcp-state.json")
	for _, want := range []string{
		"--type 5",
		"--target-environment-id",
		"--target-bootstrap-endpoint",
		"--existing-private-link-vpce-id",
		"--jump-cluster-broker-subnet-cidr",
		"--jump-cluster-setup-host-subnet-cidr",
		"--jump-cluster-iam-auth-role-name",
		"--jump-cluster-instance-type",  // Serverless-only
		"--jump-cluster-broker-storage", // Serverless-only
	} {
		if !strings.Contains(cmd, want) {
			t.Errorf("type-5 serverless command missing %q:\n%s", want, cmd)
		}
	}
}
