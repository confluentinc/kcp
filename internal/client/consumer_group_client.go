package client

import (
	"errors"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/types"
)

// ConsumerGroupScanner is the group-discovery surface the collector depends on
// (kept small so it can be mocked).
type ConsumerGroupScanner interface {
	ListGroupsWithType() ([]types.ConsumerGroupListing, error)
	DescribeGroups(groupIDs []string) ([]*sarama.GroupDescription, error)
	Coordinators(groupIDs []string) map[string]string
	Close() error
}

// ConsumerGroupClient is an isolated Kafka client pinned at 3.8.0 (see
// NewConsumerGroupClient) used only for consumer-group discovery. It does not
// alter the 3.6-era clients used by the rest of scanning (design §4.3).
type ConsumerGroupClient struct {
	client sarama.Client
	admin  sarama.ClusterAdmin
}

var _ ConsumerGroupScanner = (*ConsumerGroupClient)(nil)

// IsUnsupportedListGroupsVersion reports whether a raw ListGroups error means the
// broker is too old for that request version (KIP-848 v5 needs a >= 3.8 broker).
// When true, the caller downgrades the version and retries. Scoped to
// UNSUPPORTED_VERSION so genuine failures (auth, connection) still surface as
// errors (design §6).
func IsUnsupportedListGroupsVersion(err error) bool {
	return errors.Is(err, sarama.ErrUnsupportedVersion)
}

// NewConsumerGroupClient builds an ISOLATED client pinned at Kafka 3.8.0 — the
// version required for KIP-848 ListGroups v5 (the group `type` field). It reuses
// kcp's existing auth options; it does NOT alter the 3.6-era clients used by the
// rest of scanning (design §4.3).
func NewConsumerGroupClient(brokerAddresses []string, region string, opts ...AdminOption) (*ConsumerGroupClient, error) {
	saramaConfig, _, err := buildKafkaClientConfig(region, sarama.V3_8_0_0, opts...)
	if err != nil {
		return nil, err
	}

	c, err := sarama.NewClient(brokerAddresses, saramaConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create consumer-group client: %w", err)
	}

	admin, err := sarama.NewClusterAdminFromClient(c)
	if err != nil {
		_ = c.Close()
		return nil, fmt.Errorf("failed to create consumer-group admin: %w", err)
	}

	return &ConsumerGroupClient{client: c, admin: admin}, nil
}

// ListGroupsWithType lists every consumer group on the cluster along with its
// KIP-848 type, via the raw ListGroups v5 request (falling back to v4 — no type —
// on brokers older than 3.8). No type filter is set, so all group types are
// returned. resp.GroupsData is populated only for v4+, and GroupType only for v5;
// on a v4 fallback Type stays "". Best-effort: a broker that fails is warned
// about and skipped; it errors only when no broker answers.
func (c *ConsumerGroupClient) ListGroupsWithType() ([]types.ConsumerGroupListing, error) {
	return mergeListings(c.listGroupsPerBroker(), false)
}

// ListGroupsAllBrokers is ListGroupsWithType without the tolerance: each broker
// reports only the groups it coordinates, so one silent broker hides its groups,
// and a caller making a safety decision from the listing must not get a partial
// one. It errors if any broker fails or refuses.
func (c *ConsumerGroupClient) ListGroupsAllBrokers() ([]types.ConsumerGroupListing, error) {
	return mergeListings(c.listGroupsPerBroker(), true)
}

// brokerListing is one broker's answer to ListGroups: a response, or the error
// that stopped it.
type brokerListing struct {
	addr string
	resp *sarama.ListGroupsResponse
	err  error
}

