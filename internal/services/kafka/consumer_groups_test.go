package kafka

import (
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMapConsumerGroups_TypeMatrix(t *testing.T) {
	groupTypes := []string{
		types.ConsumerGroupTypeClassic,
		types.ConsumerGroupTypeConsumer,
		types.ConsumerGroupTypeShare,
		types.ConsumerGroupTypeStreams,
		"",
	}
	for _, gt := range groupTypes {
		name := gt
		if name == "" {
			name = "unknown-empty"
		}
		t.Run(name, func(t *testing.T) {
			listings := []types.ConsumerGroupListing{{GroupID: "g1", Type: gt, State: "Stable"}}
			described := map[string]DescribedGroup{"g1": {GroupID: "g1", State: "Stable", ProtocolType: "consumer"}}
			got := MapConsumerGroups(listings, described)
			require.Len(t, got.Details, 1)
			assert.Equal(t, gt, got.Details[0].Type)
			assert.Equal(t, 1, got.Summary.ByType[gt])
			assert.Equal(t, 1, got.Summary.Total)
		})
	}
}

func TestMapConsumerGroups_VersionScenarios(t *testing.T) {
	tests := []struct {
		name      string
		listing   types.ConsumerGroupListing
		wantType  string
		wantState string
	}{
		{"v5_type_and_state", types.ConsumerGroupListing{GroupID: "g", Type: "classic", State: "Stable"}, "classic", "Stable"},
		{"v4_state_only_no_type", types.ConsumerGroupListing{GroupID: "g", Type: "", State: "Empty"}, "", "Empty"},
		{"pre_v4_no_type_no_state", types.ConsumerGroupListing{GroupID: "g", Type: "", State: ""}, "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := MapConsumerGroups([]types.ConsumerGroupListing{tc.listing}, nil)
			require.Len(t, got.Details, 1)
			assert.Equal(t, tc.wantType, got.Details[0].Type)
			assert.Equal(t, tc.wantState, got.Details[0].State)
		})
	}
}

func TestMapConsumerGroups_SummaryTallies(t *testing.T) {
	listings := []types.ConsumerGroupListing{
		{GroupID: "a", Type: "classic", State: "Stable"},
		{GroupID: "b", Type: "classic", State: "Empty"},
		{GroupID: "c", Type: "consumer", State: "Stable"},
		{GroupID: "d", Type: "", State: "Dead"},
	}
	got := MapConsumerGroups(listings, nil)
	assert.Equal(t, 4, got.Summary.Total)
	assert.Equal(t, map[string]int{"classic": 2, "consumer": 1, "": 1}, got.Summary.ByType)
	assert.Equal(t, map[string]int{"Stable": 2, "Empty": 1, "Dead": 1}, got.Summary.ByState)
}

func TestMapConsumerGroups_DescribeDetail(t *testing.T) {
	listings := []types.ConsumerGroupListing{{GroupID: "checkout", Type: "classic", State: "Stable"}}
	described := map[string]DescribedGroup{
		"checkout": {
			GroupID: "checkout", State: "Stable", ProtocolType: "consumer", Coordinator: "b-2.example:9092",
			Members: []DescribedMember{
				{MemberID: "m1", ClientID: "svc-a", ClientHost: "/10.0.0.1", GroupInstanceID: "svc-a-0", AssignedTopics: []string{"orders", "payments"}},
				{MemberID: "m2", ClientID: "svc-a", ClientHost: "/10.0.0.2", AssignedTopics: []string{"payments", "shipments"}},
			},
		},
	}
	got := MapConsumerGroups(listings, described)
	require.Len(t, got.Details, 1)
	d := got.Details[0]
	assert.Equal(t, "b-2.example:9092", d.Coordinator)
	assert.Equal(t, "consumer", d.ProtocolType)
	require.Len(t, d.Members, 2)
	assert.Equal(t, "svc-a-0", d.Members[0].GroupInstanceID)
	assert.Equal(t, "/10.0.0.1", d.Members[0].ClientHost)
	assert.Equal(t, []string{"orders", "payments"}, d.Members[0].AssignedTopics)
	assert.Equal(t, []string{"orders", "payments", "shipments"}, d.Topics)
}

