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

func oneClusterState(parts int) report.ProcessedState {
	return report.ProcessedState{
		Timestamp: time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC),
		Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{{
						Name: "prod", Region: "us-east-1",
						AWSClientInformation: types.AWSClientInformation{
							MskClusterConfig: kafkatypes.Cluster{
								ClusterType: kafkatypes.ClusterTypeProvisioned,
								Provisioned: &kafkatypes.Provisioned{
									ClientAuthentication: &kafkatypes.ClientAuthentication{
										Sasl: &kafkatypes.Sasl{Scram: &kafkatypes.Scram{Enabled: bp(true)}},
									},
								},
							},
						},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: parts}},
						},
					}},
				}},
			},
		}},
	}
}

func TestRenderEnginePlanMarkdown_Recommendation(t *testing.T) {
	ep := BuildEnginePlan(oneClusterState(5000), declFor("prod", IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC",
		UseCaseBreadth: "One team, one application", NeedsDataMigration: "Yes",
		DowntimeTolerance: "Minutes per service", AnyAppNeedsDataMigration: "No",
	}), "kcp-state.json", nil)
	out := RenderEnginePlanMarkdown(ep)
	for _, want := range []string{"# Migration Plan", "Cluster: prod", "Recommendation", "Enterprise", "Talk to a person"} {
		if !strings.Contains(out, want) {
			t.Errorf("markdown missing %q", want)
		}
	}
	if strings.Contains(out, "Needs a specialist") {
		t.Errorf("clean plan should not render the withheld assessment")
	}
}

func TestRenderEnginePlanMarkdown_Withheld(t *testing.T) {
	// Band 3 forces Dedicated -> withheld.
	ep := BuildEnginePlan(oneClusterState(50000), declFor("prod", IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC",
		UseCaseBreadth: "One team, one application", NeedsDataMigration: "Yes",
		DowntimeTolerance: "Minutes per service",
	}), "kcp-state.json", nil)
	out := RenderEnginePlanMarkdown(ep)
	if !strings.Contains(out, "Needs a specialist") {
		t.Errorf("withheld plan should render the assessment banner")
	}
	if strings.Contains(out, "### Recommendation") {
		t.Errorf("withheld plan must not render a recommendation section")
	}
}

// A GCP private-source cluster link alone withholds the plan on its own trigger and
// must not name Dedicated anywhere in the withheld output.
func TestRenderEnginePlanMarkdown_GCPLinkOnlyWithheldNamesNoDedicated(t *testing.T) {
	ep := BuildEnginePlan(oneClusterState(50), declFor("prod", IntakeInputs{
		TargetCloud: "GCP", PublicEndpointsOK: "No", ConnectsToday: "Same VPC",
		UseCaseBreadth: "One team, one application", NeedsDataMigration: "Yes",
		DowntimeTolerance: "Minutes per service",
	}), "kcp-state.json", nil)
	out := RenderEnginePlanMarkdown(ep)
	if !strings.Contains(out, "Needs a specialist") || !strings.Contains(out, "isn't supported over Private Service Connect") {
		t.Fatalf("expected withheld GCP link plan, got:\n%s", out)
	}
	if strings.Contains(out, "moves to Dedicated") || strings.Contains(out, "recommend Dedicated") {
		t.Errorf("link-only withheld plan must not steer to Dedicated:\n%s", out)
	}
}

// TestRender_ScanlessAnswersHeadingSuffix checks the region-less placeholder whose
// key equals its name gets an "(answers)" Q&A heading with an anchor distinct from
// its top cluster heading, while a regioned cluster needs no suffix (A6).
func TestRender_ScanlessAnswersHeadingSuffix(t *testing.T) {
	scanless := ClusterPlan{ClusterID: ScanlessClusterName, Key: ScanlessClusterName, Region: ""}
	if got := answersHeadingTitle(scanless); got != "Cluster: "+ScanlessClusterName+" (answers)" {
		t.Errorf("scanless answers heading = %q, want the (answers) suffix", got)
	}
	if qaAnchor(scanless) == headingAnchor(clusterHeadingTitle(scanless)) {
		t.Errorf("scanless Q&A anchor must differ from the top cluster heading anchor")
	}
	regioned := ClusterPlan{ClusterID: "prod", Key: "prod", Region: "us-east-1"}
	if strings.Contains(answersHeadingTitle(regioned), "(answers)") {
		t.Errorf("regioned cluster should not get an (answers) suffix: %q", answersHeadingTitle(regioned))
	}
}

