package plan

import (
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

func bp(b bool) *bool { return &b }

func TestBuildProfile_Serverless(t *testing.T) {
	c := report.ProcessedCluster{
		Name: "srvless",
		AWSClientInformation: types.AWSClientInformation{
			MskClusterConfig: kafkatypes.Cluster{ClusterType: kafkatypes.ClusterTypeServerless},
		},
	}
	p := buildProfile(c, IntakeInputs{}, "", false)
	if p.MSKClusterType != engine.MSKServerless {
		t.Errorf("MSKClusterType=%q, want Serverless", p.MSKClusterType)
	}
	if len(p.SourceAuthTypes) != 1 || p.SourceAuthTypes[0] != engineAuthAWSIAM {
		t.Errorf("SourceAuthTypes=%v, want [AWS IAM]", p.SourceAuthTypes)
	}
	// End-to-end: Serverless is settled at Band 1 regardless of partitions.
	plan := engine.ComputePlan(p)
	if plan.Sizing.Band != 1 {
		t.Errorf("serverless band=%d, want 1", plan.Sizing.Band)
	}
}

func TestBuildProfile_ProvisionedSCRAM(t *testing.T) {
	c := report.ProcessedCluster{
		Name: "prov",
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
			Connectors: []types.ConnectorSummary{}, // scanned, none
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			Topics:          &types.Topics{Summary: types.TopicSummary{TotalPartitions: 5000}},
			ConnectClusters: []types.ConnectCluster{}, // scanned, none
		},
	}
	p := buildProfile(c, IntakeInputs{}, "", false)
	if p.MSKClusterType != engine.MSKProvisioned {
		t.Errorf("MSKClusterType=%q, want Provisioned", p.MSKClusterType)
	}
	if len(p.SourceAuthTypes) != 1 || p.SourceAuthTypes[0] != engineAuthSCRAM {
		t.Errorf("SourceAuthTypes=%v, want [SASL/SCRAM]", p.SourceAuthTypes)
	}
	if p.PartitionsExact == nil || *p.PartitionsExact != 5000 {
		t.Errorf("PartitionsExact=%v, want 5000", p.PartitionsExact)
	}
	if p.StorageMode == nil || *p.StorageMode != "No" {
		t.Errorf("StorageMode=%v, want No (local storage scanned)", p.StorageMode)
	}
	if p.MSKConnectPresent == nil || *p.MSKConnectPresent != "No" {
		t.Errorf("MSKConnectPresent=%v, want No (scanned, none)", p.MSKConnectPresent)
	}

	// End-to-end: a private, Band-2 workload lands on Enterprise, self-serve.
	plan := engine.ComputePlan(buildProfile(c, IntakeInputs{
		PublicEndpointsOK:        "No", // private required
		ConnectsToday:            "Same VPC",
		UseCaseBreadth:           "One team, one application",
		NeedsDataMigration:       "Yes",
		DowntimeTolerance:        "Minutes per service",
		AnyAppNeedsDataMigration: "No",
	}, "", false))
	if plan.ClusterType.Value != engine.TierEnterprise {
		t.Errorf("private Band 2: cluster type=%s, want Enterprise", plan.ClusterType.Value)
	}
	if plan.Withheld {
		t.Errorf("clean private Band 2 must not be withheld")
	}
}

// TestBuildProfile_MeasuredThroughput exercises the scan->profile->plan wiring for
// numeric throughput (CloudWatch aggregates), which plan-inputs can't drive: a low
// partition count but a measured ingress a band higher. The measured dimension is
// marked reviewed, so the sizing band escalates on ingress AND the unusual-workload-
// shape handoff fires (partitions Band 1, ingress Band 2).
func TestBuildProfile_MeasuredThroughput(t *testing.T) {
	c := report.ProcessedCluster{
		Name: "skewed",
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
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: 1000}}, // Band 1
		},
		ClusterMetrics: types.ProcessedClusterMetrics{
			Aggregates: map[string]types.MetricAggregate{
				"BytesInPerSec": {Maximum: fptr(400 * bytesPerMBps)}, // 400 MBps -> Band 2
			},
		},
	}
	p := buildProfile(c, IntakeInputs{}, "", false)
	if p.PeakIngressMbps == nil || *p.PeakIngressMbps != 400 {
		t.Fatalf("PeakIngressMbps=%v, want 400", p.PeakIngressMbps)
	}
	if !containsStr(p.SizingReviewed, "partitions_exact") || !containsStr(p.SizingReviewed, "peak_ingress_mbps") {
		t.Fatalf("SizingReviewed=%v, want measured partitions+ingress marked reviewed", p.SizingReviewed)
	}
	plan := engine.ComputePlan(p)
	if plan.Sizing.Band != 2 {
		t.Errorf("band=%d, want 2 (ingress drives)", plan.Sizing.Band)
	}
	// The measured ingress is a band above the partition anchor: an unusual shape we
	// route to a specialist rather than sizing automatically.
	if !plan.HumanAssist.Required {
		t.Fatalf("expected the unusual-workload-shape handoff to fire")
	}
	found := false
	for _, tr := range plan.HumanAssist.Triggers {
		if tr.ID == "unusual_workload_shape" {
			found = true
		}
	}
	if !found {
		t.Errorf("triggers=%v, want unusual_workload_shape", plan.HumanAssist.Triggers)
	}
}

func TestBucketKafkaVersion(t *testing.T) {
	cases := map[string]string{
		"":      "", // no scanned version -> empty, so the plan asks (kafka_version)
		"3.5.1": "3.0 or newer",
		"2.6.2": "2.4-2.9",
		"2.3.0": "Older than 2.4",
	}
	for in, want := range cases {
		if got := bucketKafkaVersion(in); got != want {
			t.Errorf("bucketKafkaVersion(%q)=%q, want %q", in, got, want)
		}
	}
}
