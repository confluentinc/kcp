package client

import (
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
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

// countingFetcher fails the groups in failing, answers the rest with an empty
// response, and counts the calls it receives. If barrier is set, every call
// waits at it until barrier.n callers have arrived, so a test can hold that many
// fetches in flight at once.
type countingFetcher struct {
	failing map[string]bool
	calls   atomic.Int64
	barrier *sync.WaitGroup
}

func (f *countingFetcher) ListConsumerGroupOffsets(group string, _ map[string][]int32) (*sarama.OffsetFetchResponse, error) {
	f.calls.Add(1)
	if f.barrier != nil {
		f.barrier.Done()
		f.barrier.Wait()
	}
	if f.failing[group] {
		return nil, fmt.Errorf("denied for %s", group)
	}
	return offsetsResponse(nil), nil
}

func TestCommittedTopics_StopsFetchingAfterAFailure(t *testing.T) {
	f := &countingFetcher{failing: map[string]bool{"g000": true}}
	var groups []string
	for i := 0; i < 200; i++ {
		groups = append(groups, fmt.Sprintf("g%03d", i))
	}
	_, err := committedTopics(f, groups, 1)
	require.Error(t, err)
	assert.Less(t, f.calls.Load(), int64(10), "a failed group means the run fails; it must not go on to fetch the other ~200 groups")
}

func TestCommittedTopics_ReportsTheLowestFailedGroupWhenSeveralFail(t *testing.T) {
	// Hold all three fetches in flight so they fail together, then check the error
	// does not depend on which worker happened to finish first.
	for i := 0; i < 20; i++ {
		barrier := &sync.WaitGroup{}
		barrier.Add(3)
		f := &countingFetcher{failing: map[string]bool{"g3": true, "g1": true, "g2": true}, barrier: barrier}
		_, err := committedTopics(f, []string{"g3", "g1", "g2"}, 3)
		require.Error(t, err)
		require.Contains(t, err.Error(), "g1", "run %d: want the error for the lowest-named failed group", i)
	}
}

func TestCommittedTopics_NoGroups(t *testing.T) {
	got, err := committedTopics(fakeOffsetFetcher{}, nil, 4)
	require.NoError(t, err)
	assert.Empty(t, got)
}

// fakeGroupDescriber answers DescribeConsumerGroups from a canned result and records what it was asked.
type fakeGroupDescriber struct {
	descs []*sarama.GroupDescription
	err   error
	asked [][]string
}

func (f *fakeGroupDescriber) DescribeConsumerGroups(groups []string) ([]*sarama.GroupDescription, error) {
	f.asked = append(f.asked, groups)
	return f.descs, f.err
}

func TestCanDescribeAnyGroup_AllowedWhenTheBrokerAnswersDead(t *testing.T) {
	// A group that does not exist is answered as state "Dead", with no error, to a principal that may describe it.
	f := &fakeGroupDescriber{descs: []*sarama.GroupDescription{{GroupId: "probe", State: "Dead"}}}
	got, err := canDescribeAnyGroup(f, "probe")
	require.NoError(t, err)
	assert.True(t, got)
	assert.Equal(t, [][]string{{"probe"}}, f.asked, "must ask about exactly the probe group")
}

func TestCanDescribeAnyGroup_DeniedByTheDescription(t *testing.T) {
	f := &fakeGroupDescriber{descs: []*sarama.GroupDescription{{GroupId: "probe", Err: sarama.ErrGroupAuthorizationFailed}}}
	got, err := canDescribeAnyGroup(f, "probe")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestCanDescribeAnyGroup_DeniedByTheCoordinatorLookup(t *testing.T) {
	// FindCoordinator authorizes DESCRIBE on the group too, so the denial can arrive as the call's own error.
	f := &fakeGroupDescriber{err: fmt.Errorf("looking up the coordinator: %w", sarama.ErrGroupAuthorizationFailed)}
	got, err := canDescribeAnyGroup(f, "probe")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestCanDescribeAnyGroup_OtherFailuresAreErrorsNotRefusals(t *testing.T) {
	for name, f := range map[string]*fakeGroupDescriber{
		"call error":        {err: errors.New("connection reset")},
		"description error": {descs: []*sarama.GroupDescription{{GroupId: "probe", Err: sarama.ErrNotCoordinatorForConsumer}}},
		"no answer":         {},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := canDescribeAnyGroup(f, "probe")
			require.Error(t, err, "a failure to ask must be an error, never read as allowed or denied")
		})
	}
	_, err := canDescribeAnyGroup(&fakeGroupDescriber{descs: []*sarama.GroupDescription{{Err: sarama.ErrNotCoordinatorForConsumer}}}, "probe")
	require.ErrorIs(t, err, sarama.ErrNotCoordinatorForConsumer)
}

// newClientAgainstMockBroker returns a real ConsumerGroupClient (pinned at 3.8, like production) talking to a
// mock broker, so the probe is exercised through sarama's own coordinator lookup and request encoding rather
// than a fake of it. handlers supplies the group-related responses; ApiVersions and Metadata are filled in.
func newClientAgainstMockBroker(t *testing.T, handlers func(mb *sarama.MockBroker) map[string]sarama.MockResponse) *ConsumerGroupClient {
	t.Helper()
	mb := sarama.NewMockBroker(t, 1)
	t.Cleanup(mb.Close)
	all := map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys([]sarama.ApiVersionsResponseKey{
			{ApiKey: 18, MinVersion: 0, MaxVersion: 3}, // ApiVersions
			{ApiKey: 3, MinVersion: 0, MaxVersion: 10}, // Metadata
			{ApiKey: 10, MinVersion: 0, MaxVersion: 4}, // FindCoordinator
			{ApiKey: 15, MinVersion: 0, MaxVersion: 5}, // DescribeGroups
		}),
		"MetadataRequest": sarama.NewMockMetadataResponse(t).SetBroker(mb.Addr(), mb.BrokerID()).SetController(mb.BrokerID()),
	}
	for k, v := range handlers(mb) {
		all[k] = v
	}
	mb.SetHandlerByMap(all)
	cfg := sarama.NewConfig()
	cfg.Version = sarama.V3_8_0_0
	c, err := sarama.NewClient([]string{mb.Addr()}, cfg)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	admin, err := sarama.NewClusterAdminFromClient(c)
	require.NoError(t, err)
	return &ConsumerGroupClient{client: c, admin: admin}
}

