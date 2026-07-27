package report

import (
	"encoding/json"
	"fmt"
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
