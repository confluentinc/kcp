package reconcile

import (
	"fmt"
	"strings"
)

// GatewayPodsCheckName names the precondition that carries the pod check's
// verdict.
const GatewayPodsCheckName = "gateway pods serve the CR"

// SyncVerdict is what the pod check concluded about whether the gateway pods
// serve the CR as it stands.
type SyncVerdict int

const (
	SyncUnchecked  SyncVerdict = iota // no pod check ran (a fixture, or verify_fence): the core applies nothing
	SyncInSync                        // the pods serve the CR
	SyncOutOfSync                     // they do not, or a rollout or reload is still in progress
	SyncCannotTell                    // the check has no way to compare the pods with the CR
)

// GatewaySync is the pod check's verdict as the core reads it.
type GatewaySync struct {
	Verdict SyncVerdict
	// Detail says what was observed, one sentence.
	Detail string
	// Evidence is the observations behind it, one fact per line.
	Evidence []string
}

// applyGatewaySync turns the pod check's verdict into a precondition and,
// when the plan cannot proceed on it, into a refusal. Out of sync always
// refuses. Cannot tell refuses only when the plan the core built treats a
// gateway-side step as already done (gatewayStepsAlreadyDone): a plan that
// starts from scratch has its first gateway write reload every pod and be
// checked before any decision depends on them. A refusal strips the plan of
// everything that would let the caller act on it.
func applyGatewaySync(plan *Plan, gw *GatewayConfig) *Plan {
	if gw == nil {
		return plan
	}
	sync := gw.Sync
	switch sync.Verdict {
	case SyncInSync:
		plan.Report.Preconditions = append(plan.Report.Preconditions, pass(GatewayPodsCheckName))
	case SyncOutOfSync:
		plan.Report.Preconditions = append(plan.Report.Preconditions, PreconditionResult{
			Name: GatewayPodsCheckName, Detail: sync.Detail, Evidence: sync.Evidence})
		stripToRefusal(plan)
	case SyncCannotTell:
		done := gatewayStepsAlreadyDone(plan)
		if len(done) == 0 {
			plan.Report.Preconditions = append(plan.Report.Preconditions, PreconditionResult{
				Name: GatewayPodsCheckName, OK: true, Skipped: true, Detail: sync.Detail, Evidence: sync.Evidence})
			break
		}
		plan.Report.Preconditions = append(plan.Report.Preconditions, PreconditionResult{
			Name:     GatewayPodsCheckName,
			Detail:   sync.Detail + "; this plan treats " + strings.Join(done, " and ") + " as already done",
			Evidence: sync.Evidence})
		stripToRefusal(plan)
	}
	return plan
}

// stripToRefusal removes everything a caller could act on from a plan.
func stripToRefusal(plan *Plan) {
	plan.Artifacts = nil
	plan.NothingToDo = false
	plan.Report.RestoreOffsetSync = false
	plan.Report.ConvertToStatic = false
}

// gatewayStepsAlreadyDone lists the gateway-side steps the plan counts as
// already done, derived from the plan the core built rather than from the
// CR's shape: kcp's fence, topics already on the target, a completed
// conversion.
func gatewayStepsAlreadyDone(plan *Plan) []string {
	var done []string
	if plan.Artifacts != nil && plan.Artifacts.FencedAtStart {
		done = append(done, "the fence")
	}
	convert := plan.Mode == convertMode
	if n := len(plan.Report.Unchanged); n > 0 {
		if convert {
			done = append(done, fmt.Sprintf("%d link topic(s) routed to the target", n))
		} else {
			done = append(done, fmt.Sprintf("%d topic(s) on the target", n))
		}
	}
	if convert && plan.NothingToDo {
		done = append(done, "the conversion")
	}
	return done
}
