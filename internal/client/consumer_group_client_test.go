package client

import (
	"errors"
	"fmt"
	"math"
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

// fakeOffsetFetcher answers ListConsumerGroupOffsets from canned responses and
// insists on an all-topics fetch (nil topicPartitions).
type fakeOffsetFetcher struct {
	resp map[string]*sarama.OffsetFetchResponse
	errs map[string]error
}

func (f fakeOffsetFetcher) ListConsumerGroupOffsets(group string, tp map[string][]int32) (*sarama.OffsetFetchResponse, error) {
	if tp != nil {
		return nil, fmt.Errorf("must fetch every topic, got %v", tp)
	}
	if err := f.errs[group]; err != nil {
		return nil, err
	}
	return f.resp[group], nil
}

// offsetsResponse builds a response from topic -> partition -> committed offset.
func offsetsResponse(blocks map[string]map[int32]int64) *sarama.OffsetFetchResponse {
	r := &sarama.OffsetFetchResponse{Blocks: map[string]map[int32]*sarama.OffsetFetchResponseBlock{}}
	for topic, parts := range blocks {
		for part, off := range parts {
			r.AddBlock(topic, part, &sarama.OffsetFetchResponseBlock{Offset: off})
		}
	}
	return r
}

func TestCommittedTopics_TrackedMeansACommittedOffset(t *testing.T) {
	f := fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{
		"app":  offsetsResponse(map[string]map[int32]int64{"orders": {0: 5, 1: -1}, "payments": {0: 0}, "subscribed-only": {0: -1}}),
		"idle": offsetsResponse(nil),
	}}
	got, err := committedTopics(f, []string{"app", "idle"}, 2)
	require.NoError(t, err)
	assert.Equal(t, map[string][]string{"app": {"orders", "payments"}, "idle": nil}, got)
}

func TestCommittedTopics_ManyGroupsConcurrently(t *testing.T) {
	f := fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{}}
	var groups []string
	for i := 0; i < 50; i++ {
		g := fmt.Sprintf("g%02d", i)
		groups = append(groups, g)
		f.resp[g] = offsetsResponse(map[string]map[int32]int64{"t-" + g: {0: 1}})
	}
	got, err := committedTopics(f, groups, 8)
	require.NoError(t, err)
	require.Len(t, got, 50)
	assert.Equal(t, []string{"t-g07"}, got["g07"])
}

func TestCommittedTopics_AnyGroupFailureIsAnError(t *testing.T) {
	f := fakeOffsetFetcher{
		resp: map[string]*sarama.OffsetFetchResponse{"ok": offsetsResponse(nil)},
		errs: map[string]error{"bad": errors.New("coordinator not available")},
	}
	_, err := committedTopics(f, []string{"ok", "bad"}, 2)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "bad")
}

func TestCommittedTopics_ABlockErrorIsAnError(t *testing.T) {
	r := &sarama.OffsetFetchResponse{Blocks: map[string]map[int32]*sarama.OffsetFetchResponseBlock{}}
	r.AddBlock("orders", 0, &sarama.OffsetFetchResponseBlock{Offset: -1, Err: sarama.ErrTopicAuthorizationFailed})
	_, err := committedTopics(fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{"g": r}}, []string{"g"}, 1)
	require.ErrorIs(t, err, sarama.ErrTopicAuthorizationFailed)
}

func TestCommittedTopics_NoGroups(t *testing.T) {
	got, err := committedTopics(fakeOffsetFetcher{}, nil, 4)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// fakeMetadataFetcher answers GetMetadata per request version and records what
// it was asked.
type fakeMetadataFetcher struct {
	resp map[int16]*sarama.MetadataResponse
	errs map[int16]error
	seen []int16
	incl []bool
}

func (f *fakeMetadataFetcher) GetMetadata(r *sarama.MetadataRequest) (*sarama.MetadataResponse, error) {
	f.seen = append(f.seen, r.Version)
	f.incl = append(f.incl, r.IncludeClusterAuthorizedOperations)
	if err := f.errs[r.Version]; err != nil {
		return nil, err
	}
	return f.resp[r.Version], nil
}

// opsMask builds the authorized-operations bitfield a broker returns: bit n set
// for ACL operation code n.
func opsMask(ops ...sarama.AclOperation) int32 {
	var m int32
	for _, op := range ops {
		m |= 1 << int32(op)
	}
	return m
}

func metadataWithOps(mask int32) *sarama.MetadataResponse {
	return &sarama.MetadataResponse{ClusterAuthorizedOperations: mask}
}

func TestClusterDescribeAccess_Granted(t *testing.T) {
	f := &fakeMetadataFetcher{resp: map[int16]*sarama.MetadataResponse{
		10: metadataWithOps(opsMask(sarama.AclOperationDescribe, sarama.AclOperationAlter)),
	}}
	got, err := clusterDescribeAccess(f)
	require.NoError(t, err)
	assert.Equal(t, DescribeGranted, got)
	assert.Equal(t, []int16{10}, f.seen)
	assert.Equal(t, []bool{true}, f.incl, "must ask the broker for the cluster's authorized operations")
}

func TestClusterDescribeAccess_DeniedWhenTheDescribeBitIsClear(t *testing.T) {
	for name, mask := range map[string]int32{
		"no operations":               0,
		"other operations only":       opsMask(sarama.AclOperationRead, sarama.AclOperationAlter),
		"describe configs not enough": opsMask(sarama.AclOperationDescribeConfigs),
	} {
		t.Run(name, func(t *testing.T) {
			f := &fakeMetadataFetcher{resp: map[int16]*sarama.MetadataResponse{10: metadataWithOps(mask)}}
			got, err := clusterDescribeAccess(f)
			require.NoError(t, err)
			assert.Equal(t, DescribeDenied, got)
		})
	}
}

func TestClusterDescribeAccess_NotReportedIsUnknown(t *testing.T) {
	// Int.MinValue is the schema default a broker leaves when it did not compute
	// the field.
	f := &fakeMetadataFetcher{resp: map[int16]*sarama.MetadataResponse{10: metadataWithOps(math.MinInt32)}}
	got, err := clusterDescribeAccess(f)
	require.NoError(t, err)
	assert.Equal(t, DescribeUnknown, got)
}

func TestClusterDescribeAccess_FallsBackToV8OnAnOlderBroker(t *testing.T) {
	f := &fakeMetadataFetcher{
		errs: map[int16]error{10: sarama.ErrUnsupportedVersion},
		resp: map[int16]*sarama.MetadataResponse{8: metadataWithOps(opsMask(sarama.AclOperationDescribe))},
	}
	got, err := clusterDescribeAccess(f)
	require.NoError(t, err)
	assert.Equal(t, DescribeGranted, got)
	assert.Equal(t, []int16{10, 8}, f.seen)
}

func TestClusterDescribeAccess_NoSupportedVersionIsUnknown(t *testing.T) {
	f := &fakeMetadataFetcher{errs: map[int16]error{10: sarama.ErrUnsupportedVersion, 8: sarama.ErrUnsupportedVersion}}
	got, err := clusterDescribeAccess(f)
	require.NoError(t, err)
	assert.Equal(t, DescribeUnknown, got)
}

func TestClusterDescribeAccess_OtherFailuresAreErrors(t *testing.T) {
	f := &fakeMetadataFetcher{errs: map[int16]error{10: errors.New("connection reset")}}
	_, err := clusterDescribeAccess(f)
	require.Error(t, err)
	assert.Equal(t, []int16{10}, f.seen, "a connection failure must not be retried at another version")
}
