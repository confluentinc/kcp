package reconcile

import "fmt"

// Side is one of a reversible migration's two clusters, by its manifest role.
type Side string

const (
	SideSource Side = "source"
	SideTarget Side = "target"
)

func (s Side) Other() Side {
	if s == SideSource {
		return SideTarget
	}
	return SideSource
}

// RowStatus is a mirror row's status on one copy of a bidirectional link. It
// tells apart the transitional statuses that the one-way paths' MirrorState
// lumps together, because a reversal passes through each of them.
type RowStatus int

const (
	RowNone RowStatus = iota
	RowActive
	RowStopped
	RowPendingSynchronize
	RowPendingMirror
	RowPendingStopped
	RowOther
)

// MirrorRow is one copy's row for a topic. Raw is the REST mirror_status, kept
// for refusal text.
type MirrorRow struct {
	Status RowStatus
	Raw    string
}

// LinkCopy is one cluster's copy of a bidirectional cluster link. Found is
// false when the link was never created on that cluster; no other field is
// then set. Rows is keyed by mirror topic name; a topic with no key has no row.
type LinkCopy struct {
	Found             bool
	LinkID            string
	RemoteClusterID   string
	LinkError         string
	Mode              string // link.mode
	Prefix            string // cluster.link.prefix
	OffsetSyncEnabled bool
	GroupFilters      string // raw consumer.offset.group.filters
	Rows              map[string]MirrorRow
}

// ReversibleInput is the reversible migration's request, decoupled from the
// manifest. SourceDomain is the streaming domain of spec.source's cluster and
// TargetDomain spec.target's. WritableOn is a domain name. The cluster IDs are
// the manifest's pins.
type ReversibleInput struct {
	Topics, TopicPatterns            []string
	Route                            string
	LinkName                         string
	SourceDomain, TargetDomain       string
	WritableOn                       string
	SourceClusterID, TargetClusterID string
}

func (in ReversibleInput) Domain(s Side) string {
	if s == SideSource {
		return in.SourceDomain
	}
	return in.TargetDomain
}

// ClusterLabel names a side's cluster for messages, e.g. "the source cluster
// (lkc-123)".
func (in ReversibleInput) ClusterLabel(s Side) string {
	id := in.TargetClusterID
	if s == SideSource {
		id = in.SourceClusterID
	}
	return fmt.Sprintf("the %s cluster (%s)", s, id)
}

// ReversibleGroupFacts is the consumer-group data the group checks read.
// Committed and ListingIncomplete come from the side the route is bound to:
// every group and the topics it has committed on, and why the credential may
// not list them all ("" when the listing is complete). The Entered fields are
// the same for the side being entered, which is always the writable side.
type ReversibleGroupFacts struct {
	Committed                map[string][]string
	ListingIncomplete        string
	EnteredStates            map[string]string
	EnteredTypes             map[string]string
	EnteredListingIncomplete string
}

// ReversibleFacts is everything ReconcileReversibleStatic reads, gathered live.
// The live cluster IDs are read over the Kafka protocol. Groups is nil unless
// the sides resolve and a reversal or switch can follow (Sides.Moving).
type ReversibleFacts struct {
	Gateway                                  *GatewayConfig
	SourceTopics, TargetTopics               []string
	LiveSourceClusterID, LiveTargetClusterID string
	SourceLink, TargetLink                   LinkCopy
	MissingSecrets                           []string
	SecretCheckSkipped                       string
	Groups                                   *ReversibleGroupFacts
}

func (f ReversibleFacts) Link(s Side) LinkCopy {
	if s == SideSource {
		return f.SourceLink
	}
	return f.TargetLink
}

func (f ReversibleFacts) Topics(s Side) []string {
	if s == SideSource {
		return f.SourceTopics
	}
	return f.TargetTopics
}

// Sides is the role each cluster plays in this run. Bound is the side the
// route points at now, and Fenced whether it carries kcp's static fence. A
// back-out is a fenced route already on the writable side: a flip stopped part
// way and the operator turned it around. Moving is whether a reversal or a
// switch can follow.
type Sides struct {
	Writable, Mirror             Side
	WritableDomain, MirrorDomain string
	Bound                        Side
	Fenced                       bool
	BackOut                      bool
	Moving                       bool
}

// Reversal is what a reversible static plan asks the state machine to do:
// reverse Reverse on ReverseOn's copy of the link (always the writable side),
// wait for AwaitStopped to settle there, and switch the route with SwitchOnly
// already writable. InScopeGroups are the groups to re-check before
// reversing. Every list is sorted.
type Reversal struct {
	Sides         Sides
	ReverseOn     Side
	Reverse       []string
	AwaitStopped  []string
	SwitchOnly    []string
	InScopeGroups []string
}
