// Package grouping derives transactional topic groups from observed transactions.
//
// Rule (from the design doc): if two or more topics appear within the same
// transaction, they belong in the same group. Coupling is transitive — two
// transactions that share even a single topic merge into one group — so the result
// is the connected components of the topic co-occurrence graph.
package grouping

import (
	"fmt"
	"sort"
	"strings"
)

// Transaction is the grouping input: one transactional id and the topics it touched.
type Transaction struct {
	ID               string
	Topics           []string
	ReadProcessWrite bool
}

// Options tunes grouping behaviour.
type Options struct {
	// IncludeInternalTopics keeps __-prefixed topics in the grouping. Default false:
	// internal topics — above all __consumer_offsets, which every EOS app shares —
	// are dropped BEFORE grouping. Leaving them in would transitively chain every
	// unrelated EOS workload into one giant group.
	IncludeInternalTopics bool
}

// Group is a set of topics that must migrate together (atomically).
type Group struct {
	Name             string
	Topics           []string
	TxnIDs           []string
	ReadProcessWrite bool
}

// Result is the full output of grouping.
type Result struct {
	Groups []Group

	// IndividualTopics were only ever seen alone in a transaction (or in no
	// multi-topic transaction) and can migrate one at a time.
	IndividualTopics []string

	// ReadProcessWriteTopics are topics PRODUCED inside a consume-transform-produce
	// (EOS) transaction. Surfaced separately — and independently of whether they
	// landed in a group or as an individual topic — because their CONSUMED input
	// topics are invisible in the transaction footprint. A read-process-write app that produces
	// to a single topic otherwise looks like a safe "move individually" topic when it
	// is not: moving it without its (unknown) input topics breaks EOS at cutover.
	ReadProcessWriteTopics []string
}

// IsInternalTopic reports whether t is a Kafka-internal topic (e.g.
// __consumer_offsets, __transaction_state) that must be excluded from grouping.
func IsInternalTopic(t string) bool {
	return strings.HasPrefix(t, "__")
}

type component struct {
	topics           map[string]struct{}
	txns             map[string]struct{}
	readProcessWrite bool
}

// Build computes the transactional topic groups from txns.
func Build(txns []Transaction, opts Options) Result {
	uf := newUnionFind()
	txnTopics := make(map[string][]string, len(txns))
	rpwTxns := make(map[string]bool)

	for _, txn := range txns {
		var topics []string
		for _, t := range txn.Topics {
			if !opts.IncludeInternalTopics && IsInternalTopic(t) {
				continue
			}
			topics = append(topics, t)
			uf.add(t)
		}
		txnTopics[txn.ID] = topics
		if txn.ReadProcessWrite {
			rpwTxns[txn.ID] = true
		}
		// Union every topic in this transaction together (via the first one).
		for i := 1; i < len(topics); i++ {
			uf.union(topics[0], topics[i])
		}
	}

	// Bucket topics into their connected components.
	comps := map[string]*component{}
	for topic := range uf.parent {
		root := uf.find(topic)
		c := comps[root]
		if c == nil {
			c = &component{topics: map[string]struct{}{}, txns: map[string]struct{}{}}
			comps[root] = c
		}
		c.topics[topic] = struct{}{}
	}

	// Attribute each transaction (and its RPW flag) to the component of its topics.
	for id, topics := range txnTopics {
		for _, t := range topics {
			c := comps[uf.find(t)]
			c.txns[id] = struct{}{}
			if rpwTxns[id] {
				c.readProcessWrite = true
			}
		}
	}

	var (
		groups     []Group
		individual []string
		rpwTopics  []string
	)
	for _, c := range comps {
		topics := keys(c.topics)
		if c.readProcessWrite {
			rpwTopics = append(rpwTopics, topics...)
		}
		if len(topics) < 2 {
			individual = append(individual, topics...)
			continue
		}
		groups = append(groups, Group{
			Topics:           topics,
			TxnIDs:           keys(c.txns),
			ReadProcessWrite: c.readProcessWrite,
		})
	}

	// Deterministic ordering: largest groups first, then alphabetical.
	sort.Slice(groups, func(i, j int) bool {
		if len(groups[i].Topics) != len(groups[j].Topics) {
			return len(groups[i].Topics) > len(groups[j].Topics)
		}
		return groups[i].Topics[0] < groups[j].Topics[0]
	})
	for i := range groups {
		groups[i].Name = fmt.Sprintf("group-%d", i+1)
	}
	sort.Strings(individual)
	sort.Strings(rpwTopics)

	return Result{
		Groups:                 groups,
		IndividualTopics:       individual,
		ReadProcessWriteTopics: rpwTopics,
	}
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
