package plan

import (
	"regexp"

	"github.com/confluentinc/kcp/internal/services/report"
)

// Shared topic-name regexes used by both the Red Flags detector
// (per-row heuristics) and the Effort Signals detector (counters).
// Lives in its own file so the patterns are a single source of
// truth — multiple files inferring "what does a Connect fleet topic
// look like?" is the kind of drift that bit V2 review round 1.

// Connect internal topic — boundary-required form. The leading
// `(?:^|-)` non-capturing anchor stops a topic like "disconnect-configs"
// from matching while still permitting both an empty prefix
// (`connect-configs`) and any dash-suffixed prefix
// (`team-a-connect-configs`). Used by the red-flags row 7
// cross-check, the row-15 broad-pattern scan, and the
// effort-signals self-managed Connect fleet counter.
//
// Single capture group:
//
//	[1] = the kind suffix (configs | offsets | status)
//
// The boundary is intentionally non-capturing — callers that need the
// fleet prefix use `connectInternalTopicPrefix(topic)` (below), which
// derives it from the match-start offset rather than a capture group.
var connectInternalTopicPattern = regexp.MustCompile(`(?:^|-)connect-(configs|offsets|status)$`)

// connectInternalTopicPrefix returns the fleet prefix portion of a
// Connect internal topic name plus the kind suffix
// (configs|offsets|status), or ("", "", false) when the name doesn't
// match. The prefix is what precedes `-connect-…` (empty for the
// unprefixed default case). Callers use the prefix to group topics
// into fleets without re-implementing the regex.
func connectInternalTopicPrefix(topic string) (prefix, kind string, ok bool) {
	m := connectInternalTopicPattern.FindStringSubmatchIndex(topic)
	if m == nil {
		return "", "", false
	}
	// m[0] = match start, m[2..3] = group 1 (configs|offsets|status).
	// The fleet prefix is everything before the match start; when the
	// match starts at offset 0, the prefix is empty.
	if m[0] > 0 {
		prefix = topic[:m[0]]
	}
	kind = topic[m[2]:m[3]]
	return prefix, kind, true
}

// MM2 checkpoint topic. `<source-alias>.checkpoints.internal` is the
// MM2 default naming convention; deployments using
// IdentityReplicationPolicy suppress the prefix entirely (Effort
// Signal note surfaces that caveat to the customer).
var mm2CheckpointPattern = regexp.MustCompile(`\.checkpoints\.internal$`)

// Kafka Streams internal topic suffixes. Both the broad-pattern Red
// Flag row (15) and the Streams cross-check (`kafkaStreamsTopicsHit`)
// match against these — defined here so changing one suffix doesn't
// silently miss the other surface.
var (
	kafkaStreamsChangelogPattern   = regexp.MustCompile(`-changelog$`)
	kafkaStreamsRepartitionPattern = regexp.MustCompile(`-repartition$`)
)

// topicPatternFound reports whether any topic on `c` matches `re`,
// returning the first matched topic name so callers can surface
// evidence. Returns (false, "") when the topics scan didn't populate
// — the upstream `topic_inventory_empty` OQ already surfaces that
// gap, so callers shouldn't conflate "no match" with "no scan".
func topicPatternFound(c report.ProcessedCluster, re *regexp.Regexp) (bool, string) {
	if c.KafkaAdminClientInformation.Topics == nil {
		return false, ""
	}
	for _, td := range c.KafkaAdminClientInformation.Topics.Details {
		if re.MatchString(td.Name) {
			return true, td.Name
		}
	}
	return false, ""
}
