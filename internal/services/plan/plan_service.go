package plan

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/confluentinc/kcp/internal/build_info"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// Function-naming convention across this package:
//
//   - `decide*`  — produces a verdict / path from inputs. One decision
//     per cluster (sizing, cluster type, networking, auth) or one
//     per fleet (cutover, schema). Output is the rendered §section's
//     primary recommendation.
//
//   - `detect*`  — enumerates findings or signals from `state` /
//     `plan` (red flags, effort signals, tiered storage, cost
//     reconciliation, all OQ detectors). Output is a list/section,
//     not a single verdict. Some detectors also apply small input
//     cascades (e.g. `detectTieredStorage` defaults
//     `HistoricalDataStrategy`) — that's deliberate: the cascade is
//     scoped to the section's findings, not a standalone decision.
//
//   - `compute*` — pure numeric / structural transforms with no
//     verdict (e.g. `computeClusterSizing`, `computeCutoverOverrides`).
//     Output is intermediate data the renderer + decide* consume.
//
// PlanService orchestrates the deterministic plan-generation pipeline.
// Each Build step is a pure function so the test surface is the
// orchestration, not its parts.
type PlanService struct {
	cfg *PlanConfig
	now func() time.Time
}

// NewPlanService wires a PlanConfig into a PlanService. now is overridable
// for deterministic tests; pass nil for time.Now.
func NewPlanService(cfg *PlanConfig, now func() time.Time) *PlanService {
	if now == nil {
		now = time.Now
	}
	return &PlanService{cfg: cfg, now: now}
}

// Build produces a Plan from a ProcessedState and resolved plan-inputs.
// Each step is a pure function so the test surface is the orchestration,
// not its parts.
func (s *PlanService) Build(state types.ProcessedState, inputs types.PlanInputsResolved, stateFilePath string) (*types.Plan, error) {
	// Backfill `ClusterMetrics.Aggregates` once, in-place, against the
	// canonical state. Every downstream caller (the per-cluster Build
	// loop, plus every fleet-wide detector that re-runs
	// `collectClusters`) reads the same populated map without
	// recomputing CalculateMetricsAggregates per call.
	backfillAggregates(&state)
	// Merge customer-declared per-cluster facts from plan-inputs.yaml
	// into `state`. Two modes: overlay (cluster name matches an
	// existing scan cluster) or synthesise (no scan match — build a
	// fresh ProcessedCluster from the declaration). After this, the
	// rest of the pipeline reads the merged state without caring
	// whether values came from the scanner or the customer.
	applyClusterDeclarations(&state, inputs.Raw)
	clusters := collectClusters(state)
	// Stable sort by (Region, Name, Arn) so two clusters that share a name
	// across regions still get a deterministic order.
	sort.SliceStable(clusters, func(i, j int) bool {
		if clusters[i].Region != clusters[j].Region {
			return clusters[i].Region < clusters[j].Region
		}
		if clusters[i].Name != clusters[j].Name {
			return clusters[i].Name < clusters[j].Name
		}
		return clusters[i].Arn < clusters[j].Arn
	})

	plan := &types.Plan{
		Header: types.PlanHeader{
			Source:            "Amazon MSK",
			StateFilePath:     stateFilePath,
			KCPVersion:        build_info.Version,
			GeneratedAt:       s.now().UTC(),
			StateGeneratedAt:  state.Timestamp.UTC(),
			PlanSchemaVersion: "1",
		},
		Inputs: inputs,
	}

	for _, c := range clusters {
		// (Aggregates backfill happens in collectClusters so fleet-wide
		// helpers see the same map.)
		// Per-cluster overrides win over globals — heterogeneous fleets
		// need finer-grained inputs than flipping one global flag across
		// every cluster. We start from the already-resolved global view
		// (`inputs`) and layer any per-cluster overlay via
		// `applyClusterOverride` — that avoids the redundant global
		// re-resolution `ResolvePlanInputsForCluster` would do on every
		// iteration.
		clusterInputs := applyClusterOverride(inputs, inputs.Raw, c.Name)
		sizing := computeClusterSizing(c, s.cfg, clusterInputs)
		missing := inputsMissing(c)
		// Each consumer gets its own slice — without Clone, a future
		// `append` to any one InputsMissing would silently mutate the
		// others if cap permits.
		sizing.InputsMissing = slices.Clone(missing)
		ct := decideClusterType(c, sizing, s.cfg, clusterInputs)
		net := decideNetworking(sizing, ct, s.cfg, clusterInputs)
		ct.InputsMissing = slices.Clone(missing)
		net.InputsMissing = slices.Clone(missing)

		auth := decideAuth(c, s.cfg, clusterInputs)

		plan.Sizing = append(plan.Sizing, sizing)
		plan.ClusterTypeDecision = append(plan.ClusterTypeDecision, ct)
		plan.NetworkingDecision = append(plan.NetworkingDecision, net)
		plan.Auth = append(plan.Auth, auth)
		plan.SourceEnvironment.Clusters = append(plan.SourceEnvironment.Clusters, types.SourceClusterSummary{
			ClusterID:    c.Name,
			Region:       c.Region,
			TopicCount:   topicCount(c),
			BrokerCount:  brokerCount(c),
			IsServerless: isServerless(c),
			SourceAuths:  auth.SourceAuths,
		})
		plan.OpenQuestions = append(plan.OpenQuestions, detectOpenQuestions(c, sizing, ct, net, auth, s.cfg, clusterInputs)...)
	}
	plan.SourceEnvironment.TotalRegions = countRegions(state)
	plan.SizingAppendix = buildSizingAppendix(plan.Sizing, s.cfg, inputs)

	// Cutover defaults are fleet-wide; per-cluster overrides layer on
	// top via clusters[<name>].downtime_tolerance / .sub_pattern.
	// Empty fleet → no cutover decision at all.
	if len(clusters) > 0 {
		cutover := decideCutover(clusters, inputs)
		plan.Cutover = &cutover
		plan.CutoverOverrides = computeCutoverOverrides(clusters, cutover, inputs)
		plan.OpenQuestions = append(plan.OpenQuestions, detectCutoverOpenQuestions(cutover, plan.CutoverOverrides, inputs, fleetUsesIAM(clusters))...)
		plan.OpenQuestions = append(plan.OpenQuestions, detectAuthFleetOpenQuestions(clusters, inputs)...)
		plan.OpenQuestions = append(plan.OpenQuestions, detectClusterCutoverOpenQuestions(clusters, inputs)...)
		plan.OpenQuestions = append(plan.OpenQuestions, detectPerClusterGatewayIncompat(clusters, cutover, inputs)...)
		plan.OpenQuestions = append(plan.OpenQuestions, detectUnknownClusterOverrides(clusters, inputs)...)
		plan.OpenQuestions = append(plan.OpenQuestions, detectAmbiguousClusterOverrides(state, inputs)...)
	}
	// Schema migration — fleet-wide; one Plan, one verdict. The
	// `schemaless` branch returns nil so the renderer can omit the
	// whole section cleanly. We still run the OQ detector either way:
	// the strategy-typo / strategy-unknown signals are valuable even
	// when the verdict resolves to no recommendation.
	schema := decideSchema(state, s.cfg, inputs)
	if schema != nil && !hasPath(schema, types.SchemaPathSchemaless) {
		plan.Schema = schema
	}
	plan.OpenQuestions = append(plan.OpenQuestions, detectSchemaOpenQuestions(schema, s.cfg, inputs)...)

	// Effort Signals — quantitative inputs the customer's PM consumes
	// to scope migration effort. Counts only; no day-estimate.
	plan.EffortSignals = detectEffortSignals(state)

	// Tiered Storage — per-cluster trade-off framing for fleets with
	// MSK tiered storage enabled. Customer-decision shaped; kcp
	// doesn't pick a path, just makes the three dimensions
	// (mechanism / duration / cost direction) legible.
	plan.TieredStorage = detectTieredStorage(state, inputs)
	plan.OpenQuestions = append(plan.OpenQuestions, detectTieredStorageOpenQuestions(plan.TieredStorage, inputs)...)

	// Cost-vs-Inventory Reconciliation — lists MSK instance types in
	// the AWS cost report that `kcp discover` didn't surface. Sorted
	// by spend desc; no materiality threshold (the customer judges
	// what's real). Emits an OQ when cost data is empty. Runs BEFORE
	// Red Flags so row 16 (cost_inventory_hidden_clusters) can read
	// plan.CostReconciliation as input.
	plan.CostReconciliation = detectCostReconciliation(state, s.cfg)
	plan.OpenQuestions = append(plan.OpenQuestions, detectCostReconciliationOpenQuestions(state)...)

	// Red Flags — fleet-wide list of trigger rows the customer should
	// discuss with the SE. Each row carries its own evidence (field
	// path + value) so the conversation is grounded in scan facts.
	plan.RedFlags = detectRedFlags(state, plan, s.cfg, inputs)

	// Stale-state OQ: surface a fleet-wide accuracy warning when the
	// source state file is older than the freshness window. The Plan
	// still renders against whatever's in state.json — but a 14-day-old
	// snapshot could miss ACL drift, new brokers, etc. that would
	// change the verdicts.
	plan.OpenQuestions = append(plan.OpenQuestions, detectStaleStateOQ(state.Timestamp, s.now(), s.cfg.Thresholds.StaleStateDays)...)

	// OSK-source OQ: today's plan covers MSK only; OSK clusters in the
	// state are silently ignored. Surface a fleet-wide OQ so the
	// customer knows their on-prem Kafka clusters didn't get planned.
	plan.OpenQuestions = append(plan.OpenQuestions, detectOSKSourceOpenQuestion(state)...)

	// Stable order: blocker OQs first, acknowledgement OQs last; tie-break
	// on cluster ID so two clusters of equal priority sort alphabetically.
	sort.SliceStable(plan.OpenQuestions, func(i, j int) bool {
		pi, pj := openQuestionPriority(plan.OpenQuestions[i].ID), openQuestionPriority(plan.OpenQuestions[j].ID)
		if pi != pj {
			return pi < pj
		}
		return plan.OpenQuestions[i].ClusterID < plan.OpenQuestions[j].ClusterID
	})

	return plan, nil
}

