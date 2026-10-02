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

// committedTopicsOf fetches every committed offset of one group. A topic counts
// as tracked only if some partition has a committed offset >= 0 (-1 means a
// subscription with no commit). Any block-level error fails the whole fetch.
func committedTopicsOf(f groupOffsetFetcher, group string) ([]string, error) {
	resp, err := f.ListConsumerGroupOffsets(group, nil)
	if err != nil {
		return nil, fmt.Errorf("fetching committed offsets for consumer group %s: %w", group, err)
	}
	var topics []string
	for topic, parts := range resp.Blocks {
		tracked := false
		for part, b := range parts {
			if b.Err != sarama.ErrNoError {
				return nil, fmt.Errorf("fetching committed offset for consumer group %s, topic %s partition %d: %w", group, topic, part, b.Err)
			}
			if b.Offset >= 0 {
				tracked = true
			}
		}
		if tracked {
			topics = append(topics, topic)
		}
	}
	sort.Strings(topics)
	return topics, nil
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
