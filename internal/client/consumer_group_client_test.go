package client

import (
	"errors"
	"fmt"
	"reflect"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// newClientAndMockBroker returns a real ConsumerGroupClient (pinned at 3.8, like production) talking to a
// mock broker, so the probe is exercised through sarama's own coordinator lookup and request encoding rather
// than a fake of it. handlers supplies the group-related responses; ApiVersions and Metadata are filled in.
func newClientAndMockBroker(t *testing.T, metadataMax int16, handlers func(mb *sarama.MockBroker) map[string]sarama.MockResponse) (*ConsumerGroupClient, *sarama.MockBroker) {
	t.Helper()
	mb := sarama.NewMockBroker(t, 1)
	t.Cleanup(mb.Close)
	all := map[string]sarama.MockResponse{
		"ApiVersionsRequest": sarama.NewMockApiVersionsResponse(t).SetApiKeys([]sarama.ApiVersionsResponseKey{
			{ApiKey: 18, MinVersion: 0, MaxVersion: 3},          // ApiVersions
			{ApiKey: 3, MinVersion: 0, MaxVersion: metadataMax}, // Metadata
			{ApiKey: 10, MinVersion: 0, MaxVersion: 4},          // FindCoordinator
			{ApiKey: 15, MinVersion: 0, MaxVersion: 5},          // DescribeGroups
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
	return &ConsumerGroupClient{client: c, admin: admin}, mb
}

// newClientAgainstMockBroker is newClientAndMockBroker for tests that do not inspect the broker.
func newClientAgainstMockBroker(t *testing.T, metadataMax int16, handlers func(mb *sarama.MockBroker) map[string]sarama.MockResponse) *ConsumerGroupClient {
	t.Helper()
	c, _ := newClientAndMockBroker(t, metadataMax, handlers)
	return c
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
			c := newClientAgainstMockBroker(t, 10, tc.handlers)

			got, err := canDescribeAnyGroup(c.admin, probe)

			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

// fakeTopicMetadata answers GetMetadata with a canned response and records the requests.
type fakeTopicMetadata struct {
	resp *sarama.MetadataResponse
	err  error
	reqs []*sarama.MetadataRequest
}

func (f *fakeTopicMetadata) GetMetadata(r *sarama.MetadataRequest) (*sarama.MetadataResponse, error) {
	f.reqs = append(f.reqs, r)
	return f.resp, f.err
}

func topicMetadata(version int16, name string, kerr sarama.KError) *sarama.MetadataResponse {
	return &sarama.MetadataResponse{Version: version, Topics: []*sarama.TopicMetadata{{Name: name, Err: kerr}}}
}

func TestCanDescribeAnyTopic_AllowedWhenTheBrokerSaysTheTopicDoesNotExist(t *testing.T) {
	// The broker authorizes DESCRIBE on the name first: a principal that may describe it is told the topic
	// does not exist, one that may not is told it is not authorized.
	f := &fakeTopicMetadata{resp: topicMetadata(10, "probe", sarama.ErrUnknownTopicOrPartition)}
	got, err := canDescribeAnyTopic(f, "probe")
	require.NoError(t, err)
	assert.True(t, got)
	require.Len(t, f.reqs, 1)
	assert.Equal(t, []string{"probe"}, f.reqs[0].Topics, "must ask about exactly the probe topic, never all topics")
	assert.False(t, f.reqs[0].AllowAutoTopicCreation, "the probe must never be allowed to create the topic")
}

func TestCanDescribeAnyTopic_AllowedWhenTheTopicExistsToo(t *testing.T) {
	got, err := canDescribeAnyTopic(&fakeTopicMetadata{resp: topicMetadata(10, "probe", sarama.ErrNoError)}, "probe")
	require.NoError(t, err)
	assert.True(t, got)
}

func TestCanDescribeAnyTopic_DeniedWhenNotAuthorized(t *testing.T) {
	got, err := canDescribeAnyTopic(&fakeTopicMetadata{resp: topicMetadata(10, "probe", sarama.ErrTopicAuthorizationFailed)}, "probe")
	require.NoError(t, err)
	assert.False(t, got)
}

func TestCanDescribeAnyTopic_OtherFailuresAreErrorsNotRefusals(t *testing.T) {
	for name, f := range map[string]*fakeTopicMetadata{
		"call error":        {err: errors.New("connection reset")},
		"other topic error": {resp: topicMetadata(10, "probe", sarama.ErrLeaderNotAvailable)},
		"no topic back":     {resp: &sarama.MetadataResponse{Version: 10}},
		"different topic":   {resp: topicMetadata(10, "something-else", sarama.ErrUnknownTopicOrPartition)},
		// Below Metadata v4 the request has no auto-create flag, so a broker with
		// auto.create.topics.enable would create the probe topic. Never read that as an answer.
		"pre-v4 response": {resp: topicMetadata(3, "probe", sarama.ErrUnknownTopicOrPartition)},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := canDescribeAnyTopic(f, "probe")
			require.Error(t, err, "a probe that could not be asked safely is neither allowed nor denied")
		})
	}
}

func TestCanDescribeAnyTopic_ThroughARealClient(t *testing.T) {
	const probe = "__kcp_probe_topic_test"
	base := func(mb *sarama.MockBroker) *sarama.MockMetadataResponse {
		return sarama.NewMockMetadataResponse(t).SetBroker(mb.Addr(), mb.BrokerID()).SetController(mb.BrokerID())
	}
	for _, tc := range []struct {
		name        string
		metadataMax int16
		handlers    func(mb *sarama.MockBroker) map[string]sarama.MockResponse
		want        bool
		wantErr     bool
	}{
		// MockMetadataResponse answers an unknown topic with UNKNOWN_TOPIC_OR_PARTITION, as a real broker does
		// for a principal that may describe the name.
		{"allowed", 10, func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
			return map[string]sarama.MockResponse{"MetadataRequest": base(mb)}
		}, true, false},
		{"denied", 10, func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
			return map[string]sarama.MockResponse{"MetadataRequest": base(mb).SetError(probe, sarama.ErrTopicAuthorizationFailed)}
		}, false, false},
		// sarama clamps the request to the broker's advertised Metadata range; a v3 broker cannot be probed safely.
		{"broker advertising only Metadata v3 is an error", 3, func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
			return map[string]sarama.MockResponse{"MetadataRequest": base(mb)}
		}, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := newClientAgainstMockBroker(t, tc.metadataMax, tc.handlers)

			got, err := c.canDescribeAnyTopicNamed(probe)

			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tc.want, got)
		})
	}
}

