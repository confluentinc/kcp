package discovery

import (
	"sort"
	"sync"
	"time"
)

// TxnFootprint is the union of everything observed for one transactional id across the
// whole run (all samples, all sources).
type TxnFootprint struct {
	TxnID            string
	ProducerID       int64
	Topics           []string // sorted union across sources; still includes internal topics
	ReadProcessWrite bool
	Sources          []string // sorted names of the phases that reported this transaction
	FirstSeen        time.Time
	LastSeen         time.Time
	Samples          int
}

// Accumulator merges Observations from every source, keyed by transactional id. Safe
// for concurrent use.
type Accumulator struct {
	mu    sync.Mutex
	byTxn map[string]*txnState
}

type txnState struct {
	producerID int64
	// topics is the union across every source rather than a set per source. Nothing
	// downstream reads a per-source topic set — only the set of source names — and the
	// union is what "did this observation grow the transaction's topic set?" has to be
	// answered against: a topic already reported by another source is not growth.
	topics           map[string]struct{}
	readProcessWrite bool
	sources          map[string]struct{}
	firstSeen        time.Time
	lastSeen         time.Time
	samples          int
}

// NewAccumulator returns an empty accumulator ready for concurrent use.
func NewAccumulator() *Accumulator {
	return &Accumulator{byTxn: make(map[string]*txnState)}
}

// Change reports what an Add did to a transaction's topic set.
type Change struct {
	// Added are the topics the observation introduced, sorted. Empty when the
	// observation reported nothing the transaction had not already been seen touching.
	Added []string

	// Topics is the transaction's resulting full topic set, sorted.
	Topics []string
}

// Grew reports whether the observation grew the transaction's topic set.
func (c Change) Grew() bool { return len(c.Added) > 0 }

// Add merges an observation into the footprint for its transactional id and reports
// what that did to the transaction's topic set.
func (a *Accumulator) Add(obs Observation) Change {
	// An observation with no transactional id is dropped rather than pooled into a
	// keyless bucket, which would chain unrelated topics into one group and attribute
	// audit lines to a transaction that does not exist.
	if obs.TxnID == "" {
		return Change{}
	}
	// The whole merge runs under the lock: the check-and-insert that computes Added must
	// be atomic, or two concurrent sources reporting the same topic both claim it (a
	// duplicate audit line) or neither does (a coupling missing from the trace).
	a.mu.Lock()
	defer a.mu.Unlock()

	st := a.byTxn[obs.TxnID]
	if st == nil {
		st = &txnState{
			topics:    make(map[string]struct{}),
			sources:   make(map[string]struct{}),
			firstSeen: obs.ObservedAt,
		}
		a.byTxn[obs.TxnID] = st
	}
	// Only a positive id is recorded. The naming-based enrichment phase reports 0 and a
	// source with no producer may report Kafka's -1 sentinel; neither may displace a
	// real id, which is the only key an offset commit can be correlated on.
	if obs.ProducerID > 0 {
		st.producerID = obs.ProducerID
	}
	st.sources[obs.Source] = struct{}{}
	var added []string
	for _, t := range obs.Topics {
		if _, seen := st.topics[t]; seen {
			continue
		}
		st.topics[t] = struct{}{}
		added = append(added, t)
	}
	sort.Strings(added)

	if obs.ReadProcessWrite {
		st.readProcessWrite = true // sticky: once read-process-write, always
	}
	// Sources run concurrently, so a sighting can arrive out of order; only move the
	// window forwards.
	if obs.ObservedAt.After(st.lastSeen) {
		st.lastSeen = obs.ObservedAt
	}
	st.samples++

	return Change{Added: added, Topics: sortedKeys(st.topics)}
}

// Snapshot returns the accumulated footprints, sorted by transactional id.
func (a *Accumulator) Snapshot() []TxnFootprint {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]TxnFootprint, 0, len(a.byTxn))
	for id, st := range a.byTxn {
		out = append(out, TxnFootprint{
			TxnID:            id,
			ProducerID:       st.producerID,
			Topics:           sortedKeys(st.topics),
			ReadProcessWrite: st.readProcessWrite,
			Sources:          sortedKeys(st.sources),
			FirstSeen:        st.firstSeen,
			LastSeen:         st.lastSeen,
			Samples:          st.samples,
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].TxnID < out[j].TxnID })
	return out
}

func sortedKeys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}
