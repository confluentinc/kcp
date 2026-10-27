package engine

import (
	"strings"
	"testing"
)

func TestSizingShape_Mismatch(t *testing.T) {
	// Reviewed peer above the anchor is the unusual-shape edge case.
	shape := sizingShape(Profile{
		PartitionBand: "Under 2,500", RequestRateBand: "75,000–240,000/s",
		SizingReviewed: []string{"partition_band", "request_rate_band"},
	})
	if shape.Mismatch == nil || shape.Mismatch.Name != "request rate" || shape.Mismatch.Band != 3 || shape.Mismatch.AnchorBand != 1 {
		t.Errorf("mismatch = %+v, want {request rate, 3, 1}", shape.Mismatch)
	}
}

func TestSizingShape_EgressNeverMismatch(t *testing.T) {
	reviewed := sizingShape(Profile{
		PartitionBand: "Under 2,500", EgressBand: "1,800–5,760 MB/s",
		SizingReviewed: []string{"partition_band", "egress_band"},
	})
	if reviewed.Mismatch != nil {
		t.Errorf("egress must never be a mismatch, got %+v", reviewed.Mismatch)
	}
	// An unreviewed egress is still reported as an assumption.
	unrev := sizingShape(Profile{PartitionBand: "Under 2,500", SizingReviewed: []string{"partition_band"}})
	if !contains(unrev.Assumed, "egress") {
		t.Errorf("unreviewed egress should be assumed, got %v", unrev.Assumed)
	}
}

