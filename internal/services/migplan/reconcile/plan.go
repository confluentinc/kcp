// Package reconcile is the pure reconciliation core for topic-based migrations.
// It performs no I/O: callers gather the four live inputs and pass plain data in.
package reconcile

type Verdict int

const (
	Migratable Verdict = iota
	Unchanged
	FailFast
	SwitchOnly   // resume: already promoted (STOPPED) but not yet switched → switch only
	AwaitStopped // resume: promotion in flight (PENDING_STOPPED) → await STOPPED, then switch
)

func (v Verdict) String() string {
	switch v {
	case Migratable:
		return "migratable"
	case Unchanged:
		return "unchanged"
	case FailFast:
		return "fail-fast"
	case SwitchOnly:
		return "switch-only"
	case AwaitStopped:
		return "await-stopped"
	default:
		return "unknown"
	}
}

type MirrorState int

const (
	MirrorNone    MirrorState = iota // topic is not a mirror on the link
	MirrorActive                     // ACTIVE — mirroring
	MirrorStopped                    // STOPPED — promoted
	MirrorBad                        // genuine failure / unexpected status (FAILED, PAUSED, …) — refuse
	MirrorPending                    // PENDING_STOPPED — promotion in flight toward STOPPED; await
)

func (m MirrorState) String() string {
	switch m {
	case MirrorNone:
		return "none"
	case MirrorActive:
		return "mirroring"
	case MirrorStopped:
		return "stopped"
	case MirrorBad:
		return "bad"
	case MirrorPending:
		return "pending"
	default:
		return "unknown"
	}
}

// TopicVerdict is one topic's classification. S/M/T/R are the diagnostic facts
// ("present"/"absent", mirror state, "present"/"absent", "->source"/"->target").
type TopicVerdict struct {
	Topic      string
	Verdict    Verdict
	Reason     string // fail-fast only: the human-readable reason the topic is blocked
	S, M, T, R string // diagnostics for --verbose
}

type PreconditionResult struct {
	Name   string
	OK     bool
	Detail string
	// Skipped is true when the check itself could not be run (e.g. a
	// permission denial reading a live resource) rather than confirmed to
	// pass. Always paired with OK: true — a skip is advisory, never a
	// refusal reason — but callers rendering the report must not show it as
	// a plain "✓ passed" the way a real pass is shown, since that would
	// claim a verification that never happened.
	Skipped bool
}

type Report struct {
	Preconditions []PreconditionResult
	Migratable    []TopicVerdict
	Unchanged     []TopicVerdict
	SwitchOnly    []TopicVerdict // resume: already promoted, needs switching only
	AwaitStopped  []TopicVerdict // resume: promotion in flight, await STOPPED then switch
	FailFast      []TopicVerdict
	Warnings      []string

	// RestoreOffsetSync is true when this static run must set the link's
	// consumer offset sync back to the manifest's baseline after the switch:
	// the pause is opted in and either this run pauses (a cutover is in
	// flight) or the link's live offset sync still differs from the baseline.
	// Never true for a refused run or a dynamic route.
	RestoreOffsetSync bool

	// ConvertToStatic is true when a conversion (ReconcileConvert) has work:
	// fence, sync offsets, and switch the route to static. Never true for a
	// refused or nothing-to-do conversion, or a topic migration.
	ConvertToStatic bool
}

// Refused reports whether the run must emit no artifacts: any failed
// precondition, or any fail-fast topic.
func (r Report) Refused() bool {
	for _, p := range r.Preconditions {
		if !p.OK {
			return true
		}
	}
	return len(r.FailFast) > 0
}

type Artifacts struct {
	// PromoteTopics are the topics that must still reach STOPPED before the
	// switch (Migratable + AwaitStopped) → topics.json. Exclude AwaitStopped
	// before issuing promote calls. SwitchOnly topics are already STOPPED, so
	// they are migrated this run but absent here.
	PromoteTopics []string
	// AwaitStopped is the subset of PromoteTopics already mid-promotion
	// (PENDING_STOPPED) on resume: the FSM waits for these to reach STOPPED
	// rather than re-promoting an already-promoting mirror.
	AwaitStopped    []string
	FenceRules      []byte // → fence-rules.yaml (whole rules block)
	SwitchoverRules []byte // → switchover-rules.yaml (whole rules block)

	// RollbackFenceRules is the route as a rollback leaves it: the
	// start-of-run route with kcp's fence for this batch taken out.
	RollbackFenceRules []byte
	// RollbackAllowed is true when a pre-promote failure may roll back
	// (unfence): no topic in the batch is promoted or promoting yet.
	RollbackAllowed bool

	// FencedAtStart is true when the start-of-run route already carries kcp's
	// fence for this migration: an earlier run fenced it and was interrupted.
	// The fence is then up before this run's own fence step.
	FencedAtStart bool
	// MigrateTopics is every topic this run migrates (Migratable +
	// AwaitStopped + SwitchOnly), sorted: the topics the fence check watches
	// for producers writing straight to the source.
	MigrateTopics []string
}

type Plan struct {
	Report    Report
	Artifacts *Artifacts // nil when refused OR when no topic is left to migrate

	// NothingToDo is true when the run is not refused and has no work at all:
	// no topic left to migrate and no offset-sync restore owed. The caller
	// then runs no state machine.
	NothingToDo bool

	// GatewayYAML is the whole gateway CR the plan was computed against,
	// cleaned of server-managed metadata by the provider layer (see
	// migplan/gatewayfile.go's cleanGatewayDoc). Set even on a refusal (the
	// pull precedes the checks) and empty only if the source did not carry
	// it. It exists for a later drift diff, not for the plan itself.
	GatewayYAML string

	// Mode is the strategy this plan was reconciled under ("dynamic",
	// "static" or "convert"), so a caller knows how to interpret the
	// artifacts. Dynamic: every one is a whole rules: block, set on the named
	// route's rules. Static: FenceRules is a {fence: …} block set on the named
	// route; SwitchoverRules and RollbackFenceRules are a whole {route: …} that
	// replaces the named route (both remove the fence, which a field patch
	// cannot). Convert: FenceRules and RollbackFenceRules are whole rules:
	// blocks; SwitchoverRules is a whole {route: …} — the route converted to
	// static. Never "apply this as the whole CR."
	Mode string
}
