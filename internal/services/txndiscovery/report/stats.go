package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/confluentinc/kcp/internal/services/txndiscovery/tail"
)

// statsDoc is the on-disk shape of the --stats-out document: the diagnostics view of a
// run, carrying the keep-up signal in full (R21) plus the per-transaction footprints
// the summary reduces to counts.
//
// It is a sensitive artifact, not a counts-only file: the footprints carry transactional
// ids and topic names, which is why it is written at the same 0600 as txn-discovery.yaml
// rather than the 0644 a report would use.
type statsDoc struct {
	GeneratedAt        string   `json:"generated_at"`
	ObservationWindow  string   `json:"observation_window"`
	EnrichmentInterval string   `json:"enrichment_interval"`
	ActiveSources      []string `json:"active_sources"`

	ObservedTransactionalIDs int `json:"observed_transactional_ids"`

	Tail                tailDoc        `json:"tail"`
	TransactionStateLog txnStateDoc    `json:"transaction_state_log"`
	ConsumerOffsetsLog  offsetsTailDoc `json:"consumer_offsets_log"`

	// AuditLogWriteErrors is the count the summary warns on, recorded here too so a
	// captured run's diagnostics say whether its audit trace is complete.
	AuditLogWriteErrors int `json:"audit_log_write_errors"`

	Transactions []statsTxn `json:"transactions"`
}

// tailDoc is the fetch component's keep-up view: aggregate first, then per partition.
// Both are needed — the aggregate says whether the run kept up, the per-partition
// breakdown says which partition did not.
type tailDoc struct {
	PartitionsAssigned int   `json:"partitions_assigned"`
	PartitionsRunning  int   `json:"partitions_running"`
	RecordsRead        int64 `json:"records_read"`
	// Lag is measured to the last stable offset (KTD9); OpenTxnBacklog is the
	// high-watermark gap, which is not something the reader can catch up on.
	Lag                int64          `json:"lag"`
	OpenTxnBacklog     int64          `json:"open_txn_backlog"`
	AbortedBatches     int64          `json:"aborted_batches"`
	DecodeErrors       int64          `json:"decode_errors"`
	LeadershipErrors   int64          `json:"leadership_errors"`
	UnclassifiedErrors int64          `json:"unclassified_errors"`
	OffsetResets       int64          `json:"offset_resets"`
	TransportErrors    int64          `json:"transport_errors"`
	Partitions         []partitionDoc `json:"partitions"`
}

type partitionDoc struct {
	Topic            string `json:"topic"`
	Partition        int32  `json:"partition"`
	NextOffset       int64  `json:"next_offset"`
	LastStableOffset int64  `json:"last_stable_offset"`
	HighWaterMark    int64  `json:"high_water_mark"`
	Lag              int64  `json:"lag"`
	OpenTxnBacklog   int64  `json:"open_txn_backlog"`
	// Running and LastAdvance together separate a stalled partition from an idle
	// one: both report zero lag, and only these say which happened.
	Running     bool       `json:"running"`
	LastAdvance *time.Time `json:"last_advance"`

	RecordsRead        int64  `json:"records_read"`
	AbortedBatches     int64  `json:"aborted_batches"`
	DecodeErrors       int64  `json:"decode_errors"`
	LeadershipErrors   int64  `json:"leadership_errors"`
	UnclassifiedErrors int64  `json:"unclassified_errors"`
	OffsetResets       int64  `json:"offset_resets"`
	TransportErrors    int64  `json:"transport_errors"`
	LastError          string `json:"last_error"`
}

type txnStateDoc struct {
	RecordsSeen int64 `json:"records_seen"`
	Footprints  int64 `json:"footprints"`
	Empty       int64 `json:"empty"`
	Committed   int64 `json:"committed_completions"`
	Aborted     int64 `json:"aborted_completions"`
	Tombstones  int64 `json:"tombstones"`
	// Key and value decode errors are kept apart so the alarm says which half of the
	// record drifted.
	KeyDecodeErrors   int64 `json:"key_decode_errors"`
	ValueDecodeErrors int64 `json:"value_decode_errors"`
}

type offsetsTailDoc struct {
	RecordsSeen      int64    `json:"records_seen"`
	TxnRecords       int64    `json:"txn_offset_commits"`
	KeyDecodeErrors  int64    `json:"key_decode_errors"`
	PendingProducers int      `json:"pending_producers"`
	PendingEvicted   int64    `json:"pending_evicted"`
	GroupsLinked     int      `json:"groups_linked"`
	Correlations     int      `json:"correlations"`
	RecoveredTopics  []string `json:"recovered_topics"`
	// Unavailable distinguishes a phase that never ran from one that ran and found
	// nothing; both otherwise report zero recoveries (R13).
	Unavailable       bool   `json:"unavailable"`
	UnavailableReason string `json:"unavailable_reason"`
}