// listGroupsPerBroker asks every known broker for the groups it coordinates.
func (c *ConsumerGroupClient) listGroupsPerBroker() []brokerListing {
	var out []brokerListing
	for _, b := range c.client.Brokers() {
		if err := b.Open(c.client.Config()); err != nil && !errors.Is(err, sarama.ErrAlreadyConnected) {
			out = append(out, brokerListing{addr: b.Addr(), err: fmt.Errorf("connecting: %w", err)})
			continue
		}
		resp, err := b.ListGroups(&sarama.ListGroupsRequest{Version: 5})
		if IsUnsupportedListGroupsVersion(err) {
			slog.Debug("⏭️ broker does not support ListGroups v5 (KIP-848 group types); falling back to v4", "broker", b.Addr())
			resp, err = b.ListGroups(&sarama.ListGroupsRequest{Version: 4})
		}
		out = append(out, brokerListing{addr: b.Addr(), resp: resp, err: err})
	}
	return out
}

// mergeListings folds per-broker answers into one de-duplicated listing. A
// broker that answered with an error code (e.g. ErrGroupAuthorizationFailed)
// counts as failed. strict errors on the first failed broker (including one that
// returned neither a response nor an error), and when there are no brokers at
// all; otherwise each failure is warned about and the run errors only if no
// broker answered. A group reported by more than one broker keeps the most
// conservative state (see groupStateRank), in both modes.
func mergeListings(results []brokerListing, strict bool) ([]types.ConsumerGroupListing, error) {
	if strict && len(results) == 0 {
		return nil, fmt.Errorf("no brokers to list consumer groups from")
	}
	var listings []types.ConsumerGroupListing
	seen := map[string]int{} // group id -> index in listings
	var lastErr error
	responded := false
	for _, r := range results {
		err := r.err
		if err == nil && r.resp != nil && r.resp.Err != sarama.ErrNoError {
			err = r.resp.Err
		}
		if err != nil {
			if strict {
				return nil, fmt.Errorf("listing consumer groups on broker %s: %w", r.addr, err)
			}
			slog.Warn("⚠️ failed to list consumer groups on broker; groups it coordinates may be omitted", "broker", r.addr, "error", err)
			lastErr = err
			continue
		}
		if r.resp == nil {
			if strict {
				return nil, fmt.Errorf("listing consumer groups on broker %s: no response", r.addr)
			}
			continue
		}
		responded = true
		for id := range r.resp.Groups {
			l := types.ConsumerGroupListing{GroupID: id}
			if gd, ok := r.resp.GroupsData[id]; ok {
				l.Type = gd.GroupType
				l.State = gd.GroupState
			}
			if i, dup := seen[id]; dup {
				// During a coordinator move the old and the new coordinator
				// can both report the group; keep the more live answer so
				// broker order never hides an active group.
				if groupStateRank(l.State) > groupStateRank(listings[i].State) {
					listings[i] = l
				}
				continue
			}
			seen[id] = len(listings)
			listings = append(listings, l)
		}
	}
	if !responded && lastErr != nil {
		return nil, fmt.Errorf("failed to list consumer groups on all brokers: %w", lastErr)
	}
	return listings, nil
}

// groupStateRank orders a listed group state by how conservatively it must be
// treated when two brokers disagree: Dead (0) < Empty (1) < anything else (2),
// which covers the active states and an unknown/empty state alike — an unknown
// state may be active, so it never loses to Empty or Dead. Case-insensitive.
func groupStateRank(state string) int {
	switch strings.ToLower(state) {
	case "dead":
		return 0
	case "empty":
		return 1
	default:
		return 2
	}
}

// DescribeGroups describes the given consumer groups via the admin client's
// standard DescribeConsumerGroups call.
func (c *ConsumerGroupClient) DescribeGroups(groupIDs []string) ([]*sarama.GroupDescription, error) {
	if len(groupIDs) == 0 {
		return nil, nil
	}
	result, err := c.admin.DescribeConsumerGroups(groupIDs)
	if err != nil {
		return nil, fmt.Errorf("failed to describe consumer groups: %w", err)
	}
	return result, nil
}

// Coordinators resolves each group's coordinator broker address, best-effort —
// it never errors; groups whose coordinator cannot be resolved are simply
// omitted from the result.
func (c *ConsumerGroupClient) Coordinators(groupIDs []string) map[string]string {
	m := make(map[string]string, len(groupIDs))
	for _, id := range groupIDs {
		if b, err := c.client.Coordinator(id); err == nil && b != nil {
			m[id] = b.Addr()
		}
	}
	return m
}

