package client

import (
	"errors"
	"fmt"
	"testing"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsUnsupportedListGroupsVersion(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"unsupported_version", sarama.ErrUnsupportedVersion, true},
		{"wrapped_unsupported_version", fmt.Errorf("list groups v5: %w", sarama.ErrUnsupportedVersion), true},
		{"other_kerror", sarama.ErrGroupAuthorizationFailed, false},
		{"generic_error", errors.New("connection reset"), false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, IsUnsupportedListGroupsVersion(tc.err))
		})
	}
}

func groupsResponse(groups map[string]sarama.GroupData) *sarama.ListGroupsResponse {
	resp := &sarama.ListGroupsResponse{Err: sarama.ErrNoError, Groups: map[string]string{}, GroupsData: groups}
	for id := range groups {
		resp.Groups[id] = "consumer"
	}
	return resp
}

func TestMergeListings_StrictFailsOnAnyBrokerError(t *testing.T) {
	results := []brokerListing{
		{addr: "b1:9092", resp: groupsResponse(map[string]sarama.GroupData{"g1": {GroupState: "Stable"}})},
		{addr: "b2:9092", err: errors.New("connection reset")},
	}
	_, err := mergeListings(results, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "b2:9092")
}

func TestMergeListings_StrictFailsOnBrokerRefusal(t *testing.T) {
	refused := &sarama.ListGroupsResponse{Err: sarama.ErrClusterAuthorizationFailed}
	_, err := mergeListings([]brokerListing{{addr: "b1:9092", resp: refused}}, true)
	require.ErrorIs(t, err, sarama.ErrClusterAuthorizationFailed)
}

func TestMergeListings_StrictFailsWithNoBrokers(t *testing.T) {
	_, err := mergeListings(nil, true)
	require.Error(t, err)
}

func TestMergeListings_StrictCarriesStateAndType(t *testing.T) {
	got, err := mergeListings([]brokerListing{
		{addr: "b1:9092", resp: groupsResponse(map[string]sarama.GroupData{"g1": {GroupState: "Empty", GroupType: "classic"}})},
		{addr: "b2:9092", resp: groupsResponse(map[string]sarama.GroupData{"g2": {GroupState: "Reconciling", GroupType: "consumer"}})},
	}, true)
	require.NoError(t, err)
	assert.ElementsMatch(t, []types.ConsumerGroupListing{
		{GroupID: "g1", Type: "classic", State: "Empty"},
		{GroupID: "g2", Type: "consumer", State: "Reconciling"},
	}, got)
}

func TestMergeListings_BestEffortKeepsAnsweringBrokers(t *testing.T) {
	got, err := mergeListings([]brokerListing{
		{addr: "b1:9092", resp: groupsResponse(map[string]sarama.GroupData{"g1": {GroupState: "Stable"}})},
		{addr: "b2:9092", err: errors.New("connection reset")},
	}, false)
	require.NoError(t, err)
	assert.Equal(t, []types.ConsumerGroupListing{{GroupID: "g1", State: "Stable"}}, got)
}

func TestMergeListings_BestEffortFailsWhenNoBrokerAnswers(t *testing.T) {
	_, err := mergeListings([]brokerListing{{addr: "b1:9092", err: errors.New("connection reset")}}, false)
	require.Error(t, err)
}