// TestRender_ProvenanceScanlessVsScanned checks A1: a fully-answered scanless plan
// never claims a scan and reads "From your answer" for sizing, while a scanned plan
// keeps "Sized from your scan" for its measured partition count.
func TestRender_ProvenanceScanlessVsScanned(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	scanless := RenderEnginePlanMarkdown(BuildEnginePlan(ScanlessState(), declFor(ScanlessClusterName, IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC", UseCaseBreadth: "One team, one application",
		NeedsDataMigration: "Yes", DowntimeTolerance: "A scheduled window, all at once",
		OvPartitionBand: "2,500–30,000", OvKafkaVersion: "3.0 or newer", OvSourceAuth: []string{engineAuthSCRAM},
		OvClusterType: engine.MSKProvisioned, SchemaStrategy: "Stay schemaless",
	}), "", fixed))
	if strings.Contains(scanless, "your scan") {
		t.Errorf("scanless plan must not claim a scan; found 'your scan':\n%s", scanless)
	}
	if !strings.Contains(scanless, "From your answer") {
		t.Errorf("scanless sizing should read 'From your answer':\n%s", scanless)
	}
	scanned := RenderEnginePlanMarkdown(BuildEnginePlan(oneClusterState(5000), declFor("prod", IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC", UseCaseBreadth: "One team, one application",
		NeedsDataMigration: "Yes", DowntimeTolerance: "Minutes per service", AnyAppNeedsDataMigration: "No",
	}), "kcp-state.json", fixed))
	if !strings.Contains(scanned, "Sized from your scan") {
		t.Errorf("scanned plan sizing should read 'Sized from your scan':\n%s", scanned)
	}
}

// The cluster header and plan.json's source_auths show the effective source auth, so a
// source_auth override replaces what the scan detected (here, nothing).
func TestRender_ShowsEffectiveSourceAuths(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(oneClusterState(5000), declFor("prod", IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC", UseCaseBreadth: "One team, one application",
		NeedsDataMigration: "Yes", DowntimeTolerance: "Minutes per service", AnyAppNeedsDataMigration: "No",
		OvSourceAuth: []string{engineAuthSCRAM},
	}), "kcp-state.json", fixed)
	if got := ep.Clusters[0].SourceAuths; len(got) != 1 || got[0] != SourceAuthSCRAM {
		t.Errorf("plan.json source_auths = %v, want [%s]", got, SourceAuthSCRAM)
	}
	if md := RenderEnginePlanMarkdown(ep); !strings.Contains(md, "auth: SASL/SCRAM") {
		t.Errorf("header does not show the overridden auth:\n%s", md)
	}
}

// An alternative folds into the reason as a clean sentence: no colon-then-capital
// join, and an imperative label reads as an infinitive.
func TestWithAlternative_Wording(t *testing.T) {
	got := withAlternative("Reason.", "PrivateLink", "You already use it.")
	if want := "Reason. An alternative is PrivateLink. You already use it."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
	got = withAlternative("Reason.", "Mirror your data across first", "If you need your messages.")
	if want := "Reason. An alternative is to mirror your data across first. If you need your messages."; got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

// A scanless plan whose source_platform answer is msk fills --source-type msk; one that
// never named its source keeps the placeholder.
func TestSourceTypeFlag_ScanlessDeclaredMSK(t *testing.T) {
	if got := sourceTypeFlag(ClusterPlan{}, ""); got != "--source-type <msk|apache-kafka>" {
		t.Errorf("undeclared scanless: %q", got)
	}
	if got := sourceTypeFlag(ClusterPlan{sourceDeclaredMSK: true}, ""); got != "--source-type msk" {
		t.Errorf("declared msk scanless: %q", got)
	}
	if got := sourceTypeFlag(ClusterPlan{SourcePlatform: "Apache Kafka"}, ""); got != "--source-type apache-kafka" {
		t.Errorf("declared apache-kafka scanless: %q", got)
	}
}

// With no clusters in the scan the inputs header must not claim the plan is ready.
func TestPlanInputsHeader_NoClusters(t *testing.T) {
	out := RenderPlanInputsYAML(&EnginePlan{})
	if strings.Contains(out, "All required questions answered") || !strings.Contains(out, "No clusters were found") {
		t.Errorf("empty-fleet header wrong:\n%s", out)
	}
}

func TestDocReplicatorCoversInterceptors(t *testing.T) {
	if docReplicator != "https://docs.confluent.io/platform/current/multi-dc-deployments/replicator/replicator-failover.html" {
		t.Errorf("docReplicator = %q", docReplicator)
	}
}
