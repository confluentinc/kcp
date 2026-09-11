// Package reconcile is the pure reconciliation core for topic-based migrations.
// It performs no I/O: callers gather the four live inputs and pass plain data in.
package reconcile

type Verdict int

const (
	Migratable Verdict = iota
	Unchanged
	FailFast
)

func (v Verdict) String() string {
	switch v {
	case Migratable:
		return "migratable"
	case Unchanged:
		return "unchanged"
	case FailFast:
		return "fail-fast"
	default:
		return "unknown"
	}
}

type MirrorState int

const (
	MirrorNone    MirrorState = iota // topic is not a mirror on the link
	MirrorActive                     // ACTIVE — mirroring
	MirrorStopped                    // STOPPED — promoted
	MirrorBad                        // any other/transient status (PENDING, FAILED, …)
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
}

type Report struct {
	Preconditions []PreconditionResult
	Migratable    []TopicVerdict
	Unchanged     []TopicVerdict
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
	Topics          []string // -> topics.json (cluster-link promote input)
	FenceRules      []byte   // -> fence-rules.yaml (whole rules block)
	SwitchoverRules []byte   // -> switchover-rules.yaml (whole rules block)
}

type Plan struct {
	Report    Report
	Artifacts *Artifacts // nil when refused OR when nothing is migratable (a no-op)

	// GatewayYAML is the whole gateway CR the plan was computed against, exactly
	// as pulled. Set even on a refusal (the pull precedes the checks) and empty
	// only if the source did not carry it. It exists for a later drift diff, not
	// for the plan itself.
	GatewayYAML string

	// Mode is the route mode this plan was reconciled under ("dynamic" or
	// "static"), so a caller knows how to interpret Artifacts.FenceRules/
	// SwitchoverRules: a rules: fragment for dynamic, a fence/streamingDomain
	// block fragment for static — both meaning "splice this onto the named
	// route," never "apply this as the whole CR."
	Mode string
}