// detectOpenQuestions surfaces state-file gaps and inferred-signal quirks
// per cluster. The Plan still ships a recommendation in each case (the
// SLA floor for degraded sizing, the verdict from the other rules when
// the ACL rule is skipped, etc.) — the OQ tells the customer what action
// will upgrade that recommendation. SERVERLESS-specific suppressions for
// "ACLs not populated" and "0 brokers" live in cluster_signals.go so the
// same logic is shared with the rule evaluator.
func detectOpenQuestions(c types.ProcessedCluster, sizing types.ClusterSizing, ct types.ClusterTypeDecision, net types.NetworkingDecision, auth types.AuthDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	// Auth posture undetectable. Fires whenever the source has no
	// detected auth methods — auth is its own concern, surfaced
	// independently of topic / ACL inventory gaps (those have their
	// own OQs and don't suppress this one). The OQ body / how-to-close
	// branch on cluster type because the resolution path differs:
	// Provisioned needs an admin re-scan; Serverless needs the
	// Serverless.ClientAuthentication block populated.
	if len(auth.SourceAuths) == 0 {
		oq := types.OpenQuestion{
			ID:        "auth_posture_unknown",
			ClusterID: c.Name,
			Title:     "No client-authentication methods detected on the source — auth migration recommendation is unconfirmed",
		}
		if isServerless(c) {
			oq.Body = "`AWSClientInformation.MskClusterConfig.Serverless.ClientAuthentication` is empty for this Serverless cluster. MSK Serverless supports only SASL/IAM, but the auth block can be missing if the scan ran without admin credentials or the cluster pre-dates the auth setup."
			oq.HowToClose = fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account to refresh `Serverless.ClientAuthentication`. If the cluster genuinely has no auth wired yet, configure SASL/IAM on the MSK side first.\n\nOR declare the auth method directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    auth_methods: [iam]   # Serverless supports IAM only\n```", regionFlag(c.Region), c.Name)
		} else {
			oq.Body = "Neither `AWSClientInformation.MskClusterConfig.Provisioned.ClientAuthentication` nor `kafka_admin_client_information.sasl_mechanism` reports an enabled auth method. A real MSK Provisioned cluster always has at least one. Likely cause: discover ran without admin credentials (or the state file pre-dates the auth scan)."
			oq.HowToClose = fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account, OR provide admin Kafka credentials to `kcp scan clusters` so the Admin probe can backfill `sasl_mechanism`.\n\nOR declare the auth methods directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    auth_methods: [scram]   # subset of {scram, iam, mtls, unauth}\n```", regionFlag(c.Region), c.Name)
		}
		oqs = append(oqs, oq)
	}
	// Customer-set target_auth_method conflicts with a source whose
	// auth_mapping is gateway_compatible: false (today: IAM). The plan
	// emits the override mapping but the gateway path itself remains
	// incompatible — surface the conflict so the customer sees the gap.
	// Skip the conflict OQ when the customer opted out of the gateway
	// — gateway compatibility is moot on the plain-CL path, so the
	// conflict isn't real. Also skip on unknown target (the
	// target_auth_method_unknown OQ already fires for that; pairing the
	// two would double-report a single typo).
	if inputs.TargetAuthMethod != "" && inputs.PreferGateway && knownTargetAuthMethod(inputs.TargetAuthMethod) {
		for _, row := range auth.TargetMappings {
			if !row.GatewayCompatible {
				oqs = append(oqs, types.OpenQuestion{
					ID:         "auth_target_gateway_incompatible",
					ClusterID:  c.Name,
					Title:      fmt.Sprintf("`target_auth_method: %s` set but source auth `%s` is gateway-incompatible", inputs.TargetAuthMethod, row.SourceAuth),
					Body:       fmt.Sprintf("You've overridden the target auth to `%s`, but the source uses `%s` — the CC Gateway can't accept `%s` clients at all (regardless of where they land on the CC side). The target-side override changes the credential clients present to CC; it does not change which auth scheme CC accepts at the gateway. Source clients need to pre-migrate to a gateway-compatible auth (SCRAM or mTLS) on MSK before the gateway path is viable.", inputs.TargetAuthMethod, row.SourceAuth, row.SourceAuth),
					HowToClose: fmt.Sprintf("Two paths: (a) pre-migrate source clients off `%s` on MSK — typical replacement is SASL/SCRAM (see [MSK SASL/SCRAM docs](https://docs.aws.amazon.com/msk/latest/developerguide/msk-password.html)); flip the matching pre-migration prereq to `complete` and re-run the plan. OR (b) unset `target_auth_method` and let the per-source default apply — the override doesn't help here because the gateway barrier is on the source side.", row.SourceAuth),
				})
				break // one OQ per cluster, not one per gateway-incompatible row
			}
		}
	}
	if sizing.Degraded {
		howToClose := fmt.Sprintf("Re-run `kcp discover%s` without `--skip-metrics` so CloudWatch metrics get backfilled into the state file.\n\nOR declare throughput directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    peak_ingress_mbps: <MBps>   # required\n    peak_egress_mbps: <MBps>    # required\n    p95_ingress_mbps: <MBps>    # optional; peak doubles as P95 when omitted\n    p95_egress_mbps:  <MBps>    # optional\n```", regionFlag(c.Region), c.Name)
		if isServerless(c) {
			howToClose = fmt.Sprintf("Serverless throughput isn't auto-populated by `kcp discover` — declare ingress/egress targets in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    peak_ingress_mbps: <MBps>\n    peak_egress_mbps: <MBps>\n```\nOR work with your Confluent account team to size against actual workload rates.", c.Name)
		}
		oqs = append(oqs, types.OpenQuestion{
			ID:         "missing_p95_metrics",
			ClusterID:  c.Name,
			Title:      fmt.Sprintf("No %s throughput metrics — sizing fell back to SLA floor", percentileHeader(inputs.SizingPercentile)),
			Body:       sizing.DegradedReason,
			HowToClose: howToClose,
		})
	}
	if !aclScanRan(c) && !isServerless(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "acls_not_scanned",
			ClusterID:  c.Name,
			Title:      "Admin scan didn't populate ACLs — cap-vs-Enterprise rule was skipped",
			Body:       "The `acl_count_exceeds_cap` hard-limit rule needs a successful ACL scan to evaluate. Either the scan didn't run, or `--skip-acls` was passed; without the ACL list the rule is treated as inconclusive and the verdict resolves on the other rules.",
			HowToClose: fmt.Sprintf("Re-run `kcp scan clusters --source-type msk --credentials-file msk-credentials.yaml` without `--skip-acls`. The credentials file is a YAML with the admin Kafka credentials (SASL/IAM or SCRAM) — see `kcp scan clusters --help` for the schema, or [the kcp docs](https://confluentinc.github.io/kcp/command-reference/scan/clusters/) for a sample.\n\nSample `msk-credentials.yaml`:\n```yaml\nclusters:\n  - cluster_arn: <arn>\n    authentication_type: SASL_SCRAM        # or AWS_MSK_IAM\n    sasl_scram_username: <username>        # for SASL/SCRAM\n    sasl_scram_password: <password>\n```\n\nOR declare the ACL count directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    acl_count: <integer>\n```", c.Name),
		})
	}
	if brokerInventoryGap(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "broker_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 brokers — likely an incomplete scan",
			Body:       "`AWSClientInformation.Nodes` is empty for this cluster. The Source Environment table reads as `Brokers: 0`, which is almost certainly wrong for an MSK Provisioned cluster.",
			HowToClose: fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account to populate the broker inventory.\n\nOR declare the broker count directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    broker_count: <integer>\n    broker_instance_type: kafka.m5.large   # optional; drives Express tier detection\n```", regionFlag(c.Region), c.Name),
		})
	}
	if topicCount(c) == 0 {
		topicsField := c.KafkaAdminClientInformation.Topics
		var body string
		switch {
		case topicsField == nil:
			body = "`KafkaAdminClientInformation.Topics` is absent on this cluster — the admin scan that enumerates topics didn't run (or ran with `--skip-topics`)."
		case isServerless(c):
			body = "`KafkaAdminClientInformation.Topics.Summary.Topics` is 0. For a Serverless cluster that's only plausible if the cluster has genuinely never been used; if apps are connected, the admin scan probably didn't run with credentials to enumerate topics."
		default:
			body = "`KafkaAdminClientInformation.Topics.Summary.Topics` is 0. The Source Environment table reads as `Topics: 0`, which is almost certainly wrong for a real MSK cluster (system topics alone usually push the count above zero)."
		}
		oqs = append(oqs, types.OpenQuestion{
			ID:         "topic_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 topics — likely an incomplete scan",
			Body:       body,
			HowToClose: fmt.Sprintf("Re-run `kcp scan clusters --source-type msk --credentials-file <msk-credentials.yaml>` with admin Kafka credentials (without `--skip-topics`) to populate the topic list.\n\nOR declare topic/partition counts directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    topic_count: <integer>          # user-topic count\n    partition_count: <integer>      # total user-partitions; drives the eCKU partition-cap check\n```", c.Name),
		})
	}
	if hasUnknownClusterType(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "cluster_type_unrecognised",
			ClusterID:  c.Name,
			Title:      "MSK cluster discriminator unrecognised — Plan treated as Provisioned with empty fields",
			Body:       fmt.Sprintf("`MskClusterConfig.ClusterType` is %q. Recognised values are `PROVISIONED` and `SERVERLESS`. Without a recognised discriminator (or with `PROVISIONED` but `Provisioned == nil`), the Provisioned-shaped helpers — Kafka version, broker instance type, storage mode, mTLS detection — all return empty. The cluster appears in the Plan with most signals missing.", string(c.AWSClientInformation.MskClusterConfig.ClusterType)),
			HowToClose: fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account to refresh `MskClusterConfig`. If the cluster legitimately uses a future MSK variant, file an issue against kcp so it can be added to the recognised set.\n\nOR declare the cluster shape directly in `plan-inputs.yaml`:\n```yaml\nclusters:\n  %s:\n    cluster_type: PROVISIONED      # or SERVERLESS\n    kafka_version: \"3.6.0\"\n    broker_instance_type: kafka.m5.large\n    storage_mode: LOCAL            # or TIERED\n```", regionFlag(c.Region), c.Name),
		})
	}
	if privateLinkSizingExceedsCap(sizing, ct, net, cfg) {
		cap := cfg.EnterpriseCaps.PrivateLinkMaxECKU
		oqs = append(oqs, types.OpenQuestion{
			ID:         "networking_privatelink_over_cap",
			ClusterID:  c.Name,
			Title:      fmt.Sprintf("PrivateLink trigger fired but cluster sizing exceeds the %d-eCKU PrivateLink cap on Enterprise", cap),
			Body:       fmt.Sprintf("PrivateLink networking on Enterprise is capped at %d eCKU. One or more clusters sized above that cap, and a PrivateLink trigger fired (target_cloud, cc_egress_required, or projected_pni_gateway_count), so the plan still recommends PrivateLink — an infeasible combination above the %d-eCKU cap. See the Sizing & Cluster Decisions table above for each affected cluster's final eCKU.", cap, cap),
			HowToClose: fmt.Sprintf("Raise this with your Confluent account team — Dedicated supports PrivateLink without the %d-eCKU cap. Or remove the PrivateLink trigger in `plan-inputs.yaml` if it was misdeclared:\n```yaml\ntarget_cloud: aws                       # PNI is AWS-only; non-AWS forces PrivateLink\ncc_egress_required: false               # true → additive Egress PrivateLink Endpoint\nprojected_pni_gateway_count: 1          # ≥2 flips to PrivateLink\n```", cap),
		})
	}
	// Spiky workload is an informational signal, not an Open Question:
	// the P95-based sizing + Enterprise elasticity absorbs occasional
	// spikes, so there's nothing the customer MUST do. The rationale
	// renderer surfaces a one-line note inline so the customer still
	// sees the signal and knows the override path (`sizing_percentile: p99`)
	// exists if they want tighter sizing.
	return oqs
}

