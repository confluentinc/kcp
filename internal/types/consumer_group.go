package types

// This file holds the consumer-group discovery model (mirrors the Topics /
// TopicDetails / TopicSummary pattern in kafka.go). ConsumerGroups is held as a
// pointer on KafkaAdminClientInformation so nil means "not scanned" and an empty
// Details slice means "scanned, none found" — the same convention used for Topics.

type ConsumerGroups struct {
	Summary ConsumerGroupSummary   `json:"summary"`
	Details []ConsumerGroupDetails `json:"details"`
}

type ConsumerGroupSummary struct {
	Total   int            `json:"total"`
	ByType  map[string]int `json:"by_type"` // "" counts groups whose type the broker could not report
	ByState map[string]int `json:"by_state"`
}

type ConsumerGroupDetails struct {
	GroupID        string                `json:"group_id"`
	Type           string                `json:"type"` // classic/consumer/share/streams, or "" if unavailable
	State          string                `json:"state"`
	ProtocolType   string                `json:"protocol_type"`
	Coordinator    string                `json:"coordinator,omitempty"`
	Members        []ConsumerGroupMember `json:"members"`
	Topics         []string              `json:"topics"`
	DetailComplete bool                  `json:"detail_complete"` // false when the type has no type-specific describer yet (consumer/share/streams); see design §14.5
}

type ConsumerGroupMember struct {
	MemberID        string   `json:"member_id"`
	ClientID        string   `json:"client_id"`
	ClientHost      string   `json:"client_host"`
	GroupInstanceID string   `json:"group_instance_id,omitempty"`
	AssignedTopics  []string `json:"assigned_topics"`
}

// ConsumerGroupListing is the type-aware ListGroups result (id + KIP-848 type +
// state). Internal intermediate, NOT part of the serialized State shape.
type ConsumerGroupListing struct {
	GroupID string
	Type    string
	State   string
}

const (
	ConsumerGroupTypeClassic  = "classic"
	ConsumerGroupTypeConsumer = "consumer"
	ConsumerGroupTypeShare    = "share"
	ConsumerGroupTypeStreams  = "streams"
)

// CalculateConsumerGroupSummary mirrors CalculateTopicSummaryFromDetails.
func CalculateConsumerGroupSummary(details []ConsumerGroupDetails) ConsumerGroupSummary {
	summary := ConsumerGroupSummary{
		Total:   len(details),
		ByType:  map[string]int{},
		ByState: map[string]int{},
	}

	for _, group := range details {
		summary.ByType[group.Type]++
		summary.ByState[group.State]++
	}

	return summary
}