func topicWithPartitions(name string, n int) *sarama.TopicMetadata {
	return &sarama.TopicMetadata{Name: name, Err: sarama.ErrNoError, Partitions: make([]*sarama.PartitionMetadata, n)}
}

func TestPartitionCounts_CountsExistingTopicsAndOmitsMissingOnes(t *testing.T) {
	f := &fakeTopicMetadata{resp: &sarama.MetadataResponse{Version: 10, Topics: []*sarama.TopicMetadata{
		topicWithPartitions("orders", 3),
		{Name: "gone", Err: sarama.ErrUnknownTopicOrPartition},
	}}}

	got, err := partitionCounts(f, []string{"orders", "gone"})

	require.NoError(t, err)
	assert.Equal(t, map[string]int{"orders": 3}, got, "a topic the broker says doesn't exist has no count, and is not an error")
	require.Len(t, f.reqs, 1)
	assert.Equal(t, []string{"orders", "gone"}, f.reqs[0].Topics, "must ask about exactly the named topics, never all topics")
	assert.False(t, f.reqs[0].AllowAutoTopicCreation, "reading counts must never create a missing topic")
}

func TestPartitionCounts_NoTopicsSendsNoRequest(t *testing.T) {
	f := &fakeTopicMetadata{}
	got, err := partitionCounts(f, nil)
	require.NoError(t, err)
	assert.Empty(t, got)
	assert.Empty(t, f.reqs, "an empty topic list in a Metadata request means every topic on some versions; never send one")
}

func TestPartitionCounts_FailuresAreErrors(t *testing.T) {
	for name, f := range map[string]*fakeTopicMetadata{
		"call error":       {err: errors.New("connection reset")},
		"not authorized":   {resp: &sarama.MetadataResponse{Version: 10, Topics: []*sarama.TopicMetadata{{Name: "orders", Err: sarama.ErrTopicAuthorizationFailed}}}},
		"no answer for it": {resp: &sarama.MetadataResponse{Version: 10}},
		// Below v4 the request has no auto-create flag, so a broker with
		// auto.create.topics.enable could have created the topic.
		"pre-v4 response": {resp: &sarama.MetadataResponse{Version: 3, Topics: []*sarama.TopicMetadata{topicWithPartitions("orders", 3)}}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := partitionCounts(f, []string{"orders"})
			require.Error(t, err)
		})
	}
}

