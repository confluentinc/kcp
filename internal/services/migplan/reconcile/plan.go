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
	// Topics is the in-flight set (Migratable + AwaitStopped) that must reach
	// STOPPED before the switch → topics.json. Exclude AwaitStopped before
	// issuing promote calls.
	Topics []string
	// AwaitStopped is the subset of Topics already mid-promotion
	// (PENDING_STOPPED) on resume: the FSM waits for these to reach STOPPED
	// rather than re-promoting an already-promoting mirror.
	AwaitStopped    []string
	FenceRules      []byte // → fence-rules.yaml (whole rules block)
	SwitchoverRules []byte // → switchover-rules.yaml (whole rules block)
}

type Plan struct {
	Report    Report
	Artifacts *Artifacts // nil when refused OR when nothing is migratable (a no-op)

	// GatewayYAML is the whole gateway CR the plan was computed against,
	// cleaned of server-managed metadata by the provider layer (see
	// migplan/gatewayfile.go's cleanGatewayDoc). Set even on a refusal (the
	// pull precedes the checks) and empty only if the source did not carry
	// it. It exists for a later drift diff, not for the plan itself.
	GatewayYAML string

	// Mode is the route mode this plan was reconciled under ("dynamic" or
	// "static"), so a caller knows how to interpret Artifacts.FenceRules/
	// SwitchoverRules: a rules: fragment for dynamic, a fence/streamingDomain
	// block fragment for static — both meaning "splice this onto the named
	// route," never "apply this as the whole CR."
	Mode string
}
