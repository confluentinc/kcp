package discovery

import (
	"sort"
	"sync"
	"time"
)

// TxnFootprint is the union of everything observed for one transactional id across
// the whole run (all samples, all sources).
type TxnFootprint struct {
	TxnID            string
	ProducerID       int64
	Topics           []string // sorted union across sources; still includes internal topics
	ReadProcessWrite bool
	Sources          []string
	FirstSeen        time.Time
	LastSeen         time.Time
	Samples          int
}

// Accumulator merges Observations from every source, keyed by transactional id. It
// keeps each source's topic set separately so a footprint can be attributed to the
// source(s) that reported it. Safe for concurrent use.
type Accumulator struct {
	mu    sync.Mutex
	byTxn map[string]*txnState
}

type txnState struct {
	producerID       int64
	topicsBySource   map[string]map[string]struct{}
	readProcessWrite bool
	firstSeen        time.Time
	lastSeen         time.Time
	samples          int
}

func NewAccumulator() *Accumulator {
	return &Accumulator{byTxn: make(map[string]*txnState)}
}

// Add merges an observation into the footprint for its transactional id.
func (a *Accumulator) Add(obs Observation) {
	a.mu.Lock()
	defer a.mu.Unlock()

	st := a.byTxn[obs.TxnID]
	if st == nil {
		st = &txnState{
			topicsBySource: make(map[string]map[string]struct{}),
			firstSeen:      obs.ObservedAt,
		}
		a.byTxn[obs.TxnID] = st
	}

	// Keep the first real producer id seen. Phase 3 emits observations with no
	// producer id (0); those must not clobber a real id recorded by the reader or Phase 4.
	if obs.ProducerID > 0 {
		st.producerID = obs.ProducerID
	}
	ts := st.topicsBySource[obs.Source]
	if ts == nil {
		ts = make(map[string]struct{})
		st.topicsBySource[obs.Source] = ts
	}
	for _, t := range obs.Topics {
		ts[t] = struct{}{}
	}
	if obs.ReadProcessWrite {
		st.readProcessWrite = true // sticky: once RPW, always RPW
	}
	if obs.ObservedAt.After(st.lastSeen) {
		st.lastSeen = obs.ObservedAt
	}
	st.samples++
}

// Snapshot returns the accumulated footprints, sorted by transactional id.
func (a *Accumulator) Snapshot() []TxnFootprint {
	a.mu.Lock()
	defer a.mu.Unlock()

	out := make([]TxnFootprint, 0, len(a.byTxn))
	for id, st := range a.byTxn {
		union := make(map[string]struct{})
		sources := make(map[string]struct{})
		for src, ts := range st.topicsBySource {
			sources[src] = struct{}{}
			for t := range ts {
				union[t] = struct{}{}
			}
		}
		out = append(out, TxnFootprint{
			TxnID:            id,
			ProducerID:       st.producerID,
			Topics:           sortedKeys(union),
			ReadProcessWrite: st.readProcessWrite,
			Sources:          sortedKeys(sources),
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