func TestCanDescribeAnyGroup_ThroughARealClient(t *testing.T) {
	const probe = "__kcp_probe_group_test"
	for _, tc := range []struct {
		name     string
		handlers func(mb *sarama.MockBroker) map[string]sarama.MockResponse
		want     bool
	}{
		{"allowed: coordinator found, group answered Dead", func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
			return map[string]sarama.MockResponse{
				"FindCoordinatorRequest": sarama.NewMockFindCoordinatorResponse(t).SetCoordinator(sarama.CoordinatorGroup, probe, mb),
				"DescribeGroupsRequest":  sarama.NewMockDescribeGroupsResponse(t),
			}
		}, true},
		{"denied at the coordinator lookup", func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
			return map[string]sarama.MockResponse{
				"FindCoordinatorRequest": sarama.NewMockFindCoordinatorResponse(t).SetError(sarama.CoordinatorGroup, probe, sarama.ErrGroupAuthorizationFailed),
			}
		}, false},
		{"denied in the group description", func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
			return map[string]sarama.MockResponse{
				"FindCoordinatorRequest": sarama.NewMockFindCoordinatorResponse(t).SetCoordinator(sarama.CoordinatorGroup, probe, mb),
				"DescribeGroupsRequest": sarama.NewMockDescribeGroupsResponse(t).
					// sarama encodes ErrorCode and derives Err from it on decode.
					AddGroupDescription(probe, &sarama.GroupDescription{GroupId: probe, ErrorCode: int16(sarama.ErrGroupAuthorizationFailed)}),
			}
		}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClientAgainstMockBroker(t, tc.handlers)

			got, err := canDescribeAnyGroup(c.admin, probe)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}