// detectOSKSourceOpenQuestion surfaces a fleet-wide OQ when the
// state file contains OSK (open-source Kafka, on-prem) clusters.
// `kcp report plan` covers MSK only today; without this OQ those
// clusters would be silently dropped from the plan.
func detectOSKSourceOpenQuestion(state types.ProcessedState) []types.OpenQuestion {
	var oskCount int
	for _, src := range state.Sources {
		if src.OSKData != nil {
			oskCount += len(src.OSKData.Clusters)
		}
	}
	if oskCount == 0 {
		return nil
	}
	noun, verb := "clusters", "aren't"
	if oskCount == 1 {
		noun, verb = "cluster", "isn't"
	}
	return []types.OpenQuestion{{
		ID:         "osk_source_unsupported",
		Title:      fmt.Sprintf("%d on-prem Kafka %s in the state file %s covered by `kcp report plan`", oskCount, noun, verb),
		Body:       "The state file includes `osk_sources` clusters (open-source Kafka, e.g. on-prem deployments). `kcp report plan` currently scopes to MSK source clusters only — the OSK clusters are silently dropped from every section above. The MSK-shaped recommendations still stand for any MSK clusters in the same state file.",
		HowToClose: "Plan the OSK clusters separately: run `kcp report plan` against a state file slice that only contains the OSK clusters, OR work with your Confluent account team on a manual migration plan for the on-prem fleet.",
	}}
}