type statsTxn struct {
	TransactionalID  string    `json:"transactional_id"`
	ProducerID       int64     `json:"producer_id"`
	Topics           []string  `json:"topics"`
	ReadProcessWrite bool      `json:"read_process_write"`
	Sources          []string  `json:"sources"`
	FirstSeen        time.Time `json:"first_seen"`
	LastSeen         time.Time `json:"last_seen"`
	Samples          int       `json:"samples"`
}

// PrintKeepUp writes the detailed keep-up breakdown.
//
// Per KTD4 this does NOT belong in the default summary, which carries one health line:
// three overlapping views of the same numbers is what the output tidy-up removed. The
// command calls this only under --verbose, for the operator who has seen the health line
// warn and needs to know which partition or which source is responsible.
//
// It names only the two internal topics the tail is assigned, never a customer topic.
func PrintKeepUp(w io.Writer, r Run) {
	_, _ = fmt.Fprintln(w)
	_, _ = fmt.Fprintln(w, "Keep-up detail:")

	t := r.Tail
	_, _ = fmt.Fprintf(w, "  fetch: %d/%d %s live, %d %s read, lag %d, open-txn backlog %d\n",
		t.PartitionsRunning, t.PartitionsAssigned, plural(t.PartitionsAssigned, "partition", "partitions"),
		t.RecordsRead, plural64(t.RecordsRead, "record", "records"), t.Lag, t.OpenTxnBacklog)
	// The taxonomy the health line collapses into one number. A run that recovered
	// from fifty leader changes and one that never saw a broker hiccup produce the
	// same health line, and this is where they differ.
	_, _ = fmt.Fprintf(w, "    errors: %d decode, %d leadership, %d unclassified, %d offset reset, %d transport\n",
		t.DecodeErrors, t.LeadershipErrors, t.UnclassifiedErrors, t.OffsetResets, t.TransportErrors)

	for _, p := range t.Partitions {
		state := "live"
		if !p.Running {
			state = "STOPPED"
		}
		_, _ = fmt.Fprintf(w, "    %s[%d]: next %d, LSO %d, HWM %d, lag %d, backlog %d, %d %s, %s\n",
			p.Topic, p.Partition, p.NextOffset, p.LastStableOffset, p.HighWaterMark,
			p.Lag, p.OpenTxnBacklog, p.RecordsRead, plural64(p.RecordsRead, "record", "records"), state)
		if p.LastError != "" {
			_, _ = fmt.Fprintf(w, "      last error: %s\n", p.LastError)
		}
	}

	ts := r.TxnState
	_, _ = fmt.Fprintf(w, "  transaction-state reader: %d %s, %d %s, %d committed / %d aborted, %d %s\n",
		ts.RecordsSeen, plural64(ts.RecordsSeen, "record", "records"),
		ts.Footprints, plural64(ts.Footprints, "footprint", "footprints"),
		ts.Committed, ts.Aborted,
		ts.Tombstones, plural64(ts.Tombstones, "tombstone", "tombstones"))
	if ts.KeyDecodeErrors > 0 || ts.ValueDecodeErrors > 0 {
		_, _ = fmt.Fprintf(w, "    decode failures: %d key, %d value — the __transaction_state record format may have drifted\n",
			ts.KeyDecodeErrors, ts.ValueDecodeErrors)
	}

	o := r.Offsets
	if o.Unavailable {
		_, _ = fmt.Fprintf(w, "  consumer-offsets tail: did not run (%s)\n", o.UnavailableReason)
		return
	}
	_, _ = fmt.Fprintf(w, "  consumer-offsets tail: %d %s, %d txn %s, %d %s linked, %d input %s recovered\n",
		o.RecordsSeen, plural64(o.RecordsSeen, "record", "records"),
		o.TxnRecords, plural64(o.TxnRecords, "offset-commit", "offset-commits"),
		o.GroupsLinked, plural(o.GroupsLinked, "group", "groups"),
		len(o.RecoveredTopics), plural(len(o.RecoveredTopics), "topic", "topics"))
	// Evictions are the only signal that the bounded pending buffer discarded
	// recoveries, so a run that evicted heavily observed fewer inputs than exist.
	_, _ = fmt.Fprintf(w, "    %d pending, %d pending %s, %d key decode %s\n",
		o.PendingProducers,
		o.PendingEvicted, plural64(o.PendingEvicted, "eviction", "evictions"),
		o.KeyDecodeErrors, plural64(o.KeyDecodeErrors, "failure", "failures"))
}

