package plan

import (
	"strings"
	"testing"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// C2 render-coverage regression tests. These pin RENDER paths that the
// questionnaire battery can't reach (migration-infra type 1 needs a scanned public
// endpoint) or that only ever render "Schemaless" there, so a renderer change to
// any of them would otherwise slip through untested.

// publicClusterState is oneClusterState with public broker endpoints enabled, so
// sourcePublicAccess(c) is true and the migration-infra decision routes to type 1
// (public source → direct SASL/SCRAM cluster link). This is the only way to reach
// type 1: SourcePublicAccess is scan-derived and the questionnaire cannot set it.
func publicClusterState(parts int) report.ProcessedState {
	st := oneClusterState(parts)
	prov := st.Sources[0].MSKData.Regions[0].Clusters[0].AWSClientInformation.MskClusterConfig.Provisioned
	prov.BrokerNodeGroupInfo = &kafkatypes.BrokerNodeGroupInfo{
		ConnectivityInfo: &kafkatypes.ConnectivityInfo{
			PublicAccess: &kafkatypes.PublicAccess{Type: sp("SERVICE_PROVIDED_EIPS")},
		},
	}
	return st
}

// TestRenderEnginePlanMarkdown_MigrationInfraType1Public renders a full plan for a
// scanned public-endpoint SCRAM source and asserts the type-1 migration-infra label,
// the `--type 1` command, and the direct-link steps — with no jump cluster and not
// the private external-outbound topology. This is the C2 gap: MigrationInfraDecision
// returns type 1 (unit-tested in engine), but the rendered type-1 plan had no test.
func TestRenderEnginePlanMarkdown_MigrationInfraType1Public(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	// Answer the facts the scan can't supply from this minimal state (cc egress,
	// Kafka version) so the infra and mechanism settle and the migration steps render;
	// the public-endpoint fact stays scan-derived, which is what routes to type 1.
	ep := BuildEnginePlan(publicClusterState(5000), declFor("prod", IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC",
		UseCaseBreadth: "One team, one application", NeedsDataMigration: "Yes",
		DowntimeTolerance: "Minutes per service", AnyAppNeedsDataMigration: "No",
		CCEgressRequired: "No", OvKafkaVersion: "3.0 or newer", SchemaStrategy: "Stay schemaless",
	}), "kcp-state.json", fixed)
	out := RenderEnginePlanMarkdown(ep)
	for _, want := range []string{
		"Public source endpoints with SASL/SCRAM cluster link", // the type-1 label
		"`--type 1`", // the migration-infra command flag
		"build the migration link.",
		"public broker endpoints", // the type-1 rationale
		"run the cutover.",        // the standard Cluster Linking cutover
	} {
		if !strings.Contains(out, want) {
			t.Errorf("type-1 public plan missing %q:\n%s", want, out)
		}
	}
	// Type 1 is a direct public link: no jump cluster (types 4/5) and not the private
	// external-outbound label (type 2), so none of those must render here.
	for _, unwanted := range []string{
		"jump cluster",
		"external outbound cluster link",
		"--jump-cluster",
		"`--type 2`",
	} {
		if strings.Contains(out, unwanted) {
			t.Errorf("type-1 direct public link must not render %q:\n%s", unwanted, out)
		}
	}
}

// TestMigrationInfraFor_PublicSCRAMType1 pins the plan-layer wrapper (not just the
// engine decision, which migration_infra_test.go covers for private SCRAM only): a
// public SCRAM source on Enterprise maps to a type-1 cluster_link with the public
// label, and its command carries `--type 1` and no `--target-environment-id` (type 1
// alone omits it — it's a public link with no target environment to name).
func TestMigrationInfraFor_PublicSCRAMType1(t *testing.T) {
	pub := engine.Profile{SourceAuthTypes: []string{engineAuthSCRAM}, SourcePublicAccess: "Yes"}
	mi := migrationInfraFor(pub, enterprisePlan())
	if mi.Type != 1 {
		t.Fatalf("public scram enterprise: type=%d, want 1", mi.Type)
	}
	if mi.Kind != "cluster_link" {
		t.Errorf("kind=%q, want cluster_link", mi.Kind)
	}
	if mi.Label != "Public source endpoints with SASL/SCRAM cluster link" {
		t.Errorf("label=%q, want the public-endpoints label", mi.Label)
	}
	cp := ClusterPlan{ClusterID: "prod", Arn: "arn:aws:kafka:us-east-1:1:cluster/prod/abc",
		MigrationInfra: mi, Plan: enterprisePlan()}
	cmd := migrationInfraCommand(cp, "kcp-state.json")
	if !strings.Contains(cmd, "--type 1") {
		t.Errorf("command missing --type 1:\n%s", cmd)
	}
	if strings.Contains(cmd, "--target-environment-id") {
		t.Errorf("type-1 command must not carry --target-environment-id (public link):\n%s", cmd)
	}
}