// detectStaleStateOQ surfaces a fleet-wide OQ when the source state
// file is older than `staleDays`. Compares state.Timestamp against the
// Plan's GeneratedAt time; if the state was empty (zero timestamp) the
// OQ is suppressed because there's nothing to compare against.
// `staleDays` comes from plan-config.yaml `thresholds.stale_state_days`.
func detectStaleStateOQ(stateTimestamp time.Time, generatedAt time.Time, staleDays int) []types.OpenQuestion {
	if stateTimestamp.IsZero() {
		return nil
	}
	threshold := time.Duration(staleDays) * 24 * time.Hour
	age := generatedAt.UTC().Sub(stateTimestamp.UTC())
	if age < threshold {
		return nil
	}
	// Round to nearest full day so the rendered title isn't off by a
	// long fractional remainder (a 7d 23h-old file rendering as
	// "7 days old" reads as wrong-by-a-day). Math.Round avoids
	// truncation toward zero.
	days := int(math.Round(age.Hours() / 24))
	return []types.OpenQuestion{{
		ID:         "state_file_stale",
		Title:      fmt.Sprintf("State file is %d days old — verdicts may not reflect current source state", days),
		Body:       fmt.Sprintf("The source state file is dated `%s`; this Plan was generated `%s` (%d days later). Verdicts above (ACL-cap, broker counts, throughput sizing) are computed against the state file as-is, but the source environment may have drifted since.", stateTimestamp.UTC().Format("2006-01-02 15:04:05 UTC"), generatedAt.UTC().Format("2006-01-02 15:04:05 UTC"), days),
		HowToClose: "Re-run `kcp discover` (and `kcp scan clusters` if you have admin creds) to refresh the state file, then re-run `kcp report plan`. If the source environment hasn't changed materially, you can ignore this OQ.",
	}}
}