// Close closes the underlying client. The admin was created via
// NewClusterAdminFromClient and shares this client, so it is NOT closed
// separately here — that would double-close it.
func (c *ConsumerGroupClient) Close() error {
	return c.client.Close()
}

// groupOffsetFetcher is the slice of sarama.ClusterAdmin CommittedTopics needs.
type groupOffsetFetcher interface {
	ListConsumerGroupOffsets(group string, topicPartitions map[string][]int32) (*sarama.OffsetFetchResponse, error)
}

// committedTopicsWorkers is the fixed fetch concurrency. The cost of one
// OffsetFetch per group is unmeasured; making this configurable is future work.
const committedTopicsWorkers = 8

// CommittedTopics returns, for each group, the sorted topics it has committed an
// offset on. A conversion checks only these topics, so a failure to read any
// group is an error: skipping it would leave its topics unverified.
func (c *ConsumerGroupClient) CommittedTopics(groups []string) (map[string][]string, error) {
	return committedTopics(c.admin, groups, committedTopicsWorkers)
}

func committedTopics(f groupOffsetFetcher, groups []string, workers int) (map[string][]string, error) {
	if workers < 1 {
		workers = 1
	}
	type result struct {
		group  string
		topics []string
		err    error
	}
	jobs := make(chan string)
	results := make(chan result, len(groups))
	// failed stops the run once any group fails: the call returns an error
	// either way, so fetching the rest of a large cluster's groups is wasted.
	var failed atomic.Bool
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for g := range jobs {
				if failed.Load() {
					continue
				}
				topics, err := committedTopicsOf(f, g)
				if err != nil {
					failed.Store(true)
				}
				results <- result{group: g, topics: topics, err: err}
			}
		}()
	}
	go func() {
		for _, g := range groups {
			if failed.Load() {
				break
			}
			jobs <- g
		}
		close(jobs)
		wg.Wait()
		close(results)
	}()

	out := make(map[string][]string, len(groups))
	var firstGroup string
	var firstErr error
	for r := range results {
		if r.err != nil {
			// Several fetches can fail at once; report the lowest-named group so
			// the error does not depend on which worker finished first.
			if firstErr == nil || r.group < firstGroup {
				firstGroup, firstErr = r.group, r.err
			}
			continue
		}
		out[r.group] = r.topics
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return out, nil
}

// CommittedOffset is one partition's committed position and the metadata string the committing client
// stored with it (Kafka Streams keeps per-partition stream time there). Both are carried to the destination.
type CommittedOffset struct {
	Offset   int64
	Metadata string
}

// committedOffsetsOf fetches every committed offset of one group, with its metadata: topic -> partition ->
// committed offset. A partition counts only if its committed offset is >= 0 (-1 means a subscription with no
// commit), so a topic with no commit is absent. Any block-level error fails the whole fetch.
func committedOffsetsOf(f groupOffsetFetcher, group string) (map[string]map[int32]CommittedOffset, error) {
	resp, err := f.ListConsumerGroupOffsets(group, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching committed offsets for consumer group %s: %w", group, err)
	}
	out := map[string]map[int32]CommittedOffset{}
	for topic, parts := range resp.Blocks {
		for part, b := range parts {
			if b.Err != sarama.ErrNoError {
				return nil, fmt.Errorf("fetching committed offset for consumer group %s, topic %s partition %d: %w", group, topic, part, b.Err)
			}
			if b.Offset >= 0 {
				if out[topic] == nil {
					out[topic] = map[int32]CommittedOffset{}
				}
				out[topic][part] = CommittedOffset{Offset: b.Offset, Metadata: b.Metadata}
			}
		}
	}
	return out, nil
}

