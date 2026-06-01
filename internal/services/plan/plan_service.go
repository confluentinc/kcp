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
			PlanSchemaVersion: "1-experimental",
		},
		Inputs: inputs,
	}

	for _, c := range clusters {
		// ProcessState doesn't populate Aggregates on the top-level path —
		// only the per-subcommand date-filtered helpers do. Fill it in if
		// the cluster has raw metrics but no aggregates, so sizing has P95.
		if len(c.ClusterMetrics.Aggregates) == 0 && len(c.ClusterMetrics.Metrics) > 0 {
			c.ClusterMetrics.Aggregates = report.CalculateMetricsAggregates(c.ClusterMetrics.Metrics)
		}
		// Per-cluster overrides win over globals — heterogeneous fleets
		// need finer-grained inputs than flipping one global flag across
		// every cluster. We start from the already-resolved global view
		// (`inputs`) and layer any per-cluster overlay via
		// `applyClusterOverride` — that avoids the redundant global
		// re-resolution `ResolvePlanInputsForCluster` would do on every
		// iteration.
		clusterInputs := applyClusterOverride(inputs, inputs.Raw, c.Name)
		sizing := ComputeClusterSizing(c, s.cfg, clusterInputs)
		missing := inputsMissing(c)
		// Each consumer gets its own slice — without Clone, a future
		// `append` to any one InputsMissing would silently mutate the
		// others if cap permits.
		sizing.InputsMissing = slices.Clone(missing)
		ct := DecideClusterType(c, sizing, s.cfg, clusterInputs)
		net := DecideNetworking(sizing, ct, s.cfg, clusterInputs)
		ct.InputsMissing = slices.Clone(missing)
		net.InputsMissing = slices.Clone(missing)

		auth := DecideAuth(c, s.cfg, clusterInputs)

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

	// Cutover is fleet-wide (one Plan = one cutover style). Only emit
	// when there are clusters to migrate; an empty fleet has nothing
	// to cut over.
	if len(clusters) > 0 {
		cutover := DecideCutover(clusters, inputs)
		plan.Cutover = &cutover
		plan.OpenQuestions = append(plan.OpenQuestions, detectCutoverOpenQuestions(cutover, inputs, fleetUsesIAM(clusters))...)
		plan.OpenQuestions = append(plan.OpenQuestions, detectAuthFleetOpenQuestions(inputs)...)
	}
	// Schema migration — fleet-wide; one Plan, one verdict. The
	// `schemaless` branch returns nil so the renderer can omit the
	// whole section cleanly. We still run the OQ detector either way:
	// the strategy-typo / strategy-unknown signals are valuable even
	// when the verdict resolves to no recommendation.
	schema := DecideSchema(state, s.cfg, inputs)
	if schema != nil && !HasPath(schema, types.SchemaPathSchemaless) {
		plan.Schema = schema
	}
	plan.OpenQuestions = append(plan.OpenQuestions, detectSchemaOpenQuestions(schema, s.cfg, inputs)...)
	// Stale-state OQ: surface a fleet-wide accuracy warning when the
	// source state file is older than the freshness window. The Plan
	// still renders against whatever's in state.json — but a 14-day-old
	// snapshot could miss ACL drift, new brokers, etc. that would
	// change the verdicts.
	plan.OpenQuestions = append(plan.OpenQuestions, detectStaleStateOQ(state.Timestamp, s.now(), s.cfg.Thresholds.StaleStateDays)...)

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
	// detected auth methods on a non-serverless cluster — auth is its
	// own concern, surfaced independently of topic / ACL inventory gaps
	// (those have their own OQs and don't suppress this one).
	// Serverless clusters legitimately produce no detected methods
	// when IAM isn't enabled; suppress only for them.
	if len(auth.SourceAuths) == 0 && !isServerless(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "auth_posture_unknown",
			ClusterID:  c.Name,
			Title:      "No client-authentication methods detected on the source — auth migration recommendation is unconfirmed",
			Body:       "Neither `AWSClientInformation.MskClusterConfig.Provisioned.ClientAuthentication` nor `kafka_admin_client_information.sasl_mechanism` reports an enabled auth method. A real MSK Provisioned cluster always has at least one. Likely cause: discover ran without admin credentials (or the state file pre-dates the auth scan).",
			HowToClose: fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account, OR provide admin Kafka credentials to `kcp scan clusters` so the Admin probe can backfill `sasl_mechanism`.", regionFlag(c.Region)),
		})
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
		oqs = append(oqs, types.OpenQuestion{
			ID:        "missing_p95_metrics",
			ClusterID: c.Name,
			Title:     fmt.Sprintf("No %s throughput metrics — sizing fell back to SLA floor", percentileHeader(inputs.SizingPercentile)),
			Body:      sizing.DegradedReason,
			// Metrics are collected by `kcp discover` (CloudWatch path), not a separate `scan metrics` subcommand.
			HowToClose: fmt.Sprintf("Re-run `kcp discover%s` without `--skip-metrics` so CloudWatch metrics get backfilled into the state file.", regionFlag(c.Region)),
		})
	}
	if !aclScanRan(c) && !isServerless(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:        "acls_not_scanned",
			ClusterID: c.Name,
			Title:     "Admin scan didn't populate ACLs — cap-vs-Enterprise rule was skipped",
			Body:      "The `acl_count_exceeds_cap` hard-limit rule needs a successful ACL scan to evaluate. Either the scan didn't run, or `--skip-acls` was passed; without the ACL list the rule is treated as inconclusive and the verdict resolves on the other rules.",
			// `kcp scan clusters` doesn't take --region — it reads region from the state file.
			HowToClose: "Re-run `kcp scan clusters --source-type msk --credentials-file msk-credentials.yaml` without `--skip-acls`. The credentials file is a YAML with the admin Kafka credentials (SASL/IAM or SCRAM) — see `kcp scan clusters --help` for the schema, or [the kcp docs](https://confluentinc.github.io/kcp/command-reference/scan/clusters/) for a sample.\n\nSample `msk-credentials.yaml`:\n```yaml\nclusters:\n  - cluster_arn: <arn>\n    authentication_type: SASL_SCRAM        # or AWS_MSK_IAM\n    sasl_scram_username: <username>        # for SASL/SCRAM\n    sasl_scram_password: <password>\n```",
		})
	}
	if brokerInventoryGap(c) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "broker_inventory_empty",
			ClusterID:  c.Name,
			Title:      "Source environment shows 0 brokers — likely an incomplete scan",
			Body:       "`AWSClientInformation.Nodes` is empty for this cluster. The Source Environment table reads as `Brokers: 0`, which is almost certainly wrong for an MSK Provisioned cluster.",
			HowToClose: fmt.Sprintf("Re-run `kcp discover%s` against the source AWS account to populate the broker inventory.", regionFlag(c.Region)),
		})
	}
	if topicCount(c) == 0 {
		oqs = append(oqs, types.OpenQuestion{
			ID:        "topic_inventory_empty",
			ClusterID: c.Name,
			Title:     "Source environment shows 0 topics — likely an incomplete scan",
			Body:      "`KafkaAdminClientInformation.Topics.Summary.Topics` is 0. The Source Environment table reads as `Topics: 0`, which is almost certainly wrong for a real MSK cluster (system topics alone usually push the count above zero).",
			// `kcp scan clusters` doesn't take --region — it reads region from the state file.
			HowToClose: "Re-run `kcp scan clusters --source-type msk --credentials-file <msk-credentials.yaml>` with admin Kafka credentials (without `--skip-topics`) to populate the topic list.",
		})
	}
	if privateLinkSizingExceedsCap(sizing, ct, net, cfg) {
		cap := cfg.EnterpriseCaps.PrivateLinkMaxECKU
		oqs = append(oqs, types.OpenQuestion{
			ID:         "networking_privatelink_over_cap",
			ClusterID:  c.Name,
			Title:      fmt.Sprintf("PrivateLink trigger fired but cluster sizing exceeds the %d-eCKU PrivateLink cap on Enterprise", cap),
			Body:       fmt.Sprintf("PrivateLink networking on Enterprise is capped at %d eCKU. One or more clusters sized above that cap, and a PrivateLink trigger fired (target_cloud, cc_egress_required, or projected_pni_gateway_count), so the plan still recommends PrivateLink — an infeasible combination above the %d-eCKU cap. See the Sizing & Cluster Decisions table above for each affected cluster's final eCKU.", cap, cap),
			HowToClose: fmt.Sprintf("Raise this with your Confluent account team. Dedicated supports PrivateLink without the %d-eCKU Enterprise cap and is the most likely path to keep both the PrivateLink requirement and the throughput.", cap),
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

// detectCutoverOpenQuestions surfaces fleet-wide gateway-intent / prereq
// gaps. Emitted once per Plan (no ClusterID — these aren't per-cluster).
// Three independent OQs can fire:
//   - gateway intent ambiguous (degraded_awaiting_oq)
//   - gateway prereqs pending (degraded_prereqs_pending)
//   - seconds_per_service tolerance requires the gateway, but it isn't
//     mediated for some reason (cross-check; message varies by cause).
//
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

func detectCutoverOpenQuestions(cutover types.CutoverDecision, inputs types.PlanInputsResolved, iamInUse bool) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	if !knownDowntimeTolerance(inputs.DowntimeTolerance) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "downtime_tolerance_unknown",
			Title:      fmt.Sprintf("`downtime_tolerance: %s` is not a recognised value — defaulted to Stop-Restart-Repeat", inputs.DowntimeTolerance),
			Body:       "The Plan only recognises `zero | seconds_per_service | minutes_per_service | scheduled_window_sequential | scheduled_window_all_at_once | let_confluent_choose`. The current value falls outside the enum, so the Plan inherits the Confluent default (Stop-Restart-Repeat) silently — which is probably not what you intended.",
			HowToClose: "Set `downtime_tolerance` in `plan-inputs.yaml` to one of the recognised values, then re-run `kcp report plan`.",
		})
	}
	switch cutover.RecommendationStatus {
	case types.RecommendationDegradedAwaitingOQ:
		oqs = append(oqs, types.OpenQuestion{
			ID:         "gateway_intent_unconfirmed",
			Title:      "Gateway intent — pick CC Gateway or plain Cluster Linking",
			Body:       "`prefer_gateway: true` (default) AND all three gateway prereqs (`confluent_for_kubernetes_status`, `cc_gateway_license_status`, `iam_pre_migration_status`) are at `not_started`. Both paths are fully supported — the Plan just needs you to pick. Plain Cluster Linking applies while this is open.",
			HowToClose: "In `plan-inputs.yaml`, either (a) set `prefer_gateway: false` to commit to plain Cluster Linking, OR (b) move at least one gateway prereq to `in_progress` to commit to the gateway path. Re-run `kcp report plan` to clear the OQ.",
		})
	case types.RecommendationDegradedPrereqsPending:
		oqs = append(oqs, types.OpenQuestion{
			ID:         "gateway_prereqs_pending",
			Title:      "Gateway prereqs — pending items before the gateway path can be recommended",
			Body:       fmt.Sprintf("`prefer_gateway: true` and at least one gateway prereq is still at `not_started`: %s. The gateway-mediated path needs all applicable prereqs at `in_progress` or `complete`. Plain Cluster Linking applies until they advance.", pendingPrereqList(inputs, iamInUse)),
			HowToClose: "Move each pending prereq above to `in_progress` (intent declared) or `complete` (done). Re-run `kcp report plan` once the prereqs advance.",
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
			HowToClose: "Adjust either `prefer_gateway` / the gateway prereq statuses, or `downtime_tolerance`, in `plan-inputs.yaml`.",
		})
	}
	return oqs
}