// WriteStatsJSON writes the diagnostics document for r to path.
func WriteStatsJSON(path string, r Run) error {
	doc := statsDoc{
		GeneratedAt:              time.Now().UTC().Format(time.RFC3339),
		ObservationWindow:        r.Duration.String(),
		EnrichmentInterval:       r.Interval.String(),
		ActiveSources:            emptyIfNil(r.ActiveSources),
		ObservedTransactionalIDs: len(r.Footprints),
		Tail:                     tailDocOf(r.Tail),
		TransactionStateLog: txnStateDoc{
			RecordsSeen:       r.TxnState.RecordsSeen,
			Footprints:        r.TxnState.Footprints,
			Empty:             r.TxnState.Empty,
			Committed:         r.TxnState.Committed,
			Aborted:           r.TxnState.Aborted,
			Tombstones:        r.TxnState.Tombstones,
			KeyDecodeErrors:   r.TxnState.KeyDecodeErrors,
			ValueDecodeErrors: r.TxnState.ValueDecodeErrors,
		},
		ConsumerOffsetsLog: offsetsTailDoc{
			RecordsSeen:       r.Offsets.RecordsSeen,
			TxnRecords:        r.Offsets.TxnRecords,
			KeyDecodeErrors:   r.Offsets.KeyDecodeErrors,
			PendingProducers:  r.Offsets.PendingProducers,
			PendingEvicted:    r.Offsets.PendingEvicted,
			GroupsLinked:      r.Offsets.GroupsLinked,
			Correlations:      r.Offsets.Correlations,
			RecoveredTopics:   emptyIfNil(r.Offsets.RecoveredTopics),
			Unavailable:       r.Offsets.Unavailable,
			UnavailableReason: r.Offsets.UnavailableReason,
		},
		AuditLogWriteErrors: r.AuditErrors,
		Transactions:        make([]statsTxn, 0, len(r.Footprints)),
	}
	for _, fp := range r.Footprints {
		doc.Transactions = append(doc.Transactions, statsTxn{
			TransactionalID:  fp.TxnID,
			ProducerID:       fp.ProducerID,
			Topics:           emptyIfNil(fp.Topics),
			ReadProcessWrite: fp.ReadProcessWrite,
			Sources:          emptyIfNil(fp.Sources),
			FirstSeen:        fp.FirstSeen,
			LastSeen:         fp.LastSeen,
			Samples:          fp.Samples,
		})
	}

	// encoding/json, never string formatting: topic names and transactional ids are
	// chosen by whoever can create a topic or configure a producer, and an unescaped
	// newline in one would let the rest of a name be read as a forged field.
	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal the transaction discovery stats document: %w", err)
	}
	return writeSensitiveFile(path, append(body, '\n'))
}

// tailDocOf converts the fetch component's snapshot, aggregate and per partition.
func tailDocOf(s tail.Stats) tailDoc {
	out := tailDoc{
		PartitionsAssigned: s.PartitionsAssigned,
		PartitionsRunning:  s.PartitionsRunning,
		RecordsRead:        s.RecordsRead,
		Lag:                s.Lag,
		OpenTxnBacklog:     s.OpenTxnBacklog,
		AbortedBatches:     s.AbortedBatches,
		DecodeErrors:       s.DecodeErrors,
		LeadershipErrors:   s.LeadershipErrors,
		UnclassifiedErrors: s.UnclassifiedErrors,
		OffsetResets:       s.OffsetResets,
		TransportErrors:    s.TransportErrors,
		Partitions:         make([]partitionDoc, 0, len(s.Partitions)),
	}
	for _, p := range s.Partitions {
		pd := partitionDoc{
			Topic:              p.Topic,
			Partition:          p.Partition,
			NextOffset:         p.NextOffset,
			LastStableOffset:   p.LastStableOffset,
			HighWaterMark:      p.HighWaterMark,
			Lag:                p.Lag,
			OpenTxnBacklog:     p.OpenTxnBacklog,
			Running:            p.Running,
			RecordsRead:        p.RecordsRead,
			AbortedBatches:     p.AbortedBatches,
			DecodeErrors:       p.DecodeErrors,
			LeadershipErrors:   p.LeadershipErrors,
			UnclassifiedErrors: p.UnclassifiedErrors,
			OffsetResets:       p.OffsetResets,
			TransportErrors:    p.TransportErrors,
			LastError:          p.LastError,
		}
		// A partition that never advanced has a zero time. Rendering that as
		// "0001-01-01T00:00:00Z" reads as a real timestamp from an absurd clock;
		// null says plainly that it never moved.
		if !p.LastAdvance.IsZero() {
			t := p.LastAdvance.UTC()
			pd.LastAdvance = &t
		}
		out.Partitions = append(out.Partitions, pd)
	}
	return out
}

// emptyIfNil renders an absent list as [] rather than null, so a consumer can iterate
// it without a nil check.
func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