// committedTopicsOf is the topics of committedOffsetsOf, sorted.
func committedTopicsOf(f groupOffsetFetcher, group string) ([]string, error) {
	offsets, err := committedOffsetsOf(f, group)
	if err != nil {
		return nil, err
	}
	var topics []string
	for topic := range offsets {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	return topics, nil
}

// CommittedOffsets returns one group's committed offsets and their metadata, topic -> partition -> committed
// offset, offsets >= 0 only.
func (c *ConsumerGroupClient) CommittedOffsets(group string) (map[string]map[int32]CommittedOffset, error) {
	return committedOffsetsOf(c.admin, group)
}

// commitAttempts is how many times CommitGroupOffsets asks before giving up on a coordinator that is moving
// or still loading. commitRetryBackoff is the wait between attempts; it is a var so tests can shorten it.
const commitAttempts = 3

var commitRetryBackoff = 200 * time.Millisecond

// buildGroupCommit builds the admin commit for one group: OffsetCommit v7 (a bare request is v0), generation
// -1 and an empty member id, so no group membership is needed or created. Every block carries leader epoch -1
// ("unknown"), which makes a consumer that later joins skip truncation detection on its first fetch.
// AddBlock would send epoch 0 instead, which the broker treats as a real epoch at v6+. Each block carries the
// source commit's metadata unchanged.
func buildGroupCommit(group string, offsets map[string]map[int32]CommittedOffset) *sarama.OffsetCommitRequest {
	req := &sarama.OffsetCommitRequest{
		Version:                 7,
		ConsumerGroup:           group,
		ConsumerGroupGeneration: sarama.GroupGenerationUndefined,
	}
	for topic, parts := range offsets {
		for part, c := range parts {
			req.AddBlockWithLeaderEpoch(topic, part, c.Offset, -1, sarama.ReceiveTime, c.Metadata)
		}
	}
	return req
}

// retriableCommit reports whether a commit failed only because the group's coordinator moved or is loading.
func retriableCommit(err error) bool {
	return errors.Is(err, sarama.ErrOffsetsLoadInProgress) ||
		errors.Is(err, sarama.ErrConsumerCoordinatorNotAvailable) ||
		errors.Is(err, sarama.ErrNotCoordinatorForConsumer)
}

// firstCommitError returns the error for the lowest (topic, partition) the broker rejected, so the message
// does not depend on map order. It returns a nil error when every partition was accepted.
func firstCommitError(resp *sarama.OffsetCommitResponse) (string, int32, error) {
	var topics []string
	for topic := range resp.Errors {
		topics = append(topics, topic)
	}
	sort.Strings(topics)
	for _, topic := range topics {
		var parts []int32
		for part := range resp.Errors[topic] {
			parts = append(parts, part)
		}
		sort.Slice(parts, func(i, j int) bool { return parts[i] < parts[j] })
		for _, part := range parts {
			if kerr := resp.Errors[topic][part]; kerr != sarama.ErrNoError {
				return topic, part, kerr
			}
		}
	}
	return "", 0, nil
}

// CommitGroupOffsets writes one group's offsets to this cluster as a single admin commit (see
// buildGroupCommit). A coordinator that moved or is still loading is retried; anything else, including a
// group that has live members (UNKNOWN_MEMBER_ID) or a credential without READ on the group or topic, is an
// error naming the group, topic and partition, and is not retried.
func (c *ConsumerGroupClient) CommitGroupOffsets(group string, offsets map[string]map[int32]CommittedOffset) error {
	var lastErr error
	for attempt := 0; attempt < commitAttempts; attempt++ {
		if attempt > 0 {
			time.Sleep(commitRetryBackoff)
			_ = c.client.RefreshCoordinator(group)
		}
		coordinator, err := c.client.Coordinator(group)
		if err != nil {
			lastErr = fmt.Errorf("finding the coordinator of consumer group %s: %w", group, err)
			if retriableCommit(err) {
				continue
			}
			return lastErr
		}
		resp, err := coordinator.CommitOffset(buildGroupCommit(group, offsets))
		if err != nil {
			return fmt.Errorf("committing offsets for consumer group %s: %w", group, err)
		}
		if topic, part, kerr := firstCommitError(resp); kerr != nil {
			lastErr = fmt.Errorf("committing offset for consumer group %s, topic %s partition %d: %w", group, topic, part, kerr)
			if retriableCommit(kerr) {
				continue
			}
			return lastErr
		}
		return nil
	}
	return lastErr
}

// groupDescriber is the slice of sarama.ClusterAdmin the group-describe probe needs.
type groupDescriber interface {
	DescribeConsumerGroups(groups []string) ([]*sarama.GroupDescription, error)
}

// CanDescribeAnyGroup reports whether the connected principal may describe an arbitrary consumer group,
// by asking about a group that does not exist. A credential that may not describe groups gets a
// silently filtered ListGroups (no error) and cannot read the offsets of the groups it can't see, so a
// conversion that trusted its listing would verify only part of the cluster.
//
// The broker authorizes DESCRIBE on the group name before saying anything about it: a principal that may
// describe it is told the group is "Dead" (it does not exist), one that may not is told
// GROUP_AUTHORIZATION_FAILED. This tests what actually matters on every authorizer seen so far. Cluster
// DESCRIBE does not: on open-source Kafka it shows every group but not their offsets, and on MSK IAM it
// neither implies nor is needed for a complete listing, so a "cluster DESCRIBE" probe can pass while the
// listing is empty.
func (c *ConsumerGroupClient) CanDescribeAnyGroup() (bool, error) {
	return canDescribeAnyGroup(c.admin, fmt.Sprintf("__kcp_probe_group_%x", time.Now().UnixNano()))
}

// canDescribeAnyGroup asks DescribeGroups about group, which must not exist. The denial can arrive as the
// call's own error (the coordinator lookup authorizes the group too) or in the group's description. Any
// other failure is an error: a probe that could not be asked is neither allowed nor denied.
func canDescribeAnyGroup(f groupDescriber, group string) (bool, error) {
	descs, err := f.DescribeConsumerGroups([]string{group})
	if errors.Is(err, sarama.ErrGroupAuthorizationFailed) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("probing group describe access: %w", err)
	}
	if len(descs) == 0 {
		return false, fmt.Errorf("probing group describe access: the broker returned no description for %q", group)
	}
	for _, d := range descs {
		switch {
		case d.Err == sarama.ErrGroupAuthorizationFailed:
			return false, nil
		case d.Err != sarama.ErrNoError:
			return false, fmt.Errorf("probing group describe access: %w", d.Err)
		}
	}
	return true, nil
}