// detectCutoverOpenQuestions surfaces fleet-wide gateway-intent /
// prereq gaps. Emitted once per Plan (no ClusterID — these aren't
// per-cluster). Independent OQs that can fire:
//   - gateway intent ambiguous (degraded_awaiting_oq)
//   - gateway prereqs pending (degraded_prereqs_pending)
//   - seconds_per_service tolerance requires the gateway, but it isn't
//     mediated for some reason (cross-check; message varies by cause).
//
// `overrides` carries per-cluster cutover exceptions; when any cluster
// overrides to Blue/Green, the gateway-intent / prereq OQs add a note
// that those clusters are exempt — otherwise the OQ reads as if it
// applies to the entire fleet.
func detectCutoverOpenQuestions(cutover types.CutoverDecision, overrides []types.ClusterCutoverOverride, inputs types.PlanInputsResolved, iamInUse bool) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	if !knownDowntimeTolerance(inputs.DowntimeTolerance) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "downtime_tolerance_unknown",
			Title:      fmt.Sprintf("`downtime_tolerance: %s` is not a recognised value — defaulted to Stop-Restart-Repeat", inputs.DowntimeTolerance),
			Body:       "The Plan only recognises `zero | seconds_per_service | minutes_per_service | scheduled_window_sequential | scheduled_window_all_at_once | let_confluent_choose`. The current value falls outside the enum, so the Plan inherits the Confluent default (Stop-Restart-Repeat) silently — which is probably not what you intended.",
			HowToClose: "In `plan-inputs.yaml`:\n```yaml\ndowntime_tolerance: let_confluent_choose   # zero | seconds_per_service | minutes_per_service | scheduled_window_sequential | scheduled_window_all_at_once | let_confluent_choose\n```",
		})
	}
	// Clusters with a Blue/Green override sidestep the gateway-mediation
	// question entirely; the fleet-wide gateway OQs below need to
	// acknowledge that or the reader sees a contradiction between §3's
	// "gateway N/A for this style" note and the OQ asking them to pick
	// a gateway path "for the fleet".
	gatewayExempt := bgOverrideClusterNames(overrides)
	exemptSuffix := ""
	if len(gatewayExempt) > 0 {
		exemptSuffix = fmt.Sprintf(" Per-cluster Blue/Green overrides (`%s`) sidestep the gateway question — this OQ applies to the rest of the fleet.", strings.Join(gatewayExempt, "`, `"))
	}
	switch cutover.RecommendationStatus {
	case types.RecommendationDegradedAwaitingOQ:
		oqs = append(oqs, types.OpenQuestion{
			ID:         "gateway_intent_unconfirmed",
			Title:      "Gateway intent — confirm the CC Gateway opt-in",
			Body:       "`prefer_gateway: true` is set but both gateway-infra prereqs (`confluent_for_kubernetes_status`, `cc_gateway_license_status`) are still at `not_started`. Plain Cluster Linking applies until the gateway opt-in is followed through on." + exemptSuffix,
			HowToClose: "In `plan-inputs.yaml`, either (a) remove `prefer_gateway: true` (or set it to `false`) to commit to plain Cluster Linking, OR (b) move at least one gateway prereq to `in_progress` to commit to the gateway path. Re-run `kcp report plan` to clear the OQ.",
		})
	case types.RecommendationDegradedPrereqsPending:
		oqs = append(oqs, types.OpenQuestion{
			ID:         "gateway_prereqs_pending",
			Title:      "Gateway prereqs — pending items before the gateway path can be recommended",
			Body:       fmt.Sprintf("`prefer_gateway: true` and at least one gateway-infra prereq is still at `not_started`: %s. The gateway-mediated path needs both at `in_progress` or `complete`. Plain Cluster Linking applies until they advance.%s", pendingPrereqList(inputs, iamInUse), exemptSuffix),
			HowToClose: "Update the prereq statuses in `plan-inputs.yaml`:\n```yaml\nconfluent_for_kubernetes_status: in_progress   # or complete\ncc_gateway_license_status:       in_progress   # or complete\n```\nEach field accepts `not_started | in_progress | complete`. Re-run `kcp report plan` once they advance.",
		})
	}
	// Cross-check: `seconds_per_service` is only achievable through the
	// gateway. If the customer asks for it but mediation isn't possible
	// (opt-out OR ambiguous OR prereqs pending), surface a cause-specific
	// OQ. Blue/Green sidesteps the gateway entirely and never trips this.
	if inputs.DowntimeTolerance == DowntimeSecondsPerService && cutover.GatewayMediated != types.GatewayMediatedTrue && cutover.Style != types.CutoverBlueGreen {
		body := "seconds_per_service downtime tolerance requires CC-Gateway mediation (the gateway's 30–90s `BROKER_NOT_AVAILABLE` window is what makes sub-minute cutovers possible). The current Plan doesn't mediate via the gateway because: "
		switch {
		case !inputs.PreferGateway:
			body += "`prefer_gateway: false`. Set `prefer_gateway: true` OR relax `downtime_tolerance` to `minutes_per_service` (plain Cluster Linking)."
		case cutover.RecommendationStatus == types.RecommendationDegradedAwaitingOQ:
			body += "gateway intent is unconfirmed (see the related Open Question above). Commit to the gateway path or relax `downtime_tolerance`."
		default:
			body += "one or more gateway prereqs are still at `not_started`. Move pending prereqs to `in_progress`/`complete`, OR relax `downtime_tolerance` to `minutes_per_service`."
		}
		oqs = append(oqs, types.OpenQuestion{
			ID:         "downtime_tolerance_requires_gateway",
			Title:      "`downtime_tolerance: seconds_per_service` requires the gateway but the recommendation is plain Cluster Linking",
			Body:       body,
			HowToClose: "Either commit to the gateway path in `plan-inputs.yaml`:\n```yaml\nprefer_gateway: true                            # opt-in; kcp default is false (plain Cluster Linking)\nconfluent_for_kubernetes_status: in_progress    # not_started | in_progress | complete\ncc_gateway_license_status:       in_progress    # not_started | in_progress | complete\n```\nOR relax the downtime requirement:\n```yaml\ndowntime_tolerance: minutes_per_service         # zero | seconds_per_service | minutes_per_service | scheduled_window_sequential | scheduled_window_all_at_once | let_confluent_choose\n```\nThe `minutes_per_service` value works without the gateway; the other recognised values that don't need the gateway are `scheduled_window_sequential`, `scheduled_window_all_at_once`, and `let_confluent_choose`.",
		})
	}
	return oqs
}