func TestMapConsumerGroups_StatePrecedence(t *testing.T) {
	t.Run("describe_state_overrides_listing", func(t *testing.T) {
		got := MapConsumerGroups(
			[]types.ConsumerGroupListing{{GroupID: "g", Type: "classic", State: "PreparingRebalance"}},
			map[string]DescribedGroup{"g": {GroupID: "g", State: "Stable"}},
		)
		assert.Equal(t, "Stable", got.Details[0].State)
	})
	t.Run("listing_state_used_when_describe_state_empty", func(t *testing.T) {
		got := MapConsumerGroups(
			[]types.ConsumerGroupListing{{GroupID: "g", Type: "classic", State: "Empty"}},
			map[string]DescribedGroup{"g": {GroupID: "g", State: ""}},
		)
		assert.Equal(t, "Empty", got.Details[0].State)
	})
}

func TestMapConsumerGroups_EmptyGroup(t *testing.T) {
	got := MapConsumerGroups(
		[]types.ConsumerGroupListing{{GroupID: "abandoned", Type: "classic", State: "Empty"}},
		map[string]DescribedGroup{},
	)
	require.Len(t, got.Details, 1)
	d := got.Details[0]
	assert.Equal(t, "Empty", d.State)
	assert.Empty(t, d.Members)
	assert.Empty(t, d.Topics)
	assert.NotNil(t, d.Members)
	assert.NotNil(t, d.Topics)
}

func TestMapConsumerGroups_Deterministic(t *testing.T) {
	listings := []types.ConsumerGroupListing{
		{GroupID: "zebra", Type: "classic", State: "Stable"},
		{GroupID: "alpha", Type: "classic", State: "Stable"},
		{GroupID: "mike", Type: "classic", State: "Stable"},
	}
	got := MapConsumerGroups(listings, nil)
	require.Len(t, got.Details, 3)
	assert.Equal(t, "alpha", got.Details[0].GroupID)
	assert.Equal(t, "mike", got.Details[1].GroupID)
	assert.Equal(t, "zebra", got.Details[2].GroupID)
}

func TestBuildConsumerGroups_ToleratesUndecodableAssignment(t *testing.T) {
	garbage := []byte{0xFF, 0xFF, 0xFF, 0xFF, 0x00, 0x11}
	descriptions := []*sarama.GroupDescription{
		{
			GroupId: "g", State: "Stable", ProtocolType: "consumer",
			Members: map[string]*sarama.GroupMemberDescription{
				"m1": {MemberId: "m1", ClientId: "c1", ClientHost: "/10.0.0.9", MemberAssignment: garbage},
			},
		},
	}
	listings := []types.ConsumerGroupListing{{GroupID: "g", Type: "consumer", State: "Stable"}}
	var got *types.ConsumerGroups
	require.NotPanics(t, func() {
		got = BuildConsumerGroups(listings, descriptions, map[string]string{"g": "b-1:9092"})
	})
	require.Len(t, got.Details, 1)
	require.Len(t, got.Details[0].Members, 1)
	assert.Equal(t, "m1", got.Details[0].Members[0].MemberID)
	assert.Empty(t, got.Details[0].Members[0].AssignedTopics)
	assert.Equal(t, "consumer", got.Details[0].Type)
	assert.Equal(t, "b-1:9092", got.Details[0].Coordinator)
}

func TestBuildConsumerGroups_EmptyAssignment(t *testing.T) {
	descriptions := []*sarama.GroupDescription{
		{GroupId: "g", State: "Stable", Members: map[string]*sarama.GroupMemberDescription{"m1": {MemberId: "m1", MemberAssignment: nil}}},
	}
	got := BuildConsumerGroups([]types.ConsumerGroupListing{{GroupID: "g", Type: "classic", State: "Stable"}}, descriptions, nil)
	require.Len(t, got.Details, 1)
	require.Len(t, got.Details[0].Members, 1)
	assert.Empty(t, got.Details[0].Members[0].AssignedTopics)
}

func TestBuildConsumerGroups_SkipsNilDescription(t *testing.T) {
	got := BuildConsumerGroups([]types.ConsumerGroupListing{{GroupID: "g", Type: "classic", State: "Stable"}}, []*sarama.GroupDescription{nil}, nil)
	require.Len(t, got.Details, 1)
	assert.Empty(t, got.Details[0].Members)
}
