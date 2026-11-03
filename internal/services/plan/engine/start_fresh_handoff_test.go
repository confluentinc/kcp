package engine

import (
	"strings"
	"testing"
)

const startFreshHandoffCopy = "we'd design the path with you"

// startFreshHandoffRepro returns the start-fresh alternative and the plan the
// customer would get by flipping needs_data_migration to Yes.
func startFreshHandoffRepro(mut func(*Profile)) (PlanResult, PlanResult) {
	p := Profile{
		SourcePlatform: "Amazon MSK", SourceCloud: "AWS", TargetCloud: "Azure",
		PublicEndpointsOK: "No", SourceAuthTypes: []string{authAWSIAM},
		AnyAppNeedsDataMigration: "No", NeedsDataMigration: "No",
		CCEgressRequired: "No", PartitionBand: "Under 2,500",
	}
	mut(&p)
	flipped := p
	flipped.NeedsDataMigration = "Yes"
	return ComputePlan(p), ComputePlan(flipped)
}

func TestStartFreshAlternative_HandsOffForIAMCrossCloudJumpCluster(t *testing.T) {
	plan, flipped := startFreshHandoffRepro(func(p *Profile) {})
	if !hasTriggerID(flipped, "iam_cross_cloud_jump_cluster") {
		t.Fatalf("flipped plan should fire iam_cross_cloud_jump_cluster: %+v", flipped.HumanAssist.Triggers)
	}
	r := plan.Switchover.Alternative.Reason
	if !strings.Contains(r, startFreshHandoffCopy) || strings.Contains(r, "jump cluster") {
		t.Errorf("alternative should hand off, got %q", r)
	}
}

func TestStartFreshAlternative_HandsOffForGCPPrivateSourceLink(t *testing.T) {
	plan, flipped := startFreshHandoffRepro(func(p *Profile) {
		p.SourceAuthTypes = []string{authSCRAM}
		p.TargetCloud = "GCP"
	})
	if !hasTriggerID(flipped, "gcp_private_source_cluster_link") {
		t.Fatalf("flipped plan should fire gcp_private_source_cluster_link: %+v", flipped.HumanAssist.Triggers)
	}
	r := plan.Switchover.Alternative.Reason
	if !strings.Contains(r, startFreshHandoffCopy) || strings.Contains(r, "Cluster Linking") {
		t.Errorf("alternative should hand off, got %q", r)
	}
}

func TestStartFreshAlternative_NamesMechanismWhenFlippingFiresNoHandoff(t *testing.T) {
	plan, _ := startFreshHandoffRepro(func(p *Profile) { p.TargetCloud = "AWS" })
	if r := plan.Switchover.Alternative.Reason; strings.Contains(r, startFreshHandoffCopy) {
		t.Errorf("alternative should name the mechanism, got %q", r)
	}
}

func TestConnectionOther_NotFiredForOnPremSource(t *testing.T) {
	p := Profile{
		SourceType: SourceApacheKafka, SourcePlatform: "Apache Kafka", SourceCloud: "On-prem or other",
		RequiresPrivateField: "Yes", TargetCloud: "AWS", ConnectsToday: connectsOther,
		NeedsDataMigration: "No", AnyAppNeedsDataMigration: "No",
	}
	if asksConnectsToday(p) {
		t.Fatal("connects_today is not asked for an on-prem source")
	}
	if hasTriggerID(ComputePlan(p), "connection_other") {
		t.Error("connection_other fired on an unasked answer")
	}
	p.SourceCloud = "AWS"
	if !hasTriggerID(ComputePlan(p), "connection_other") {
		t.Error("connection_other should still fire when the question is asked")
	}
}