// metadataFetcher is the slice of *sarama.Broker the topic-describe probe needs.
type metadataFetcher interface {
	GetMetadata(request *sarama.MetadataRequest) (*sarama.MetadataResponse, error)
}

// CanDescribeAnyTopic reports whether the connected principal may describe an arbitrary topic, by asking
// about one that does not exist. A credential that may not gets a silently filtered topic list and, worse,
// an all-topics OffsetFetch that quietly leaves out the topics it can't see: a group's commit on such a
// topic never appears, so the conversion would treat the topic as untracked and never verify it.
//
// The broker authorizes DESCRIBE on the topic name before saying whether it exists: a principal that may
// describe the name is told UNKNOWN_TOPIC_OR_PARTITION, one that may not is told TOPIC_AUTHORIZATION_FAILED.
// The group probe cannot see this: a credential that may describe every group can still be unable to
// describe some topics.
func (c *ConsumerGroupClient) CanDescribeAnyTopic() (bool, error) {
	return c.canDescribeAnyTopicNamed(fmt.Sprintf("__kcp_probe_topic_%x", time.Now().UnixNano()))
}

// canDescribeAnyTopicNamed asks one broker directly. It deliberately does not go through the client's
// metadata refresh, whose sarama default allows auto-creating a missing topic.
func (c *ConsumerGroupClient) canDescribeAnyTopicNamed(topic string) (bool, error) {
	b := c.client.LeastLoadedBroker()
	if b == nil {
		return false, fmt.Errorf("no broker available to probe topic describe access")
	}
	if err := b.Open(c.client.Config()); err != nil && !errors.Is(err, sarama.ErrAlreadyConnected) {
		return false, fmt.Errorf("connecting to %s to probe topic describe access: %w", b.Addr(), err)
	}
	return canDescribeAnyTopic(b, topic)
}