// detectAuthFleetOpenQuestions surfaces auth OQs that come from
// invalid customer input — the global `target_auth_method` typo
// (fleet-level OQ, single emit) and any per-cluster
// `clusters[<name>].target_auth_method` typos (per-cluster OQs so the
// affected cluster is obvious). Per-cluster typos silently fall back
// to the per-source default in decideAuth via effectiveTarget, so
// without this detector they're invisible to the customer.
func detectAuthFleetOpenQuestions(clusters []types.ProcessedCluster, inputs types.PlanInputsResolved) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	if !knownTargetAuthMethod(inputs.TargetAuthMethod) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "target_auth_method_unknown",
			Title:      fmt.Sprintf("`target_auth_method: %s` is not a recognised value — per-source defaults applied instead", inputs.TargetAuthMethod),
			Body:       fmt.Sprintf("The Plan only recognises `%s | %s | %s`. The current value falls outside the enum; the per-source `auth_mapping` default is used silently for every cluster.", TargetAuthAPIKeys, TargetAuthMTLS, TargetAuthOAuth),
			HowToClose: fmt.Sprintf("In `plan-inputs.yaml`:\n```yaml\ntarget_auth_method: %s   # %s | %s | %s\n```\nOR remove the line to keep the per-source default. Per-cluster override available at `clusters[<name>].target_auth_method` with the same enum.", TargetAuthAPIKeys, TargetAuthAPIKeys, TargetAuthMTLS, TargetAuthOAuth),
		})
	}
	for _, name := range sortedKnownClusterOverrideNames(clusters, inputs) {
		cluster := inputs.Raw.Clusters[name]
		if cluster.TargetAuthMethod == nil {
			continue
		}
		value := *cluster.TargetAuthMethod
		if knownTargetAuthMethod(value) {
			continue
		}
		oqs = append(oqs, types.OpenQuestion{
			ID:         "target_auth_method_unknown",
			ClusterID:  name,
			Title:      fmt.Sprintf("`clusters[%s].target_auth_method: %s` is not a recognised value — per-source default applied for this cluster", name, value),
			Body:       fmt.Sprintf("The Plan only recognises `%s | %s | %s`. The override falls outside the enum; the per-source `auth_mapping` default is used silently for `%s`.", TargetAuthAPIKeys, TargetAuthMTLS, TargetAuthOAuth, name),
			HowToClose: fmt.Sprintf("Set `clusters[%s].target_auth_method` in `plan-inputs.yaml` to one of the recognised values, OR remove the override to keep the per-source default.", name),
		})
	}
	return oqs
}

// sortedKnownClusterOverrideNames returns the keys of
// `inputs.Raw.Clusters` that match an actual scanned cluster, in
// stable alphabetical order. Unknown-cluster overrides are filtered
// out here; `detectUnknownClusterOverrides` handles surfacing them as
// their own OQ. Returns nil when no Raw inputs exist.
func sortedKnownClusterOverrideNames(clusters []types.ProcessedCluster, inputs types.PlanInputsResolved) []string {
	if inputs.Raw == nil || len(inputs.Raw.Clusters) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(clusters))
	for _, c := range clusters {
		known[c.Name] = struct{}{}
	}
	names := make([]string, 0, len(inputs.Raw.Clusters))
	for name := range inputs.Raw.Clusters {
		if _, ok := known[name]; !ok {
			continue
		}
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// detectUnknownClusterOverrides surfaces 🟡 OQs for keys under
// `clusters:` in plan-inputs.yaml that don't match any scanned
// cluster. Without this, a typo'd cluster name (e.g. `clusters[trust]`
// against a fleet with no `trust` cluster) silently produces no
// override and the reader has no signal that their input was rejected.
func detectUnknownClusterOverrides(clusters []types.ProcessedCluster, inputs types.PlanInputsResolved) []types.OpenQuestion {
	if inputs.Raw == nil || len(inputs.Raw.Clusters) == 0 {
		return nil
	}
	known := make(map[string]struct{}, len(clusters))
	for _, c := range clusters {
		known[c.Name] = struct{}{}
	}
	var unknown []string
	for name := range inputs.Raw.Clusters {
		if _, ok := known[name]; ok {
			continue
		}
		unknown = append(unknown, name)
	}
	if len(unknown) == 0 {
		return nil
	}
	sort.Strings(unknown)
	oqs := make([]types.OpenQuestion, 0, len(unknown))
	for _, name := range unknown {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "cluster_override_unknown_cluster",
			Title:      fmt.Sprintf("`clusters[%s]` in `plan-inputs.yaml` doesn't match any scanned cluster — override silently ignored", name),
			Body:       fmt.Sprintf("The plan-inputs `clusters:` map names `%s`, but the state file contains no cluster with that name. The override block has no effect; either the cluster name is a typo, or the state file is from a different source than expected.", name),
			HowToClose: fmt.Sprintf("Either correct the cluster name under `clusters:` in `plan-inputs.yaml` to match a real scanned cluster, OR remove the `%s` block entirely.", name),
		})
	}
	return oqs
}

// detectAmbiguousClusterOverrides surfaces 🟡 OQs for plan-inputs cluster
// keys that match more than one scanned cluster (same Name across
// regions, possible per scanner output). applyClusterDeclarations skips
// both overlay and synthesis for ambiguous names — declared fields are
// silently dropped — so the customer needs a signal to disambiguate
// (typically by renaming on the source side, or by scoping the plan
// run to one region's state file).
func detectAmbiguousClusterOverrides(state types.ProcessedState, inputs types.PlanInputsResolved) []types.OpenQuestion {
	if inputs.Raw == nil || len(inputs.Raw.Clusters) == 0 {
		return nil
	}
	counts := map[string]int{}
	for i := range state.Sources {
		if state.Sources[i].MSKData == nil {
			continue
		}
		for j := range state.Sources[i].MSKData.Regions {
			for k := range state.Sources[i].MSKData.Regions[j].Clusters {
				counts[state.Sources[i].MSKData.Regions[j].Clusters[k].Name]++
			}
		}
	}
	var ambiguous []string
	for name := range inputs.Raw.Clusters {
		if counts[name] > 1 {
			ambiguous = append(ambiguous, name)
		}
	}
	if len(ambiguous) == 0 {
		return nil
	}
	sort.Strings(ambiguous)
	oqs := make([]types.OpenQuestion, 0, len(ambiguous))
	for _, name := range ambiguous {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "cluster_override_ambiguous",
			Title:      fmt.Sprintf("`clusters[%s]` matches %d scanned clusters with the same name — override silently dropped", name, counts[name]),
			Body:       fmt.Sprintf("The plan-inputs `clusters:` map names `%s`, and the state file contains %d clusters with that name (across different regions). The override could non-deterministically apply to the wrong one, so kcp dropped it instead of guessing.", name, counts[name]),
			HowToClose: "Either run `kcp report plan` against a state file scoped to a single region so the name is unique, OR rename the source clusters so each carries a distinct Name, then re-scan and re-run.",
		})
	}
	return oqs
}

