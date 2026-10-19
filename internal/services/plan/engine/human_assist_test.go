package engine

import "testing"

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

	// Connects "Other" on the private path.
	other := humanAssistDecision(Profile{RequiresPrivateField: "Yes", ConnectsToday: connectsOther}, haCtx{Tier: TierEnterprise, Band: 2})
	if !hasTrigger(other, "connection_other") {
		t.Errorf("connection_other not fired: %+v", other.Triggers)
	}

	// Every Dedicated route is a trigger; the cause is named, ceiling-figures stay internal.
	ded := humanAssistDecision(Profile{}, haCtx{
		Tier: TierDedicated, Band: 3,
		DedicatedReasons: []cause{{ID: "band4_exceeds_enterprise_cap", Customer: "a workload above what our Enterprise clusters hold", Encouragement: "Dedicated clusters scale well past the limits we publish for Enterprise."}},
	})
	if !ded.Required || !hasTrigger(ded, "dedicated_0") {
		t.Errorf("dedicated: required=%v triggers=%+v", ded.Required, ded.Triggers)
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
