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