func TestHumanAssist_Triggers(t *testing.T) {
	hasTrigger := func(r HumanAssistResult, id string) bool {
		for _, tr := range r.Triggers {
			if tr.ID == id {
				return true
			}
		}
		return false
	}

	// Shared-fabric breadth.
	breadth := humanAssistDecision(Profile{UseCaseBreadth: breadthFabric}, haCtx{Tier: TierEnterprise, Band: 2})
	if !breadth.Required || !hasTrigger(breadth, "use_case_breadth") {
		t.Errorf("breadth: required=%v triggers=%+v", breadth.Required, breadth.Triggers)
	}

	// Connects "Other" on the private path, on an AWS target — connects_today is
	// only asked on AWS, so the trigger must require it too.
	other := humanAssistDecision(Profile{RequiresPrivateField: "Yes", TargetCloud: "AWS", ConnectsToday: connectsOther}, haCtx{Tier: TierEnterprise, Band: 2})
	if !hasTrigger(other, "connection_other") {
		t.Errorf("connection_other not fired: %+v", other.Triggers)
	}

	// Same private + Other answer, but a GCP/Azure target: connects_today is never
	// asked off-AWS, so this must not fire.
	for _, cloud := range []string{"GCP", "Azure"} {
		notAWS := humanAssistDecision(Profile{RequiresPrivateField: "Yes", TargetCloud: cloud, ConnectsToday: connectsOther}, haCtx{Tier: TierEnterprise, Band: 2})
		if hasTrigger(notAWS, "connection_other") {
			t.Errorf("connection_other fired on a %s target: %+v", cloud, notAWS.Triggers)
		}
	}

	// MSK on AWS IAM (no SASL/SCRAM) moving data to a non-AWS target: the jump cluster is
	// AWS-only, so it is a specialist handoff. AWS targets and SCRAM sources are unaffected.
	iam := func(cloud string, auths ...string) HumanAssistResult {
		return humanAssistDecision(Profile{SourcePlatform: "Amazon MSK", MSKClusterType: MSKProvisioned, SourceAuthTypes: auths,
			SourceAccessibility: "Private", TargetCloud: cloud, NeedsDataMigration: "Yes"}, haCtx{Tier: TierEnterprise, Band: 2})
	}
	for cloud, name := range map[string]string{"Azure": "Azure", "GCP": "Google Cloud"} {
		r := iam(cloud, authAWSIAM)
		if !hasTrigger(r, "iam_cross_cloud_jump_cluster") || !strings.Contains(r.Reason, "Your Confluent Cloud cluster is on "+name+", so we'll design the migration path with you.") {
			t.Errorf("iam_cross_cloud_jump_cluster on %s: %+v", cloud, r)
		}
		if hasTrigger(iam(cloud, authAWSIAM, authSCRAM), "iam_cross_cloud_jump_cluster") {
			t.Errorf("iam_cross_cloud_jump_cluster fired for IAM+SCRAM on %s", cloud)
		}
	}
	if hasTrigger(iam("AWS", authAWSIAM), "iam_cross_cloud_jump_cluster") {
		t.Errorf("iam_cross_cloud_jump_cluster fired on an AWS target")
	}

	// Every Dedicated route is a trigger; the cause is named, ceiling-figures stay internal.
	ded := humanAssistDecision(Profile{}, haCtx{
		Tier: TierDedicated, Band: 3,
		DedicatedReasons: []cause{{ID: "band4_exceeds_enterprise_cap", Customer: "a workload above what our Enterprise clusters hold", Encouragement: "Dedicated clusters scale well past the limits we publish for Enterprise."}},
	})
	if !ded.Required || !hasTrigger(ded, "dedicated_0") {
		t.Errorf("dedicated: required=%v triggers=%+v", ded.Required, ded.Triggers)
	}

	if !strings.HasSuffix(ded.Reason, "Talk to one of our technical experts and they'll size it with you.") {
		t.Errorf("dedicated handoff closer = %q", ded.Reason)
	}

	// Declared Enterprise-ceiling breach on Band 2: banner names no figure, internal keeps them.
	over := humanAssistDecision(Profile{ExceedsEnterpriseLimits: "Yes"}, haCtx{Tier: TierDedicated, Band: 2})
	var t2 *Trigger
	for i := range over.Triggers {
		if over.Triggers[i].ID == "exceeds_enterprise_limits" {
			t2 = &over.Triggers[i]
		}
	}
	if t2 == nil {
		t.Fatalf("exceeds_enterprise_limits trigger missing: %+v", over.Triggers)
	}
	if containsAnyFold(t2.Why, "megabytes/sec", "requests/sec") {
		t.Errorf("banner must not name a specific ceiling: %q", t2.Why)
	}
	if !containsAnyFold(t2.Internal, "megabytes/sec") {
		t.Errorf("internal record should keep the figures: %q", t2.Internal)
	}

	// Unusual shape (reviewed peer above the anchor).
	shape := humanAssistDecision(Profile{
		PartitionBand: "Under 2,500", RequestRateBand: "75,000–240,000/s",
		SizingReviewed: []string{"partition_band", "request_rate_band"},
	}, haCtx{Tier: TierStandard, Band: 3})
	if !hasTrigger(shape, "unusual_workload_shape") {
		t.Errorf("unusual_workload_shape not fired: %+v", shape.Triggers)
	}

	// Clean plan: no trigger, but the CTA and completeness copy still land.
	clean := humanAssistDecision(Profile{}, haCtx{Tier: TierStandard, Band: 1, Complete: true})
	if clean.Required || clean.Value != "Not needed" || clean.Action != "Talk to a person" {
		t.Errorf("clean: %+v", clean)
	}
}

func TestDedicatedSrcReason_MtlsAttribution(t *testing.T) {
	c := cause{ID: "mtls_on_gcp_target"}
	src, _ := dedicatedSrcReason(Profile{TargetIdentityModel: []string{"mTLS"}}, c)
	if !strings.Contains(src, "you chose mTLS for your clients on Confluent Cloud") {
		t.Errorf("target-only: %q", src)
	}
	src, _ = dedicatedSrcReason(Profile{SourceAuthTypes: []string{authMTLS}}, c)
	if !strings.Contains(src, "your source uses mTLS") {
		t.Errorf("source: %q", src)
	}
	src, _ = dedicatedSrcReason(Profile{SourceAuthTypes: []string{authMTLS}, TargetIdentityModel: []string{"mTLS"}}, c)
	if !strings.Contains(src, "your source uses mTLS") {
		t.Errorf("both: %q", src)
	}
}