// computeCutoverOverrides walks each cluster, layers its
// `clusters[<name>].downtime_tolerance` / `.sub_pattern` override on
// the global inputs, and emits an override entry whenever the resolved
// style or sub_pattern differs from the fleet default. Gateway
// mediation is recomputed per cluster too — a Blue/Green override
// flips mediation to N/A even when the fleet mediates via the gateway.
// When the override value isn't in the recognised enum, the entry
// carries OverrideRejected + RejectedOverrideValue so a JSON consumer
// can detect rejected overrides structurally (mirrors AuthDecision).
func computeCutoverOverrides(clusters []types.ProcessedCluster, fleet types.CutoverDecision, inputs types.PlanInputsResolved) []types.ClusterCutoverOverride {
	if inputs.Raw == nil || len(inputs.Raw.Clusters) == 0 {
		return nil
	}
	var out []types.ClusterCutoverOverride
	for _, c := range clusters {
		raw, ok := inputs.Raw.Clusters[c.Name]
		if !ok || (raw.DowntimeTolerance == nil && raw.SubPattern == nil) {
			continue
		}
		clusterInputs := applyClusterOverride(inputs, inputs.Raw, c.Name)
		dec := decideCutover([]types.ProcessedCluster{c}, clusterInputs)
		// Gateway prereqs are fleet-scoped — a per-cluster override
		// can't earn its own gateway path. Inherit the fleet's
		// mediation verdict unless the override is Blue/Green, which
		// sidesteps the gateway entirely (N/A). We can't reuse
		// `dec.GatewayMediated` directly because `decideCutover` ran
		// against a single-cluster slice and `fleetUsesIAM` would
		// have re-evaluated against that one cluster's auth instead
		// of the whole fleet — producing wrong mediation for mixed
		// IAM / non-IAM fleets. (See `detectPerClusterGatewayIncompat`
		// for the separate OQ that surfaces the cross-check.)
		mediated := fleet.GatewayMediated
		if dec.Style == types.CutoverBlueGreen {
			mediated = types.GatewayMediatedNotApplicable
		}
		rejected, rejectedValue := rejectedCutoverOverride(raw)
		styleMatchesFleet := dec.Style == fleet.Style && dec.SubPattern == fleet.SubPattern && mediated == fleet.GatewayMediated
		if styleMatchesFleet && !rejected {
			continue
		}
		out = append(out, types.ClusterCutoverOverride{
			ClusterID:             c.Name,
			Style:                 dec.Style,
			SubPattern:            dec.SubPattern,
			GatewayMediated:       mediated,
			OverrideRejected:      rejected,
			RejectedOverrideValue: rejectedValue,
		})
	}
	return out
}

// detectPerClusterGatewayIncompat fires when a per-cluster
// `downtime_tolerance: seconds_per_service` override exists but the
// fleet's gateway isn't mediated (gateway prereqs are fleet-scoped, so
// a per-cluster override can't get its own gateway path). Without
// this, the customer's per-cluster choice is silently lost — the
// cluster falls back to the fleet's plain Cluster Linking shape and
// the reader has no signal.
func detectPerClusterGatewayIncompat(clusters []types.ProcessedCluster, fleet types.CutoverDecision, inputs types.PlanInputsResolved) []types.OpenQuestion {
	if fleet.GatewayMediated == types.GatewayMediatedTrue {
		return nil
	}
	var oqs []types.OpenQuestion
	for _, name := range sortedKnownClusterOverrideNames(clusters, inputs) {
		cluster := inputs.Raw.Clusters[name]
		if cluster.DowntimeTolerance == nil || *cluster.DowntimeTolerance != DowntimeSecondsPerService {
			continue
		}
		oqs = append(oqs, types.OpenQuestion{
			ID:        "downtime_tolerance_requires_gateway",
			ClusterID: name,
			Title:     fmt.Sprintf("`clusters[%s].downtime_tolerance: seconds_per_service` requires the gateway but the fleet's recommendation is plain Cluster Linking", name),
			Body: "seconds_per_service downtime tolerance requires CC-Gateway mediation, and gateway prereqs are fleet-scoped — there's no per-cluster gateway path. " +
				"The per-cluster override is honoured for the cutover style (Stop-Restart-Repeat), but the sub-minute window depends on the fleet committing to the gateway path.",
			HowToClose: fmt.Sprintf("Either advance the fleet's gateway prereqs in `plan-inputs.yaml`:\n```yaml\nprefer_gateway: true                            # opt-in; kcp default is false (plain Cluster Linking)\nconfluent_for_kubernetes_status: in_progress    # not_started | in_progress | complete\ncc_gateway_license_status:       in_progress    # not_started | in_progress | complete\n```\nOR relax the per-cluster downtime requirement:\n```yaml\nclusters:\n  %s:\n    downtime_tolerance: minutes_per_service     # zero | seconds_per_service | minutes_per_service | scheduled_window_sequential | scheduled_window_all_at_once | let_confluent_choose\n```", name),
		})
	}
	return oqs
}

// bgOverrideClusterNames returns the cluster names that override to
// Blue/Green — the only style that sidesteps the gateway-mediation
// question. Used by detectCutoverOpenQuestions to add a clarifying
// note to fleet-wide gateway OQs so the reader doesn't think the OQ
// applies to gateway-exempt clusters too.
func bgOverrideClusterNames(overrides []types.ClusterCutoverOverride) []string {
	var out []string
	for _, o := range overrides {
		if o.Style == types.CutoverBlueGreen {
			out = append(out, o.ClusterID)
		}
	}
	return out
}

// rejectedCutoverOverride reports whether the per-cluster override
// values are outside the recognised enum. Returns the first rejected
// value found (downtime_tolerance takes precedence over sub_pattern)
// for the renderer / JSON consumer to display.
func rejectedCutoverOverride(raw types.ClusterPlanInputs) (bool, string) {
	if raw.DowntimeTolerance != nil && !knownDowntimeTolerance(*raw.DowntimeTolerance) {
		return true, *raw.DowntimeTolerance
	}
	if raw.SubPattern != nil && !knownCutoverSubPattern(*raw.SubPattern) {
		return true, *raw.SubPattern
	}
	return false, ""
}