// canDescribeAnyTopic requests metadata for exactly topic, which must not exist, with auto-creation off.
// Any answer other than "unknown" (allowed), "not authorized" (denied) or "exists" (allowed) is an error: a
// probe that could not be asked is neither. A response below Metadata v4 is an error too: sarama clamps
// the request to the broker's advertised range, and before v4 there is no auto-create flag, so a broker
// with auto.create.topics.enable would create the probe topic.
func canDescribeAnyTopic(f metadataFetcher, topic string) (bool, error) {
	resp, err := f.GetMetadata(&sarama.MetadataRequest{
		Version:                10,
		Topics:                 []string{topic},
		AllowAutoTopicCreation: false,
	})
	if err != nil {
		return false, fmt.Errorf("probing topic describe access: %w", err)
	}
	if resp.Version < 4 {
		return false, fmt.Errorf("probing topic describe access: the broker only speaks Metadata v%d, which cannot be told not to auto-create the probe topic", resp.Version)
	}
	if len(resp.Topics) != 1 || resp.Topics[0].Name != topic {
		return false, fmt.Errorf("probing topic describe access: the broker did not answer for %q", topic)
	}
	switch resp.Topics[0].Err {
	case sarama.ErrUnknownTopicOrPartition, sarama.ErrNoError:
		return true, nil
	case sarama.ErrTopicAuthorizationFailed:
		return false, nil
	default:
		return false, fmt.Errorf("probing topic describe access: %w", resp.Topics[0].Err)
	}
}

// PartitionCounts returns the partition count of each named topic that exists on the cluster. A topic the
// broker says does not exist is left out of the map, not an error: a conversion reads a missing count as
// "no copy on this cluster". Like the topic probe it asks one broker directly with auto-creation off,
// because the client's own metadata refresh defaults to creating a missing topic.
func (c *ConsumerGroupClient) PartitionCounts(topics []string) (map[string]int, error) {
	if len(topics) == 0 {
		return map[string]int{}, nil
	}
	b := c.client.LeastLoadedBroker()
	if b == nil {
		return nil, fmt.Errorf("no broker available to read partition counts")
	}
	if err := b.Open(c.client.Config()); err != nil && !errors.Is(err, sarama.ErrAlreadyConnected) {
		return nil, fmt.Errorf("connecting to %s to read partition counts: %w", b.Addr(), err)
	}
	return partitionCounts(b, topics)
}

// partitionCounts requests metadata for exactly topics, with auto-creation off. Any per-topic error other
// than "unknown topic" is an error, as is a topic the broker did not answer for, and a response below
// Metadata v4 (no auto-create flag, so a broker with auto.create.topics.enable could create the topic).
func partitionCounts(f metadataFetcher, topics []string) (map[string]int, error) {
	if len(topics) == 0 {
		return map[string]int{}, nil
	}
	resp, err := f.GetMetadata(&sarama.MetadataRequest{
		Version:                10,
		Topics:                 topics,
		AllowAutoTopicCreation: false,
	})
	if err != nil {
		return nil, fmt.Errorf("reading partition counts: %w", err)
	}
	if resp.Version < 4 {
		return nil, fmt.Errorf("reading partition counts: the broker only speaks Metadata v%d, which cannot be told not to auto-create a missing topic", resp.Version)
	}
	counts := make(map[string]int, len(topics))
	answered := make(map[string]bool, len(resp.Topics))
	for _, t := range resp.Topics {
		answered[t.Name] = true
		switch t.Err {
		case sarama.ErrNoError:
			counts[t.Name] = len(t.Partitions)
		case sarama.ErrUnknownTopicOrPartition:
		default:
			return nil, fmt.Errorf("reading the partition count of topic %s: %w", t.Name, t.Err)
		}
	}
	for _, name := range topics {
		if !answered[name] {
			return nil, fmt.Errorf("reading partition counts: the broker did not answer for topic %q", name)
		}
	}
	return counts, nil
}