func TestCommittedOffsetsOf_ReturnsOnlyCommittedOffsetsPerPartition(t *testing.T) {
	f := fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{
		"app": offsetsResponse(map[string]map[int32]int64{
			"orders":          {0: 5, 1: -1, 2: 0},
			"subscribed-only": {0: -1},
		}),
		"idle": offsetsResponse(nil),
	}}

	got, err := committedOffsetsOf(f, "app")
	require.NoError(t, err)
	assert.Equal(t, map[string]map[int32]int64{"orders": {0: 5, 2: 0}}, got,
		"a partition at -1 has no commit; a topic with none is absent; offset 0 is a real commit")

	got, err = committedOffsetsOf(f, "idle")
	require.NoError(t, err)
	assert.Empty(t, got)
}

func TestCommittedOffsetsOf_ErrorsAreErrors(t *testing.T) {
	blockErr := &sarama.OffsetFetchResponse{Blocks: map[string]map[int32]*sarama.OffsetFetchResponseBlock{}}
	blockErr.AddBlock("orders", 0, &sarama.OffsetFetchResponseBlock{Offset: -1, Err: sarama.ErrTopicAuthorizationFailed})

	_, err := committedOffsetsOf(fakeOffsetFetcher{errs: map[string]error{"g": errors.New("boom")}}, "g")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "g")

	_, err = committedOffsetsOf(fakeOffsetFetcher{resp: map[string]*sarama.OffsetFetchResponse{"g": blockErr}}, "g")
	require.ErrorIs(t, err, sarama.ErrTopicAuthorizationFailed)
}

// leaderEpochOf reads the leader epoch sarama will encode for a block. OffsetCommitRequest has no accessor for
// it and AddBlock silently uses 0, so the unexported field is read by reflection.
func leaderEpochOf(t *testing.T, req *sarama.OffsetCommitRequest, topic string, partition int32) int32 {
	t.Helper()
	blocks := reflect.ValueOf(req).Elem().FieldByName("blocks")
	require.True(t, blocks.IsValid(), "sarama's OffsetCommitRequest no longer has a blocks field; update this helper")
	b := blocks.MapIndex(reflect.ValueOf(topic))
	require.True(t, b.IsValid(), "no block for topic %s", topic)
	b = b.MapIndex(reflect.ValueOf(partition))
	require.True(t, b.IsValid(), "no block for %s/%d", topic, partition)
	f := b.Elem().FieldByName("committedLeaderEpoch")
	require.True(t, f.IsValid(), "sarama's offsetCommitRequestBlock no longer has a committedLeaderEpoch field; update this helper")
	return int32(f.Int())
}

func TestBuildGroupCommit_IsAnAdminCommitWithEpochMinusOne(t *testing.T) {
	req := buildGroupCommit("g1", map[string]map[int32]int64{
		"orders":   {0: 10, 1: 0},
		"payments": {2: 7},
	})

	assert.Equal(t, int16(7), req.Version, "a bare OffsetCommitRequest is v0 and cannot carry a leader epoch")
	assert.Equal(t, "g1", req.ConsumerGroup)
	assert.Equal(t, int32(sarama.GroupGenerationUndefined), req.ConsumerGroupGeneration, "an admin commit has no generation")
	assert.Empty(t, req.ConsumerID, "an admin commit has no member")

	for topic, parts := range map[string]map[int32]int64{"orders": {0: 10, 1: 0}, "payments": {2: 7}} {
		for part, want := range parts {
			got, _, err := req.Offset(topic, part)
			require.NoError(t, err)
			assert.Equal(t, want, got)
			assert.Equal(t, int32(-1), leaderEpochOf(t, req, topic, part),
				"AddBlock would send epoch 0, which the broker treats as real at v6+; -1 makes a restarting consumer skip truncation detection")
		}
	}
}

// commitHandlers answers FindCoordinator for group g1 with the mock broker and OffsetCommit with commit.
func commitHandlers(t *testing.T, commit sarama.MockResponse) func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
	return func(mb *sarama.MockBroker) map[string]sarama.MockResponse {
		return map[string]sarama.MockResponse{
			"FindCoordinatorRequest": sarama.NewMockFindCoordinatorResponse(t).SetCoordinator(sarama.CoordinatorGroup, "g1", mb),
			"OffsetCommitRequest":    commit,
		}
	}
}

