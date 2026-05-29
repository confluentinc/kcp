package plan

import (
	"regexp"

	"github.com/confluentinc/kcp/internal/types"
)

// Shared topic-name regexes used by both the Red Flags detector
// (per-row heuristics) and the Effort Signals detector (counters).
// Lives in its own file so the patterns are a single source of
// truth — multiple files inferring "what does a Connect fleet topic
// look like?" is the kind of drift that bit V2 review round 1.

// Connect internal topic. Captures `(prefix, full-suffix, suffix-kind)`
// where prefix may be empty (vanilla Connect deployment uses
// `connect-configs` / `-offsets` / `-status` without a prefix). The
// effort-signals counter groups by (cluster, prefix); the red-flags
// detector uses the same regex for the row-7 cross-check, and the
// row-15 broad-pattern scan re-uses it via the broadTopicPatterns
// table.
var connectInternalTopicPattern = regexp.MustCompile(`^(.*?)(connect-(configs|offsets|status))$`)

// MM2 checkpoint topic. `<source-alias>.checkpoints.internal` is the
// MM2 default naming convention; deployments using
// IdentityReplicationPolicy suppress the prefix entirely (Effort
// Signal note surfaces that caveat to the customer).
var mm2CheckpointPattern = regexp.MustCompile(`\.checkpoints\.internal$`)

// topicPatternFound reports whether any topic on `c` matches `re`,
// returning the first matched topic name so callers can surface
// evidence. Returns (false, "") when the topics scan didn't populate
// — the upstream `topic_inventory_empty` OQ already surfaces that
// gap, so callers shouldn't conflate "no match" with "no scan".
func topicPatternFound(c types.ProcessedCluster, re *regexp.Regexp) (bool, string) {
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