// detectAuthFleetOpenQuestions surfaces auth OQs that come from
// invalid customer input — the global `target_auth_method` typo
// (fleet-level OQ, single emit) and any per-cluster
// `clusters[<name>].target_auth_method` typos (per-cluster OQs so the
// affected cluster is obvious). Per-cluster typos silently fall back
// to the per-source default in DecideAuth via effectiveTarget, so
// without this detector they're invisible to the customer.
func detectAuthFleetOpenQuestions(inputs types.PlanInputsResolved) []types.OpenQuestion {
	var oqs []types.OpenQuestion
	if !knownTargetAuthMethod(inputs.TargetAuthMethod) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "target_auth_method_unknown",
			Title:      fmt.Sprintf("`target_auth_method: %s` is not a recognised value — per-source defaults applied instead", inputs.TargetAuthMethod),
			Body:       fmt.Sprintf("The Plan only recognises `%s | %s | %s`. The current value falls outside the enum; the per-source `auth_mapping` default is used silently for every cluster.", TargetAuthAPIKeys, TargetAuthMTLS, TargetAuthOAuth),
			HowToClose: "Set `target_auth_method` in `plan-inputs.yaml` (or `clusters[<name>].target_auth_method` for a per-cluster override) to one of the recognised values, OR unset it to keep the per-source default.",
		})
	}
	if inputs.Raw != nil {
		clusterNames := make([]string, 0, len(inputs.Raw.Clusters))
		for name := range inputs.Raw.Clusters {
			clusterNames = append(clusterNames, name)
		}
		sort.Strings(clusterNames)
		for _, name := range clusterNames {
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
	}
	return oqs
}

// pendingPrereqList renders the list of gateway prereqs still at
// `not_started`, for inclusion in an OQ body. Inline rather than a
// helper-with-cases because the body string is one-shot per Plan.
func pendingPrereqList(inputs types.PlanInputsResolved, iamInUse bool) string {
	var pending []string
	if inputs.ConfluentForKubernetesStatus == PrereqNotStarted {
		pending = append(pending, "`confluent_for_kubernetes_status`")
	}
	if inputs.CCGatewayLicenseStatus == PrereqNotStarted {
		pending = append(pending, "`cc_gateway_license_status`")
	}
	if iamInUse && inputs.IAMPreMigrationStatus == PrereqNotStarted {
		pending = append(pending, "`iam_pre_migration_status`")
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
