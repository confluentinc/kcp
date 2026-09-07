package kafka

import (
	"sort"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/types"
)

// Pure, broker-independent core of consumer-group discovery: mapping raw Kafka
// responses into the domain model, and the version-fallback decision. Both the
// unit tests and the integration suite exercise these, so the mapping is
// validated identically in both.

type DescribedGroup struct {
	GroupID      string
	State        string
	ProtocolType string
	Coordinator  string
	Members      []DescribedMember
}

type DescribedMember struct {
	MemberID        string
	ClientID        string
	ClientHost      string
	GroupInstanceID string
	AssignedTopics  []string
}

// MapConsumerGroups assembles the domain ConsumerGroups from the type-aware
// listing (source of Type; State when describe is unavailable) and the per-group
// describe detail (authoritative State, members, assigned topics). Output is
// sorted by group id; topics sorted — deterministic diffs and test assertions.
func MapConsumerGroups(listings []types.ConsumerGroupListing, described map[string]DescribedGroup) *types.ConsumerGroups {
	details := make([]types.ConsumerGroupDetails, 0, len(listings))

	for _, listing := range listings {
		detail := types.ConsumerGroupDetails{
			GroupID: listing.GroupID,
			Type:    listing.Type,
			State:   listing.State,
			Members: []types.ConsumerGroupMember{},
			Topics:  []string{},
		}

		if dg, ok := described[listing.GroupID]; ok {
			if dg.State != "" {
				detail.State = dg.State
			}
			detail.ProtocolType = dg.ProtocolType
			detail.Coordinator = dg.Coordinator

			topicSet := map[string]struct{}{}
			for _, m := range dg.Members {
				assigned := append([]string(nil), m.AssignedTopics...)
				sort.Strings(assigned)
				detail.Members = append(detail.Members, types.ConsumerGroupMember{
					MemberID:        m.MemberID,
					ClientID:        m.ClientID,
					ClientHost:      m.ClientHost,
					GroupInstanceID: m.GroupInstanceID,
					AssignedTopics:  assigned,
				})
				for _, t := range m.AssignedTopics {
					topicSet[t] = struct{}{}
				}
			}

			topics := make([]string, 0, len(topicSet))
			for t := range topicSet {
				topics = append(topics, t)
			}
			sort.Strings(topics)
			detail.Topics = topics
		}

		details = append(details, detail)
	}

	sort.Slice(details, func(i, j int) bool { return details[i].GroupID < details[j].GroupID })

	return &types.ConsumerGroups{
		Summary: types.CalculateConsumerGroupSummary(details),
		Details: details,
	}
}

// BuildConsumerGroups is the sarama adapter: decodes sarama GroupDescriptions into
// DescribedGroups, then hands off to MapConsumerGroups. The single function the
// collector and integration suite both call.
func BuildConsumerGroups(listings []types.ConsumerGroupListing, descriptions []*sarama.GroupDescription, coordinators map[string]string) *types.ConsumerGroups {
	described := make(map[string]DescribedGroup, len(descriptions))
	for _, gd := range descriptions {
		if gd == nil {
			continue
		}
		described[gd.GroupId] = describeFromSarama(gd, coordinators[gd.GroupId])
	}
	// A group's coordinator is resolved via FindCoordinator, which works for every
	// group type independently of DescribeGroups. Non-classic groups are not
	// described (their classic-API answer is useless — see scanConsumerGroups), so
	// seed a coordinator-only entry for any group that has a coordinator but no
	// description, so the coordinator still reaches the result. State stays empty
	// here, so MapConsumerGroups keeps the group's accurate ListGroups v5 state.
	for _, l := range listings {
		if _, ok := described[l.GroupID]; ok {
			continue
		}
		if coord := coordinators[l.GroupID]; coord != "" {
			described[l.GroupID] = DescribedGroup{GroupID: l.GroupID, Coordinator: coord}
		}
	}
	return MapConsumerGroups(listings, described)
}

func describeFromSarama(gd *sarama.GroupDescription, coordinator string) DescribedGroup {
	members := make([]DescribedMember, 0, len(gd.Members))
	for _, m := range gd.Members {
		instanceID := ""
		if m.GroupInstanceId != nil {
			instanceID = *m.GroupInstanceId
		}
		members = append(members, DescribedMember{
			MemberID:        m.MemberId,
			ClientID:        m.ClientId,
			ClientHost:      m.ClientHost,
			GroupInstanceID: instanceID,
			AssignedTopics:  decodeSaramaAssignment(m),
		})
	}
	return DescribedGroup{
		GroupID:      gd.GroupId,
		State:        gd.State,
		ProtocolType: gd.ProtocolType,
		Coordinator:  coordinator,
		Members:      members,
	}
}

// decodeSaramaAssignment extracts assigned topic names from a member's assignment
// bytes. Deliberately tolerant: a KIP-848 consumer-protocol member does not encode
// a classic assignment, so GetMemberAssignment() may error or return nil — record
// the member with no topics rather than failing the group (design §7.1 / §14).
func decodeSaramaAssignment(m *sarama.GroupMemberDescription) []string {
	if m == nil || len(m.MemberAssignment) == 0 {
		return nil
	}
	assignment, err := m.GetMemberAssignment()
	if err != nil || assignment == nil {
		return nil
	}
	topics := make([]string, 0, len(assignment.Topics))
	for t := range assignment.Topics {
		topics = append(topics, t)
	}
	sort.Strings(topics)
	return topics
}
