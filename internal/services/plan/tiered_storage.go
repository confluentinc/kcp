package plan

import (
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// Plan-input enum tokens for tiered-storage knobs. Stable
// customer-facing strings; the resolver surfaces them through
// PlanInputsResolved.ConsumerHistoryRequirement and HistoricalDataStrategy.
const (
	ConsumerHistoryRequired    = "required"
	ConsumerHistoryNotRequired = "not_required"
	ConsumerHistoryUnknown     = "unknown"

	HistoricalKeepMSKRunning = "keep_msk_running_until_data_expires"
	HistoricalBulkLoadExtern = "bulk_load_historical_via_external_tool"
	HistoricalDeferToAccount = "defer_to_account_team"
)

// detectTieredStorage produces the per-cluster tiered-storage view.
// Returns nil when no source cluster has tiered storage enabled —
// section is omitted entirely in that case (most fleets won't use it).
//
// The section is intentionally informational: kcp does not pick a
// migration path here. The renderer surfaces the three-dimension
// trade-off (mechanism / duration / cost direction) so the customer
// (and account team) can decide whether the cold data is worth
// re-fetching.
func detectTieredStorage(state report.ProcessedState, inputs types.PlanInputsResolved) *types.TieredStorageSection {
	clusters := collectClusters(state)
	var tiered []types.TieredStorageCluster
	for _, c := range clusters {
		// Serverless has no `StorageMode` (Provisioned-only field).
		// Skip explicitly so the empty `clusterStorageMode` return
		// can't be misread if the helper semantics change.
		if isServerless(c) {
			continue
		}
		if clusterStorageMode(c) != kafkatypes.StorageModeTiered {
			continue
		}
		tiered = append(tiered, types.TieredStorageCluster{
			ClusterID:          c.Name,
			StorageMode:        string(kafkatypes.StorageModeTiered),
			RemoteLogSizeBytes: remoteLogSizeBytesOf(c),
		})
	}
	if len(tiered) == 0 {
		return nil
	}
	return &types.TieredStorageSection{
		Clusters:                   tiered,
		ConsumerHistoryRequirement: defaultedConsumerHistory(inputs.ConsumerHistoryRequirement),
		HistoricalDataStrategy:     defaultedHistoricalStrategy(inputs.HistoricalDataStrategy, inputs.ConsumerHistoryRequirement),
	}
}

// remoteLogSizeBytesOf reads the CloudWatch `RemoteLogSizeBytes`
// peak (Maximum) for the cluster. Returns 0 when the metric wasn't
// collected — informational only, not the basis for a dollar
// estimate.
//
// Maximum is the right aggregate for a monotonically-accumulating
// gauge like RemoteLogSize: it surfaces the peak observed footprint,
// whereas Average over a 30-day window for a fixed 7-day retention
// would report ~half the real current footprint. Falls back to
// Average when Maximum isn't populated.
func remoteLogSizeBytesOf(c report.ProcessedCluster) float64 {
	agg, ok := c.ClusterMetrics.Aggregates["RemoteLogSizeBytes"]
	if !ok {
		return 0
	}
	if agg.Maximum != nil {
		return *agg.Maximum
	}
	if agg.Average != nil {
		return *agg.Average
	}
	return 0
}

// defaultedConsumerHistory normalizes the customer's
// `consumer_history_requirement` input: empty (no preference declared)
// resolves to `required` (the safer default — assume history matters
// until the customer says otherwise).
func defaultedConsumerHistory(v string) string {
	if v == "" {
		return ConsumerHistoryRequired
	}
	return v
}

// defaultedHistoricalStrategy applies the spec's cascade: when
// `consumer_history_requirement == not_required`, the strategy
// defaults to `defer_to_account_team` (the consumer-offset /
// per-topic cascade is workload-specific and lives with the account
// team). Otherwise empty stays empty so the renderer can emit an OQ
// asking the customer to pick.
func defaultedHistoricalStrategy(v, consumerHistory string) string {
	if v != "" {
		return v
	}
	if consumerHistory == ConsumerHistoryNotRequired {
		return HistoricalDeferToAccount
	}
	return ""
}

// knownConsumerHistory + knownHistoricalStrategy validate the
// customer-declared enums so a typo in plan-inputs.yaml surfaces as
// an OQ instead of being silently swallowed.
func knownConsumerHistory(v string) bool {
	return knownEnum(v, ConsumerHistoryRequired, ConsumerHistoryNotRequired, ConsumerHistoryUnknown)
}

func knownHistoricalStrategy(v string) bool {
	return knownEnum(v, HistoricalKeepMSKRunning, HistoricalBulkLoadExtern, HistoricalDeferToAccount)
}

// detectTieredStorageOpenQuestions emits the OQs that gate full
// confidence in the tiered-storage recommendation: typos in the two
// enums and the "you have tiered storage but haven't picked a
// strategy" prompt.
func detectTieredStorageOpenQuestions(section *types.TieredStorageSection, inputs types.PlanInputsResolved) []types.OpenQuestion {
	if section == nil {
		return nil
	}
	var oqs []types.OpenQuestion
	if !knownConsumerHistory(inputs.ConsumerHistoryRequirement) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "tiered_consumer_history_invalid",
			Title:      "`consumer_history_requirement` is not a recognised value",
			Body:       "Recognised values: `required` (default) | `not_required` | `unknown`. The current value falls outside the enum; the Plan treats it as `required` until corrected.",
			HowToClose: "Set `consumer_history_requirement` in `plan-inputs.yaml` to one of the recognised values, then re-run `kcp report plan`.",
		})
	}
	if inputs.HistoricalDataStrategy != "" && !knownHistoricalStrategy(inputs.HistoricalDataStrategy) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "tiered_historical_strategy_invalid",
			Title:      "`historical_data_strategy` is not a recognised value",
			Body:       "Recognised values: `keep_msk_running_until_data_expires` | `bulk_load_historical_via_external_tool` | `defer_to_account_team`. The current value falls outside the enum; the Plan ignores it and treats the strategy as undeclared.",
			HowToClose: "Set `historical_data_strategy` in `plan-inputs.yaml` to one of the recognised values, then re-run `kcp report plan`.",
		})
	}
	// Strategy undeclared AND the customer either hasn't expressed a
	// consumer-history preference OR explicitly said `unknown` →
	// prompt them to pick. The `not_required` cascade auto-defaults
	// the strategy to `defer_to_account_team` upstream, so this OQ
	// stays quiet on that branch.
	consumerHistory := defaultedConsumerHistory(inputs.ConsumerHistoryRequirement)
	if section.HistoricalDataStrategy == "" && consumerHistory != ConsumerHistoryNotRequired {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "tiered_strategy_undeclared",
			Title:      "Tiered storage detected — declare `historical_data_strategy` in `plan-inputs.yaml`",
			Body:       "Cluster Linking does NOT carry historical tiered data forward. Pick the path that matches your operational constraints: keep MSK running until your retention window expires (lowest engineering cost, highest infra cost), bulk-load historical data via an external tool, or defer the cascade to your Confluent account team.",
			HowToClose: "Set `historical_data_strategy` in `plan-inputs.yaml` to `keep_msk_running_until_data_expires` | `bulk_load_historical_via_external_tool` | `defer_to_account_team`, then re-run `kcp report plan`. If you set `consumer_history_requirement: not_required`, the Plan defaults this to `defer_to_account_team` automatically.",
		})
	}
	return oqs
}