// The size-driven Dedicated handoff doesn't claim an Enterprise cluster can't hold
// the workload (the size band is a planning cutoff); it says custom sizing is the
// reason and names no cap.
func TestHumanAssist_SizeHandoffIsAboutCustomSizing(t *testing.T) {
	plan := ComputePlan(baseProfile(func(p *Profile) { p.PartitionBand = "Over 96,000" }))
	if !plan.Withheld || !hasTriggerID(plan, "dedicated_0") {
		t.Fatalf("expected a size-driven handoff: withheld=%v triggers=%+v", plan.Withheld, plan.HumanAssist.Triggers)
	}
	why := plan.HumanAssist.Reason
	if !strings.Contains(why, "Workloads at this scale benefit from custom sizing, so we'd plan the right cluster with you rather than size it automatically.") {
		t.Errorf("handoff should explain custom sizing: %q", why)
	}
	for _, banned := range []string{"Enterprise clusters hold", "(30,000 partitions)", "  "} {
		if strings.Contains(why, banned) {
			t.Errorf("handoff should not contain %q: %q", banned, why)
		}
	}
}

// An AWS IAM source going to GCP raises only the IAM jump-cluster handoff: the generic
// GCP private-source link handoff says the same thing, so it is suppressed.
func TestHumanAssist_IAMToGCPRaisesOnlyJumpClusterHandoff(t *testing.T) {
	p := Profile{SourcePlatform: "Amazon MSK", MSKClusterType: MSKProvisioned, SourceAuthTypes: []string{authAWSIAM},
		SourceAccessibility: "Private", TargetCloud: "GCP", RequiresPrivateField: "Yes", AnyAppNeedsDataMigration: "Yes", NeedsDataMigration: "Yes"}
	r := humanAssistDecision(p, haCtx{Tier: TierEnterprise, Band: 2, GCPPrivateLink: true})
	has := func(id string) bool {
		for _, tr := range r.Triggers {
			if tr.ID == id {
				return true
			}
		}
		return false
	}
	if !has("iam_cross_cloud_jump_cluster") || has("gcp_private_source_cluster_link") {
		t.Errorf("triggers = %+v, want only iam_cross_cloud_jump_cluster", r.Triggers)
	}
}

// A GCP target with connector egress is private even when public endpoints are fine.
func TestWillBePrivate_GCPEgressRequired(t *testing.T) {
	p := Profile{TargetCloud: "GCP", RequiresPrivateField: "No", CCEgressRequired: "Yes"}
	if !willBePrivate(p) {
		t.Error("GCP + cc_egress_required Yes should be private")
	}
}

// The on-prem Apache Kafka handoff drops the Enterprise tier name when the settled tier
// is Dedicated, so it doesn't contradict the Dedicated banner.
func TestHumanAssist_OnPremHandoffDropsTierWhenDedicated(t *testing.T) {
	p := Profile{SourceType: SourceApacheKafka, SourcePlatform: "Apache Kafka", SourceCloud: "On-prem or other", RequiresPrivateField: "Yes", AnyAppNeedsDataMigration: "Yes", NeedsDataMigration: "Yes", TargetCloud: "AWS"}
	for tier, want := range map[Tier]string{TierEnterprise: "An Enterprise cluster can't link", TierDedicated: "Confluent Cloud can't link"} {
		r := humanAssistDecision(p, haCtx{Tier: tier, Band: 2})
		if !strings.Contains(r.Reason, want) {
			t.Errorf("%s: reason = %q, want %q", tier, r.Reason, want)
		}
	}
}
