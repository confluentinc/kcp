package report

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/discovery"
)

// Stats is the diagnostics view of a run: everything needed to judge how well the
// __transaction_state reader kept up (lag, decode failures) and the full per-transaction
// footprints. It is separate from Summary because migration.yaml stays the clean
// operator deliverable while this feeds the stress harness's recall check.
type Stats struct {
	Duration          time.Duration
	Interval          time.Duration
	TxnStateActive    bool
	OffsetsTailActive bool

	Footprints  []discovery.TxnFootprint
	Tail        discovery.TailStats
	OffsetsTail discovery.OffsetsTailStats
}

// PrintStats writes a short keep-up block for the __transaction_state reader (the
// source of truth) and the Phase 4 consumer-offsets tail: records seen, footprints
// reconstructed, completions, lag, and decode failures. A large, non-decreasing lag or
// any value-decode failure is the signal that the reader is not keeping up or that the
// internal record format has drifted.
func PrintStats(w io.Writer, s Stats) {
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Keep-up:")

	t := s.Tail
	fmt.Fprintf(w, "  txn-state reader: %d record(s), %d footprint(s), %d committed / %d aborted completion(s), max lag %d / final lag %d record(s)\n",
		t.RecordsSeen, t.Footprints, t.Committed, t.Aborted, t.MaxLag, t.FinalLag)
	if t.KeyDecodeErrors > 0 || t.ValueDecodeErrors > 0 {
		fmt.Fprintf(w, "  WARNING: %d key + %d value decode failure(s) — possible __transaction_state format drift\n",
			t.KeyDecodeErrors, t.ValueDecodeErrors)
	}

	if s.OffsetsTailActive {
		o := s.OffsetsTail
		fmt.Fprintf(w, "  consumer-offsets tail (exact EOS input recovery): %d record(s), "+
			"%d txn offset-commit(s), %d group(s) exactly linked, %d input topic(s) recovered, "+
			"max lag %d / final lag %d record(s)\n",
			o.RecordsSeen, o.TxnRecords, o.GroupsLinked, len(o.RecoveredTopics), o.MaxLag, o.FinalLag)
		if o.KeyDecodeErrors > 0 {
			fmt.Fprintf(w, "  WARNING: %d __consumer_offsets key decode failure(s)\n", o.KeyDecodeErrors)
		}
	}
}

// --- stats JSON ---

type statsDoc struct {
	GeneratedAt                string         `json:"generated_at"`
	ObservationWindow          string         `json:"observation_window"`
	EnrichmentInterval         string         `json:"enrichment_interval"`
	TransactionStateTailActive bool           `json:"transaction_state_tail_active"`
	ConsumerOffsetsTailActive  bool           `json:"consumer_offsets_tail_active"`
	ObservedTransactionalIDs   int            `json:"observed_transactional_ids"`
	TransactionStateLog        tailDoc        `json:"transaction_state_log"`
	ConsumerOffsetsLog         offsetsTailDoc `json:"consumer_offsets_log"`
	Transactions               []statsTxn     `json:"transactions"`
}

type tailDoc struct {
	RecordsSeen       int64 `json:"records_seen"`
	Footprints        int64 `json:"footprints"`
	Tombstones        int64 `json:"tombstones"`
	Empty             int64 `json:"empty"`
	Committed         int64 `json:"committed_completions"`
	Aborted           int64 `json:"aborted_completions"`
	KeyDecodeErrors   int64 `json:"key_decode_errors"`
	ValueDecodeErrors int64 `json:"value_decode_errors"`
	MaxLagRecords     int64 `json:"max_lag_records"`
	FinalLagRecords   int64 `json:"final_lag_records"`
}

type offsetsTailDoc struct {
	RecordsSeen     int64    `json:"records_seen"`
	TxnRecords      int64    `json:"txn_offset_commits"`
	KeyDecodeErrors int64    `json:"key_decode_errors"`
	MaxLagRecords   int64    `json:"max_lag_records"`
	FinalLagRecords int64    `json:"final_lag_records"`
	GroupsLinked    int      `json:"groups_linked"`
	Correlations    int      `json:"correlations"`
	RecoveredTopics []string `json:"recovered_topics"`
}

type statsTxn struct {
	TransactionalID  string   `json:"transactional_id"`
	ProducerID       int64    `json:"producer_id"`
	Topics           []string `json:"topics"`
	ReadProcessWrite bool     `json:"read_process_write"`
	Sources          []string `json:"sources"`
	Samples          int      `json:"samples"`
}

// WriteStatsJSON writes the diagnostics report to path. The per-transaction
// footprints (not just the aggregated groups) are included so an external verifier
// can compute recall against a ground-truth manifest at the transaction level.
func WriteStatsJSON(path string, s Stats) error {
	doc := statsDoc{
		GeneratedAt:                time.Now().UTC().Format(time.RFC3339),
		ObservationWindow:          s.Duration.String(),
		EnrichmentInterval:         s.Interval.String(),
		TransactionStateTailActive: s.TxnStateActive,
		ConsumerOffsetsTailActive:  s.OffsetsTailActive,
		ObservedTransactionalIDs:   len(s.Footprints),
		TransactionStateLog: tailDoc{
			RecordsSeen:       s.Tail.RecordsSeen,
			Footprints:        s.Tail.Footprints,
			Tombstones:        s.Tail.Tombstones,
			Empty:             s.Tail.Empty,
			Committed:         s.Tail.Committed,
			Aborted:           s.Tail.Aborted,
			KeyDecodeErrors:   s.Tail.KeyDecodeErrors,
			ValueDecodeErrors: s.Tail.ValueDecodeErrors,
			MaxLagRecords:     s.Tail.MaxLag,
			FinalLagRecords:   s.Tail.FinalLag,
		},
		ConsumerOffsetsLog: offsetsTailDoc{
			RecordsSeen:     s.OffsetsTail.RecordsSeen,
			TxnRecords:      s.OffsetsTail.TxnRecords,
			KeyDecodeErrors: s.OffsetsTail.KeyDecodeErrors,
			MaxLagRecords:   s.OffsetsTail.MaxLag,
			FinalLagRecords: s.OffsetsTail.FinalLag,
			GroupsLinked:    s.OffsetsTail.GroupsLinked,
			Correlations:    s.OffsetsTail.Correlations,
			RecoveredTopics: emptyIfNil(s.OffsetsTail.RecoveredTopics),
		},
	}
	for _, fp := range s.Footprints {
		doc.Transactions = append(doc.Transactions, statsTxn{
			TransactionalID:  fp.TxnID,
			ProducerID:       fp.ProducerID,
			Topics:           fp.Topics,
			ReadProcessWrite: fp.ReadProcessWrite,
			Sources:          fp.Sources,
			Samples:          fp.Samples,
		})
	}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal stats doc: %w", err)
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

func emptyIfNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}
