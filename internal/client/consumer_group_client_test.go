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

// During a coordinator move the old and new coordinator can both report a group.
// The merge must keep the most conservative state regardless of broker order:
// any state that is not Empty/Dead (active, or unknown/"") beats Empty, which
// beats Dead (compared case-insensitively). Applies in both strict and
// best-effort modes.
func TestMergeListings_DuplicateGroupKeepsMostConservativeState(t *testing.T) {
	cases := []struct {
		name          string
		first, second sarama.GroupData
		want          sarama.GroupData
	}{
		{"dead then stable", sarama.GroupData{GroupState: "Dead"}, sarama.GroupData{GroupState: "Stable", GroupType: "classic"}, sarama.GroupData{GroupState: "Stable", GroupType: "classic"}},
		{"stable then dead", sarama.GroupData{GroupState: "Stable", GroupType: "classic"}, sarama.GroupData{GroupState: "Dead"}, sarama.GroupData{GroupState: "Stable", GroupType: "classic"}},
		{"empty then reconciling", sarama.GroupData{GroupState: "Empty"}, sarama.GroupData{GroupState: "Reconciling", GroupType: "consumer"}, sarama.GroupData{GroupState: "Reconciling", GroupType: "consumer"}},
		{"reconciling then empty", sarama.GroupData{GroupState: "Reconciling", GroupType: "consumer"}, sarama.GroupData{GroupState: "Empty"}, sarama.GroupData{GroupState: "Reconciling", GroupType: "consumer"}},
		{"unknown then empty", sarama.GroupData{GroupState: ""}, sarama.GroupData{GroupState: "Empty"}, sarama.GroupData{GroupState: ""}},
		{"empty then unknown", sarama.GroupData{GroupState: "Empty"}, sarama.GroupData{GroupState: ""}, sarama.GroupData{GroupState: ""}},
		{"dead then empty, mixed case", sarama.GroupData{GroupState: "DEAD"}, sarama.GroupData{GroupState: "empty"}, sarama.GroupData{GroupState: "empty"}},
	}
	for _, tc := range cases {
		for _, strict := range []bool{true, false} {
			t.Run(fmt.Sprintf("%s/strict=%v", tc.name, strict), func(t *testing.T) {
				got, err := mergeListings([]brokerListing{
					{addr: "b1:9092", resp: groupsResponse(map[string]sarama.GroupData{"g": tc.first})},
					{addr: "b2:9092", resp: groupsResponse(map[string]sarama.GroupData{"g": tc.second})},
				}, strict)
				require.NoError(t, err)
				assert.Equal(t, []types.ConsumerGroupListing{{GroupID: "g", Type: tc.want.GroupType, State: tc.want.GroupState}}, got)
			})
		}
	}
}

// A broker result with neither a response nor an error is a failed listing in
// strict mode, not a broker to skip silently.
func TestMergeListings_StrictFailsOnNilResponse(t *testing.T) {
	_, err := mergeListings([]brokerListing{
		{addr: "b1:9092", resp: groupsResponse(map[string]sarama.GroupData{"g1": {GroupState: "Stable"}})},
		{addr: "b2:9092"},
	}, true)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "b2:9092")
}

// A group listed without a GroupsData entry (brokers below ListGroups v4 report
// no state) comes back with an empty, unknown State in strict mode.
func TestMergeListings_StrictGroupWithoutDataHasUnknownState(t *testing.T) {
	resp := &sarama.ListGroupsResponse{Err: sarama.ErrNoError, Groups: map[string]string{"g": "consumer"}}
	got, err := mergeListings([]brokerListing{{addr: "b1:9092", resp: resp}}, true)
	require.NoError(t, err)
	assert.Equal(t, []types.ConsumerGroupListing{{GroupID: "g"}}, got)
}