func commitRequests(mb *sarama.MockBroker) []*sarama.OffsetCommitRequest {
	var out []*sarama.OffsetCommitRequest
	for _, rr := range mb.History() {
		if r, ok := rr.Request.(*sarama.OffsetCommitRequest); ok {
			out = append(out, r)
		}
	}
	return out
}

func TestCommitGroupOffsets_SendsOneAdminCommitForTheWholeGroup(t *testing.T) {
	c, mb := newClientAndMockBroker(t, 10, commitHandlers(t, sarama.NewMockOffsetCommitResponse(t)))

	err := c.CommitGroupOffsets("g1", map[string]map[int32]int64{"orders": {0: 10, 1: 4}, "payments": {0: 2}})

	require.NoError(t, err)
	reqs := commitRequests(mb)
	require.Len(t, reqs, 1, "one OffsetCommit per group, never one per partition")
	assert.Equal(t, int16(7), reqs[0].Version)
	assert.Equal(t, "g1", reqs[0].ConsumerGroup)
	assert.Equal(t, int32(sarama.GroupGenerationUndefined), reqs[0].ConsumerGroupGeneration)
	off, _, err := reqs[0].Offset("orders", 1)
	require.NoError(t, err)
	assert.Equal(t, int64(4), off)
	assert.Equal(t, int32(-1), leaderEpochOf(t, reqs[0], "payments", 0))
}

func TestCommitGroupOffsets_APartitionErrorIsAnErrorAndNotRetriedWhenPermanent(t *testing.T) {
	for name, kerr := range map[string]sarama.KError{
		// A destination group with live members: the broker refuses an admin commit. Retrying cannot help.
		"group has members": sarama.ErrUnknownMemberId,
		// The destination credential holds DESCRIBE (plan 1's probes pass) but not READ.
		"no READ on the group": sarama.ErrGroupAuthorizationFailed,
		"no READ on the topic": sarama.ErrTopicAuthorizationFailed,
	} {
		t.Run(name, func(t *testing.T) {
			c, mb := newClientAndMockBroker(t, 10, commitHandlers(t,
				sarama.NewMockOffsetCommitResponse(t).SetError("g1", "orders", 0, kerr)))

			err := c.CommitGroupOffsets("g1", map[string]map[int32]int64{"orders": {0: 10}})

			require.ErrorIs(t, err, kerr)
			assert.Contains(t, err.Error(), "g1")
			assert.Contains(t, err.Error(), "orders")
			assert.Len(t, commitRequests(mb), 1, "a permanent error must not be retried")
		})
	}
}

func TestCommitGroupOffsets_RetriesWhileTheCoordinatorSettles(t *testing.T) {
	old := commitRetryBackoff
	commitRetryBackoff = time.Millisecond
	t.Cleanup(func() { commitRetryBackoff = old })

	loading := sarama.NewMockOffsetCommitResponse(t).SetError("g1", "orders", 0, sarama.ErrOffsetsLoadInProgress)
	c, mb := newClientAndMockBroker(t, 10, commitHandlers(t, sarama.NewMockSequence(loading, sarama.NewMockOffsetCommitResponse(t))))

	require.NoError(t, c.CommitGroupOffsets("g1", map[string]map[int32]int64{"orders": {0: 10}}))
	assert.Len(t, commitRequests(mb), 2, "one retry after COORDINATOR_LOAD_IN_PROGRESS")
}

func TestCommitGroupOffsets_GivesUpAfterTheAttempts(t *testing.T) {
	old := commitRetryBackoff
	commitRetryBackoff = time.Millisecond
	t.Cleanup(func() { commitRetryBackoff = old })

	c, mb := newClientAndMockBroker(t, 10, commitHandlers(t,
		sarama.NewMockOffsetCommitResponse(t).SetError("g1", "orders", 0, sarama.ErrOffsetsLoadInProgress)))

	err := c.CommitGroupOffsets("g1", map[string]map[int32]int64{"orders": {0: 10}})

	require.ErrorIs(t, err, sarama.ErrOffsetsLoadInProgress)
	assert.Len(t, commitRequests(mb), commitAttempts)
}