// TestRenderEnginePlanMarkdown_SchemaVariety renders full scanless plans for the
// three schema paths the battery otherwise leaves uncovered (it mostly renders
// Schemaless): Schema Linking (CP Enterprise 7.1+ + migrate + reachable), Glue bulk
// re-registration (Glue + migrate), and Community/below-7.1 + migrate → Replicator.
// It asserts each renders its distinct recommendation and the right migrate-schemas
// step (or, for Replicator's tech-assist path, no self-serve command).
func TestRenderEnginePlanMarkdown_SchemaVariety(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	base := IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC", UseCaseBreadth: "One team, one application",
		NeedsDataMigration: "Yes", DowntimeTolerance: "A scheduled window, all at once",
		OvPartitionBand: "2,500–30,000", OvKafkaVersion: "3.0 or newer", OvSourceAuth: []string{engineAuthSCRAM},
		OvClusterType: engine.MSKProvisioned, SchemaStrategy: "Migrate my existing schemas",
	}
	cases := []struct {
		name      string
		srType    string
		reachable string
		wantRec   string   // the schema recommendation value that must render
		wantStep  []string // migrate-schemas step substrings that must render
		noStep    []string // substrings that must NOT render
	}{
		{
			name:      "schema linking",
			srType:    "Confluent Schema Registry: Confluent Platform Enterprise 7.1 or later",
			reachable: "Yes",
			wantRec:   "Schema Linking",
			wantStep:  []string{"kcp create-asset migrate-schemas", "--url"},
			noStep:    []string{"--glue-registry"},
		},
		{
			name:     "glue bulk",
			srType:   "AWS Glue Schema Registry",
			wantRec:  "Glue bulk re-registration",
			wantStep: []string{"kcp create-asset migrate-schemas", "--glue-registry"},
			noStep:   []string{"--url "},
		},
		{
			name:    "community replicator",
			srType:  "Confluent Schema Registry: Community, or Confluent Platform below 7.1",
			wantRec: "Replicator",
			noStep:  []string{"kcp create-asset migrate-schemas"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			in := base
			in.SourceSRType = tc.srType
			in.SourceSROutboundReachableToCC = tc.reachable
			ep := BuildEnginePlan(ScanlessState(), declFor(ScanlessClusterName, in), "", fixed)
			out := RenderEnginePlanMarkdown(ep)
			if !strings.Contains(out, tc.wantRec) {
				t.Errorf("%s: rendered plan missing recommendation %q:\n%s", tc.name, tc.wantRec, out)
			}
			for _, want := range tc.wantStep {
				if !strings.Contains(out, want) {
					t.Errorf("%s: rendered plan missing schema step %q:\n%s", tc.name, want, out)
				}
			}
			for _, no := range tc.noStep {
				if strings.Contains(out, no) {
					t.Errorf("%s: rendered plan must not contain %q:\n%s", tc.name, no, out)
				}
			}
		})
	}
}

// selfManagedConnectorCluster is a scanned Provisioned+SCRAM cluster that runs a
// self-managed Kafka Connect cluster (and no MSK Connect), so buildProfile derives
// SelfManagedConnectors=Yes / MSKConnectPresent=No from the scan.
func selfManagedConnectorCluster(parts int) report.ProcessedCluster {
	return report.ProcessedCluster{
		Name: "prod", Region: "us-east-1",
		AWSClientInformation: types.AWSClientInformation{
			MskClusterConfig: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{Scram: &kafkatypes.Scram{Enabled: bp(true)}},
					},
					StorageMode: kafkatypes.StorageModeLocal,
				},
			},
			Connectors: []types.ConnectorSummary{}, // scanned, no MSK Connect
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			Topics:          &types.Topics{Summary: types.TopicSummary{TotalPartitions: parts}},
			ConnectClusters: []types.ConnectCluster{{}}, // scanned, one self-managed Connect cluster present
		},
	}
}

// TestSelfManagedConnectorsRender_EndToEnd ties the scan→profile→plan→render seam for
// self-managed connectors: a scanned self-managed Connect runtime becomes a
// ConnectorSource{SelfManaged:true}, and the rendered migrate-connectors step is the
// `self-managed` subcommand with --source-type (not the `msk` one). Battery scenario
// 17 exercises this via the questionnaire, but no Go test pinned the rendered step.
func TestSelfManagedConnectorsRender_EndToEnd(t *testing.T) {
	c := selfManagedConnectorCluster(5000)
	profile := buildProfile(c, IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC", UseCaseBreadth: "One team, one application",
		NeedsDataMigration: "Yes", DowntimeTolerance: "Minutes per service", AnyAppNeedsDataMigration: "No",
	}, "", false)
	if profile.SelfManagedConnectors == nil || *profile.SelfManagedConnectors != "Yes" {
		t.Fatalf("SelfManagedConnectors=%v, want Yes (scanned Connect cluster)", profile.SelfManagedConnectors)
	}
	cs := connectorSourceFromProfile(profile)
	if cs == nil || !cs.SelfManaged || cs.MSKConnect {
		t.Fatalf("connectorSource=%+v, want SelfManaged only", cs)
	}
	cp := ClusterPlan{
		Arn:             "arn:aws:kafka:us-east-1:1:cluster/prod/abc",
		Plan:            engine.ComputePlan(profile),
		ConnectorSource: cs,
	}
	out := renderSteps(cp)
	if !strings.Contains(out, "kcp create-asset migrate-connectors self-managed") {
		t.Errorf("expected the self-managed migrate-connectors subcommand:\n%s", out)
	}
	if !strings.Contains(out, "--source-type msk") {
		t.Errorf("self-managed command needs --source-type:\n%s", out)
	}
	if strings.Contains(out, "migrate-connectors msk \\") {
		t.Errorf("self-managed-only cluster must not emit the msk subcommand:\n%s", out)
	}
}
