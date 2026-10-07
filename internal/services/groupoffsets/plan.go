package groupoffsets

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/services/offset"
)

// ErrMissingTopics is returned by DestinationHighWaterMarks when a topic some group committed on is not on the
// destination.
var ErrMissingTopics = errors.New("committed topics missing on the destination")

// MissingTopicsError names the snapshot topics the destination does not have.
type MissingTopicsError struct {
	Topics []string
}

func (e *MissingTopicsError) Error() string {
	return fmt.Sprintf("%s: %s; every link topic was on the destination when the fence went up, so it was deleted on the destination during the run — restore it or remove it from the link, then re-run",
		ErrMissingTopics, strings.Join(e.Topics, ", "))
}

// Is makes errors.Is(err, ErrMissingTopics) true for a MissingTopicsError.
func (e *MissingTopicsError) Is(target error) bool { return target == ErrMissingTopics }

// TopicLister lists the destination's topics, from a fresh read.
type TopicLister func(ctx context.Context) ([]string, error)

// DestinationHighWaterMarks takes the one high-water-mark snapshot BuildPlan validates against: the latest
// offset of every partition of every topic in snap, read through sweep. It first checks snap's topics against
// a fresh listing of the destination and refuses with a *MissingTopicsError, without sweeping, if any is
// missing: GetMany would only fail on the first missing topic with a less useful error, and a sweep must never
// touch a topic that may not exist.
//
// sweep's client must be built with sarama's Metadata.AllowAutoTopicCreation set to false (its default is
// true), so the sweep can never create a topic on a destination with auto.create.topics.enable.
func DestinationHighWaterMarks(ctx context.Context, listTopics TopicLister, sweep offset.Provider, snap Snapshot) (map[string]map[int32]int64, error) {
	topics := snap.Topics()
	if len(topics) == 0 {
		return map[string]map[int32]int64{}, nil
	}
	listed, err := listTopics(ctx)
	if err != nil {
		return nil, fmt.Errorf("listing destination topics: %w", err)
	}
	onDest := make(map[string]struct{}, len(listed))
	for _, t := range listed {
		onDest[t] = struct{}{}
	}
	var missing []string
	for _, t := range topics {
		if _, ok := onDest[t]; !ok {
			missing = append(missing, t)
		}
	}
	if len(missing) > 0 {
		return nil, &MissingTopicsError{Topics: missing}
	}
	hwm, err := sweep.GetMany(ctx, topics)
	if err != nil {
		return nil, fmt.Errorf("reading destination high-water marks: %w", err)
	}
	return hwm, nil
}

// Commit is the offsets to write for one group, all in a single OffsetCommit.
type Commit struct {
	Group   string
	Offsets Offsets
}

// Plan is what Apply writes: one Commit per group that has something to write, sorted by group.
type Plan struct {
	Commits []Commit
}

// Partitions is the total number of partitions the plan writes.
func (p *Plan) Partitions() int {
	n := 0
	for _, c := range p.Commits {
		for _, parts := range c.Offsets {
			n += len(parts)
		}
	}
	return n
}

// ErrOutOfRange is returned by BuildPlan when a committed offset cannot be written to the destination.
var ErrOutOfRange = errors.New("committed offset out of range on the destination")

// Violation is one committed offset that cannot be written: past the destination partition's high-water
// mark, or for a partition the destination does not have.
type Violation struct {
	Group         string
	Topic         string
	Partition     int32
	Offset        int64
	HighWaterMark int64 // zero when NoPartition
	NoPartition   bool
}

// OutOfRangeError carries every violation BuildPlan found.
type OutOfRangeError struct {
	Violations []Violation
}

func (e *OutOfRangeError) Error() string {
	const show = 5
	var parts []string
	for i, v := range e.Violations {
		if i == show {
			break
		}
		if v.NoPartition {
			parts = append(parts, fmt.Sprintf("group %s topic %s partition %d: committed %d, but the destination has no such partition", v.Group, v.Topic, v.Partition, v.Offset))
		} else {
			parts = append(parts, fmt.Sprintf("group %s topic %s partition %d: committed %d, high-water mark %d", v.Group, v.Topic, v.Partition, v.Offset, v.HighWaterMark))
		}
	}
	msg := fmt.Sprintf("refusing to sync, %d committed offset(s) cannot be written to the destination, e.g. %s; "+
		"something committed past what was migrated (a producer bypassed the gateway, or an admin reset the group on the source), "+
		"or the destination topic changed during the run — fix the group's offset on the source, or the topic, and re-run; re-running alone cannot clear it",
		len(e.Violations), strings.Join(parts, "; "))
	if len(e.Violations) > show {
		msg += fmt.Sprintf(" (and %d more)", len(e.Violations)-show)
	}
	return msg
}

// Is makes errors.Is(err, ErrOutOfRange) true for an OutOfRangeError.
func (e *OutOfRangeError) Is(target error) bool { return target == ErrOutOfRange }

// BuildPlan checks every committed offset in snap against hwm, one high-water-mark snapshot of the
// destination's partitions (from DestinationHighWaterMarks), and builds the commits to write.
//
// An offset is out of range only if it is greater than its partition's high-water mark: equal is valid, since
// a caught-up consumer commits exactly the end. A committed partition the destination does not have is also a
// violation, never silently dropped; there is no skip path. If there is any violation BuildPlan returns an
// OutOfRangeError and no plan, so a refusal writes nothing at all. Offsets are never clamped: a clamped value
// would launder the decision through the consumer's own auto.offset.reset, which kcp cannot see. A planned
// commit carries each partition's committed offset and metadata unchanged.
func BuildPlan(snap Snapshot, hwm map[string]map[int32]int64) (*Plan, error) {
	var (
		violations []Violation
		plan       = &Plan{}
	)
	for _, group := range snap.Groups() {
		commit := Offsets{}
		topics := make([]string, 0, len(snap[group]))
		for t := range snap[group] {
			topics = append(topics, t)
		}
		sort.Strings(topics)
		for _, topic := range topics {
			partitions := make([]int32, 0, len(snap[group][topic]))
			for p := range snap[group][topic] {
				partitions = append(partitions, p)
			}
			sort.Slice(partitions, func(i, j int) bool { return partitions[i] < partitions[j] })
			for _, partition := range partitions {
				committed := snap[group][topic][partition]
				end, ok := hwm[topic][partition]
				switch {
				case !ok:
					violations = append(violations, Violation{Group: group, Topic: topic, Partition: partition, Offset: committed.Offset, NoPartition: true})
				case committed.Offset > end:
					violations = append(violations, Violation{Group: group, Topic: topic, Partition: partition, Offset: committed.Offset, HighWaterMark: end})
				default:
					if commit[topic] == nil {
						commit[topic] = map[int32]CommittedOffset{}
					}
					commit[topic][partition] = committed
				}
			}
		}
		if len(commit) > 0 {
			plan.Commits = append(plan.Commits, Commit{Group: group, Offsets: commit})
		}
	}
	if len(violations) > 0 {
		return nil, &OutOfRangeError{Violations: violations}
	}
	return plan, nil
}