// detectClusterCutoverOpenQuestions emits a per-cluster OQ when a
// `clusters[<name>].downtime_tolerance` (or `.sub_pattern`) override
// is typo'd. Mirrors the per-cluster `target_auth_method` typo OQ —
// without it, the typo silently falls back to a default and the
// reader has no idea why this cluster doesn't override. Only emits
// for cluster names that match an actual scanned cluster;
// unknown-name overrides are handled by
// detectUnknownClusterOverrides.
func detectClusterCutoverOpenQuestions(clusters []types.ProcessedCluster, inputs types.PlanInputsResolved) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	for _, name := range sortedKnownClusterOverrideNames(clusters, inputs) {
		cluster := inputs.Raw.Clusters[name]
		if cluster.DowntimeTolerance != nil {
			value := *cluster.DowntimeTolerance
			if !knownDowntimeTolerance(value) {
				oqs = append(oqs, types.OpenQuestion{
					ID:         "downtime_tolerance_unknown",
					ClusterID:  name,
					Title:      fmt.Sprintf("`clusters[%s].downtime_tolerance: %s` is not a recognised value — treated as `let_confluent_choose` (Stop-Restart-Repeat) for this cluster", name, value),
					Body:       "The Plan only recognises `zero | seconds_per_service | minutes_per_service | scheduled_window_sequential | scheduled_window_all_at_once | let_confluent_choose`. The override falls outside the enum, so this cluster's cutover style resolves to the Confluent default (Stop-Restart-Repeat) — note this can DIFFER from the fleet-wide default if the fleet itself selected another style, in which case this cluster appears in the **Per-cluster overrides** sub-list above.",
					HowToClose: fmt.Sprintf("Set `clusters[%s].downtime_tolerance` in `plan-inputs.yaml` to one of the recognised values, OR remove the override to keep the fleet default.", name),
				})
			}
		}
		if cluster.SubPattern != nil {
			value := *cluster.SubPattern
			if !knownCutoverSubPattern(value) {
				oqs = append(oqs, types.OpenQuestion{
					ID:         "sub_pattern_unknown",
					ClusterID:  name,
					Title:      fmt.Sprintf("`clusters[%s].sub_pattern: %s` is not a recognised value — `app-by-app` applied for this cluster", name, value),
					Body:       "The Plan only recognises `app-by-app | topic-by-topic` for the Stop-Restart-Repeat sub-pattern. The override falls outside the enum, so the cluster inherits the default `app-by-app` cadence.",
					HowToClose: fmt.Sprintf("Set `clusters[%s].sub_pattern` in `plan-inputs.yaml` to `app-by-app` or `topic-by-topic`, OR remove the override.", name),
				})
			}
		}
	}
	return oqs
}

// pendingPrereqList renders the list of gateway prereqs still at
// `not_started`, for inclusion in an OQ body. Inline rather than a
// helper-with-cases because the body string is one-shot per Plan.
func pendingPrereqList(inputs types.PlanInputsResolved, _ bool) string {
	var pending []string
	if inputs.ConfluentForKubernetesStatus == PrereqNotStarted {
		pending = append(pending, "`confluent_for_kubernetes_status`")
	}
	if inputs.CCGatewayLicenseStatus == PrereqNotStarted {
		pending = append(pending, "`cc_gateway_license_status`")
	}
	if len(pending) == 0 {
		return "(none — but eligibility check failed for another reason)"
	}
	return strings.Join(pending, ", ")
}

// openQuestionPriority is a thin shim over the OQ registry — keeps the
// sort callsite in Build readable without a direct map lookup. New OQ
// IDs land in `oqRegistry` (see oq_registry.go), not here.
func openQuestionPriority(id string) int {
	return oqMetaFor(id).Priority
}

// backfillAggregates populates `ClusterMetrics.Aggregates` from raw
// metric series in-place across every MSK cluster in `state`. Runs
// once at the top of `Build` so `collectClusters` (and the fleet-wide
// detectors that call it) read pre-populated maps without recomputing
// CalculateMetricsAggregates per invocation. Skips clusters where
// Aggregates is already populated (e.g. test fixtures that pre-set it)
// or where there are no raw metrics to fold.
func backfillAggregates(state *types.ProcessedState) {
	for i := range state.Sources {
		if state.Sources[i].MSKData == nil {
			continue
		}
		for j := range state.Sources[i].MSKData.Regions {
			for k := range state.Sources[i].MSKData.Regions[j].Clusters {
				c := &state.Sources[i].MSKData.Regions[j].Clusters[k]
				if len(c.ClusterMetrics.Aggregates) == 0 && len(c.ClusterMetrics.Metrics) > 0 {
					c.ClusterMetrics.Aggregates = report.CalculateMetricsAggregates(c.ClusterMetrics.Metrics)
				}
			}
		}
	}
}

func collectClusters(state types.ProcessedState) []types.ProcessedCluster {
	var out []types.ProcessedCluster
	for _, src := range state.Sources {
		if src.MSKData == nil {
			continue
		}
		for _, region := range src.MSKData.Regions {
			out = append(out, region.Clusters...)
		}
	}
	return out
}

func countRegions(state types.ProcessedState) int {
	regions := map[string]struct{}{}
	for _, src := range state.Sources {
		if src.MSKData != nil {
			for _, r := range src.MSKData.Regions {
				regions[r.Name] = struct{}{}
			}
		}
	}
	return len(regions)
}

// regionFlag returns " --region <region>" when region is non-empty,
// otherwise an empty string — so HowToClose commands rendered against a
// hand-edited or partially-populated state file don't produce an invalid
// `--region ` flag with a trailing empty value.
func regionFlag(region string) string {
	if region == "" {
		return ""
	}
	return " --region " + region
}

func topicCount(c types.ProcessedCluster) int {
	if c.KafkaAdminClientInformation.Topics == nil {
		return 0
	}
	return c.KafkaAdminClientInformation.Topics.Summary.Topics
}

func brokerCount(c types.ProcessedCluster) int {
	return len(c.AWSClientInformation.Nodes)
}

func buildSizingAppendix(sizings []types.ClusterSizing, cfg *PlanConfig, inputs types.PlanInputsResolved) []types.SizingMathDetail {
	caps := cfg.EnterpriseCaps
	out := make([]types.SizingMathDetail, 0, len(sizings))
	for _, s := range sizings {
		if s.Degraded {
			continue
		}
		out = append(out, types.SizingMathDetail{
			ClusterID: s.ClusterID,
			Formula: fmt.Sprintf("CEIL(max(%sIn/%d, %sOut/%d, partitions/%d) * (1 + %.2f headroom))",
				percentileHeader(inputs.SizingPercentile), caps.PerECKUIngressMBps,
				percentileHeader(inputs.SizingPercentile), caps.PerECKUEgressMBps,
				caps.PerECKUPartitionRate, inputs.HeadroomFraction),
			IntermediateSteps: []string{
				fmt.Sprintf("ingress ratio = %.2f MBps / %d = %.4f", s.SizedInMBps, caps.PerECKUIngressMBps, s.IngressRatio),
				fmt.Sprintf("egress ratio  = %.2f MBps / %d = %.4f", s.SizedOutMBps, caps.PerECKUEgressMBps, s.EgressRatio),
				fmt.Sprintf("partition ratio = %d / %d = %.4f", s.UserPartitions, caps.PerECKUPartitionRate, s.PartitionRatio),
				fmt.Sprintf("max ratio = %.4f", s.MaxRatio),
				fmt.Sprintf("sized = CEIL(%.4f * %.2f) = %d  (1 + %.2f headroom)", s.MaxRatio, 1+inputs.HeadroomFraction, s.SizedECKU, inputs.HeadroomFraction),
				fmt.Sprintf("final = max(%d sized, %d SLA floor) = %d eCKU", s.SizedECKU, s.SLAFloorECKU, s.FinalECKU),
			},
			Citations: s.Citations,
		})
	}
	return out
}
