package plan

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/types"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
)

// Stable Red Flag IDs. Matches the spec's row numbering — kept stable
// so JSON consumers can branch on the ID without parsing the title.
const (
	RedFlagIDSchemalessSource          = "schemaless_source"
	RedFlagIDKafkaVersionBelowCLFloor  = "kafka_version_below_cl_floor"
	RedFlagIDIAMAuthEnabled            = "iam_auth_enabled"
	RedFlagIDGlueSRInUse               = "glue_sr_in_use"
	RedFlagIDPartitionApproachingCap   = "partition_approaching_cap"
	RedFlagIDMSKConnectPresent         = "msk_connect_present"
	RedFlagIDSelfManagedConnectPresent = "self_managed_connect_present"
	RedFlagIDMultiRegionSource         = "multi_region_source"
	RedFlagIDZeroACLsWithIAM           = "zero_acls_with_iam"
	RedFlagIDClientInventoryGap        = "client_inventory_gap"
	RedFlagIDMSKExpressBrokerTier      = "msk_express_broker_tier"
	RedFlagIDTieredStorageInUse        = "tiered_storage_in_use"
	RedFlagIDEOSInUse                  = "eos_in_use"
	RedFlagIDKafkaStreamsInUse         = "kafka_streams_in_use"
	RedFlagIDBroadTopicPatternMatch    = "broad_topic_pattern_match"
)

// expressInstanceFamilies are the MSK Express broker instance-type
// prefixes. Express tier is a Cluster-Linking compatibility caveat —
// row 11 surfaces it for SE discussion.
var expressInstanceFamilies = []string{"express.m7g."}

// broadTopicPatterns drives row 15: catch-all naming patterns the
// structured Connect / Streams scans (rows 6, 7, 14) may miss when
// topics use custom prefixes. Each entry pairs a regex with a
// human-readable label surfaced in the rendered evidence string.
var broadTopicPatterns = []struct {
	label string
	re    *regexp.Regexp
}{
	{label: "MM2 active replication (`mm2-` prefix)", re: regexp.MustCompile(`^mm2-`)},
	{label: "MM2 active replication (`.replica` suffix)", re: regexp.MustCompile(`\.replica$`)},
	{label: "Connect fleet (`connect-(configs|offsets|status)`, with or without a prefix)", re: connectInternalTopicPattern},
	{label: "Kafka Streams changelog (`-changelog`)", re: regexp.MustCompile(`-changelog$`)},
	{label: "Kafka Streams repartition (`-repartition`)", re: regexp.MustCompile(`-repartition$`)},
	{label: "Kafka transactions (`__transaction_state`)", re: regexp.MustCompile(`^__transaction_state$`)},
	{label: "Connector heartbeats (`-heartbeats`)", re: regexp.MustCompile(`-heartbeats$`)},
}

// DetectRedFlags evaluates the 15 boolean trigger rows from the spec.
// Returns nil when there are no clusters in the state file (the
// renderer omits the section in that case). Each row is evaluated
// independently and produces a {Status, Evidence} pair — Triggered
// rows render at the top of §Red Flags, with NotTriggered / Unknown
// collapsed into a tail summary.
func DetectRedFlags(state types.ProcessedState, plan *types.Plan, cfg *PlanConfig, inputs types.PlanInputsResolved) *types.RedFlagsSection {
	clusters := collectMSKClusters(state)
	if len(clusters) == 0 {
		return nil
	}
	rows := []types.RedFlag{
		evalSchemalessSource(plan, inputs),
		evalKafkaVersionBelowFloor(clusters, cfg),
		evalIAMAuthEnabled(clusters),
		evalGlueSRInUse(plan),
		evalPartitionApproachingCap(plan, cfg),
		evalMSKConnectPresent(clusters),
		evalSelfManagedConnectPresent(clusters),
		evalMultiRegionSource(state),
		evalZeroACLsWithIAM(clusters),
		evalClientInventoryGap(clusters),
		evalMSKExpressBrokerTier(clusters),
		evalTieredStorageInUse(clusters),
		evalEOSInUse(inputs),
		evalKafkaStreamsInUse(clusters, inputs),
		evalBroadTopicPatternMatch(clusters),
	}
	return &types.RedFlagsSection{Rows: rows}
}

// collectMSKClusters flattens ProcessedState into the per-cluster
// view the rule evaluators want — same shape collectClusters in
// plan_service.go uses, but kept private here so the V2 module isn't
// coupled to the orchestrator's internal helper.
func collectMSKClusters(state types.ProcessedState) []types.ProcessedCluster {
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

// ----- Row 1: schemaless source -----

// Triggered when no Schema Registry was detected AND the customer
// declared a non-unknown schema_strategy. While `schema_strategy ==
// unknown` the row is suppressed to avoid a spurious first-run warning.
func evalSchemalessSource(plan *types.Plan, inputs types.PlanInputsResolved) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDSchemalessSource, Title: "Schemaless source (no Schema Registry detected)"}
	strategy := inputs.SchemaStrategy
	if strategy == "" || strategy == SchemaStrategyUnknown {
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "`schema_strategy` not yet declared — set it in `plan-inputs.yaml` to evaluate this row"
		return rf
	}
	if plan.Schema == nil || plan.Schema.Source == types.SchemaSourceNone {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = fmt.Sprintf("`schema_strategy: %s`, no Schema Registry detected by `kcp scan schema-registry` / `kcp scan glue-schema-registry`", strategy)
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 2: Kafka version below the Cluster Linking floor -----

// `kafka_version < 2.4.0` per the published Cluster Linking floor.
// Versions are dot-separated integer segments; the comparator
// (`versionAtLeast`) already strips pre-release suffixes and handles
// the "latest" alias.
func evalKafkaVersionBelowFloor(clusters []types.ProcessedCluster, cfg *PlanConfig) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDKafkaVersionBelowCLFloor, Title: "Kafka version below the Cluster Linking floor"}
	floor := cfg.ClusterLinking.SourceMinKafkaVersion
	type versionHit struct {
		Cluster string `json:"cluster"`
		Version string `json:"version"`
	}
	var below []versionHit
	var belowStrs []string
	var unparseable []string
	for _, c := range clusters {
		v := kafkaVersionOf(c)
		if v == "" {
			unparseable = append(unparseable, c.Name+" (no version recorded)")
			continue
		}
		if !versionAtLeast(v, floor) {
			below = append(below, versionHit{Cluster: c.Name, Version: v})
			belowStrs = append(belowStrs, fmt.Sprintf("%s=%s", c.Name, v))
		}
	}
	switch {
	case len(below) > 0:
		rf.Status = types.RedFlagTriggered
		rf.Evidence = fmt.Sprintf("clusters below floor `%s`: %s", floor, strings.Join(belowStrs, ", "))
		rf.EvidenceFields = map[string]any{
			"floor":    floor,
			"clusters": below,
		}
	case len(unparseable) > 0:
		rf.Status = types.RedFlagUnknown
		rf.Evidence = fmt.Sprintf("kafka_version missing for: %s — re-run `kcp discover`", strings.Join(unparseable, ", "))
	default:
		rf.Status = types.RedFlagNotTriggered
	}
	return rf
}

func kafkaVersionOf(c types.ProcessedCluster) string {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil || prov.CurrentBrokerSoftwareInfo == nil || prov.CurrentBrokerSoftwareInfo.KafkaVersion == nil {
		return ""
	}
	return *prov.CurrentBrokerSoftwareInfo.KafkaVersion
}

// ----- Row 3: IAM auth enabled -----

func evalIAMAuthEnabled(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDIAMAuthEnabled, Title: "IAM authentication enabled on the source"}
	var hits []string
	for _, c := range clusters {
		for _, a := range sourceAuthsDetected(c) {
			if a == SourceAuthIAM {
				hits = append(hits, c.Name)
				break
			}
		}
	}
	if len(hits) > 0 {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "IAM detected on: " + strings.Join(hits, ", ")
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 4: AWS Glue Schema Registry in use -----

func evalGlueSRInUse(plan *types.Plan) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDGlueSRInUse, Title: "AWS Glue Schema Registry in use"}
	if plan.Schema == nil {
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "schema migration verdict unavailable"
		return rf
	}
	if HasPath(plan.Schema, types.SchemaPathMigrateGlue) ||
		plan.Schema.Source == types.SchemaSourceGlue ||
		plan.Schema.Source == types.SchemaSourceConfluentAndGlue {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = fmt.Sprintf("Glue registries detected: %s", strings.Join(plan.Schema.GlueRegistries, ", "))
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 5: partition count approaching the cluster's partition ceiling -----

// Triggered when any cluster's `user_partitions` exceeds 30% of the
// sized eCKU's per-eCKU partition cap. Dynamic to the cluster's
// sized footprint, not the Enterprise PNI ceiling — so a 5-eCKU
// cluster trips at 4,500 partitions; a 32-eCKU cluster at 28,800.
func evalPartitionApproachingCap(plan *types.Plan, cfg *PlanConfig) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDPartitionApproachingCap, Title: "Partition count approaching the cluster's sized partition ceiling"}
	if cfg.EnterpriseCaps.PerECKUPartitionRate <= 0 {
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "config: per_eCKU_partition_rate missing"
		return rf
	}
	var hits []string
	for _, s := range plan.Sizing {
		if s.FinalECKU <= 0 || s.UserPartitions <= 0 {
			continue
		}
		threshold := 0.30 * float64(cfg.EnterpriseCaps.PerECKUPartitionRate) * float64(s.FinalECKU)
		if float64(s.UserPartitions) > threshold {
			hits = append(hits, fmt.Sprintf("%s=%d partitions (%.0f%% of %d eCKU sized capacity)",
				s.ClusterID, s.UserPartitions,
				100.0*float64(s.UserPartitions)/(float64(cfg.EnterpriseCaps.PerECKUPartitionRate)*float64(s.FinalECKU)),
				s.FinalECKU))
		}
	}
	if len(hits) > 0 {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = strings.Join(hits, "; ")
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 6: MSK Connect managed connectors present -----

// Distinguishes "scan ran successfully with 0 connectors" (don't fire)
// from "scan didn't run" (fire as Unknown). The disambiguation reads
// topic-population + cluster type per the spec: a PROVISIONED cluster
// with topics populated AND an empty `connectors` slice counts as
// "actually zero".
func evalMSKConnectPresent(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDMSKConnectPresent, Title: "MSK Connect managed connectors present"}
	var triggered []string
	var unscanned []string
	for _, c := range clusters {
		connectors := c.AWSClientInformation.Connectors
		if len(connectors) > 0 {
			triggered = append(triggered, fmt.Sprintf("%s (%d connector(s))", c.Name, len(connectors)))
			continue
		}
		if isServerless(c) {
			// MSK Connect doesn't apply to Serverless — treat as 0.
			continue
		}
		// PROVISIONED + topics populated AND empty slice → actually 0.
		if c.KafkaAdminClientInformation.Topics != nil && len(c.KafkaAdminClientInformation.Topics.Details) > 0 {
			continue
		}
		// Otherwise: can't disambiguate scan-didn't-run from really-zero.
		unscanned = append(unscanned, c.Name)
	}
	switch {
	case len(triggered) > 0:
		rf.Status = types.RedFlagTriggered
		rf.Evidence = strings.Join(triggered, ", ")
	case len(unscanned) > 0:
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "scan inconclusive for: " + strings.Join(unscanned, ", ")
	default:
		rf.Status = types.RedFlagNotTriggered
	}
	return rf
}

// ----- Row 7: self-managed Connect clusters present -----

// Reads `KafkaAdminClientInformation.SelfManagedConnectors` directly.
// Same nil-vs-empty disambiguation as row 6: empty struct with topics
// populated counts as "no Connect clusters"; nil struct = scan didn't
// run.
func evalSelfManagedConnectPresent(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDSelfManagedConnectPresent, Title: "Self-managed Connect clusters present"}
	var triggered []string
	var unscanned []string
	for _, c := range clusters {
		smc := c.KafkaAdminClientInformation.SelfManagedConnectors
		switch {
		case smc != nil && len(smc.Connectors) > 0:
			triggered = append(triggered, fmt.Sprintf("%s (%d connector(s))", c.Name, len(smc.Connectors)))
		case smc != nil:
			// Empty struct with `Connectors` cleared — scan ran, found nothing.
		default:
			// nil — scan didn't run. Cross-check topic patterns for
			// `connect-(configs|offsets|status)` so a stale state file
			// doesn't suppress a real fleet.
			if patternHit, _ := topicPatternFound(c, connectInternalTopicPattern); patternHit {
				triggered = append(triggered, c.Name+" (Connect topic pattern detected; SelfManagedConnectors scan didn't run)")
			} else {
				unscanned = append(unscanned, c.Name)
			}
		}
	}
	switch {
	case len(triggered) > 0:
		rf.Status = types.RedFlagTriggered
		rf.Evidence = strings.Join(triggered, ", ")
	case len(unscanned) > 0:
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "scan inconclusive for: " + strings.Join(unscanned, ", ")
	default:
		rf.Status = types.RedFlagNotTriggered
	}
	return rf
}

// ----- Row 8: multi-region source -----

func evalMultiRegionSource(state types.ProcessedState) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDMultiRegionSource, Title: "Source clusters span multiple AWS regions"}
	regions := map[string]struct{}{}
	for _, src := range state.Sources {
		if src.MSKData == nil {
			continue
		}
		for _, r := range src.MSKData.Regions {
			regions[r.Name] = struct{}{}
		}
	}
	if len(regions) > 1 {
		names := make([]string, 0, len(regions))
		for r := range regions {
			names = append(names, r)
		}
		// Sort: map iteration is randomized in Go, so without this
		// two consecutive `kcp report plan` runs produce different
		// evidence-string orderings. Deterministic output is
		// load-bearing for the "same state file + same plan-inputs
		// → byte-identical plan" guarantee.
		sort.Strings(names)
		rf.Status = types.RedFlagTriggered
		rf.Evidence = fmt.Sprintf("%d regions: %s", len(regions), strings.Join(names, ", "))
		rf.EvidenceFields = map[string]any{
			"region_count": len(names),
			"regions":      names,
		}
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 9: zero ACLs with IAM enabled -----

// IAM-only clusters legitimately have zero ACLs (IAM policy carries
// the auth/authz model), but a non-IAM cluster with zero ACLs may be
// a scan gap — the row fires only when IAM is present AND the
// ACL-list disambiguation says "actually zero".
//
// Delegates the nil-vs-empty disambiguation to `aclScanRan` (see
// cluster_signals.go) so semantics stay in lockstep with V1 — when
// `aclScanRan` returns true the scan succeeded; an empty slice in
// that case is "actually zero", not "scan didn't run".
func evalZeroACLsWithIAM(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDZeroACLsWithIAM, Title: "Zero ACLs with IAM auth enabled — verify SE expected behavior"}
	var hits []string
	for _, c := range clusters {
		iam := false
		for _, a := range sourceAuthsDetected(c) {
			if a == SourceAuthIAM {
				iam = true
				break
			}
		}
		if !iam {
			continue
		}
		if aclScanRan(c) && len(c.KafkaAdminClientInformation.Acls) == 0 {
			hits = append(hits, c.Name)
		}
	}
	if len(hits) > 0 {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "IAM + 0 ACLs on: " + strings.Join(hits, ", ")
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 10: client inventory gap -----

func evalClientInventoryGap(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDClientInventoryGap, Title: "Client inventory not populated"}
	var hits []string
	for _, c := range clusters {
		if len(c.DiscoveredClients) == 0 {
			hits = append(hits, c.Name)
		}
	}
	if len(hits) == 0 {
		rf.Status = types.RedFlagNotTriggered
		return rf
	}
	if len(hits) == len(clusters) {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "no `discovered_clients` on any cluster — run `kcp scan client-inventory`"
		return rf
	}
	rf.Status = types.RedFlagTriggered
	rf.Evidence = "missing on: " + strings.Join(hits, ", ")
	return rf
}

// ----- Row 11: MSK Express broker tier -----

func evalMSKExpressBrokerTier(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDMSKExpressBrokerTier, Title: "MSK Express broker tier in use"}
	type expressHit struct {
		Cluster      string `json:"cluster"`
		InstanceType string `json:"instance_type"`
	}
	var hits []expressHit
	var hitStrs []string
	for _, c := range clusters {
		instType := brokerInstanceType(c)
		if instType == "" {
			continue
		}
		for _, family := range expressInstanceFamilies {
			if strings.HasPrefix(instType, family) {
				hits = append(hits, expressHit{Cluster: c.Name, InstanceType: instType})
				hitStrs = append(hitStrs, fmt.Sprintf("%s=%s", c.Name, instType))
				break
			}
		}
	}
	if len(hits) > 0 {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "Express tier on: " + strings.Join(hitStrs, ", ")
		rf.EvidenceFields = map[string]any{"clusters": hits}
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

func brokerInstanceType(c types.ProcessedCluster) string {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil || prov.BrokerNodeGroupInfo == nil || prov.BrokerNodeGroupInfo.InstanceType == nil {
		return ""
	}
	return *prov.BrokerNodeGroupInfo.InstanceType
}

// ----- Row 12: tiered storage in use -----

func evalTieredStorageInUse(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDTieredStorageInUse, Title: "Tiered storage in use on the source"}
	var hits []string
	for _, c := range clusters {
		if clusterStorageMode(c) == kafkatypes.StorageModeTiered {
			hits = append(hits, c.Name)
		}
	}
	if len(hits) > 0 {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "TIERED on: " + strings.Join(hits, ", ") + " — Cluster Linking does NOT carry historical tiered data forward; see Tiered Storage section"
		rf.EvidenceFields = map[string]any{"clusters": hits}
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 13: exactly-once / Kafka transactions in use -----

// No state signal exists for EOS / transactions; returns Unknown
// unless the customer flags it. Surfaces as "not scanned" rather
// than firing false.
func evalEOSInUse(inputs types.PlanInputsResolved) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDEOSInUse, Title: "Exactly-once semantics (EOS) / Kafka transactions in use"}
	if inputs.ExactlyOnceTransactionsInUse == nil {
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "no state signal; declare `exactly_once_transactions_in_use: true|false` in `plan-inputs.yaml`"
		return rf
	}
	if *inputs.ExactlyOnceTransactionsInUse {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "`exactly_once_transactions_in_use: true` declared in plan-inputs"
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 14: Kafka Streams apps consuming -----

// Two signals: explicit customer declaration OR topic-pattern scan
// for `-changelog` / `-repartition` artifacts. Either one fires the
// row; the evidence string names which signal won.
func evalKafkaStreamsInUse(clusters []types.ProcessedCluster, inputs types.PlanInputsResolved) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDKafkaStreamsInUse, Title: "Kafka Streams apps consuming from the source"}
	if inputs.KafkaStreamsInUse != nil && *inputs.KafkaStreamsInUse {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "`kafka_streams_in_use: true` declared in plan-inputs"
		return rf
	}
	changelog := regexp.MustCompile(`-changelog$`)
	repartition := regexp.MustCompile(`-repartition$`)
	var hits []string
	for _, c := range clusters {
		clHit, _ := topicPatternFound(c, changelog)
		rpHit, _ := topicPatternFound(c, repartition)
		if clHit || rpHit {
			hits = append(hits, c.Name)
		}
	}
	if len(hits) > 0 {
		rf.Status = types.RedFlagTriggered
		rf.Evidence = "Streams topic pattern detected on: " + strings.Join(hits, ", ")
		return rf
	}
	// No topic-pattern hits AND no customer declaration → Unknown,
	// matching the row-13 EOS shape: there is no state signal that
	// definitively rules out Streams when topic names use custom
	// suffixes. Customer declaration of `kafka_streams_in_use: false`
	// flips this to NotTriggered explicitly.
	if inputs.KafkaStreamsInUse == nil {
		rf.Status = types.RedFlagUnknown
		rf.Evidence = "no state signal — declare `kafka_streams_in_use: true|false` in `plan-inputs.yaml` if you know"
		return rf
	}
	rf.Status = types.RedFlagNotTriggered
	return rf
}

// ----- Row 15: broad topic-name pattern scan -----

// Catch-all row that runs the structured patterns (mm2, connect-*,
// changelog/repartition, transactions, heartbeats) against every
// topic on every cluster. Surfaces deployments with custom prefixes
// that the structured rows above might miss.
func evalBroadTopicPatternMatch(clusters []types.ProcessedCluster) types.RedFlag {
	rf := types.RedFlag{ID: RedFlagIDBroadTopicPatternMatch, Title: "Broad topic-name pattern scan — items the structured rows might miss"}
	hitsByPattern := map[string][]string{}
	anyHits := false
	for _, c := range clusters {
		if c.KafkaAdminClientInformation.Topics == nil {
			continue
		}
		for _, td := range c.KafkaAdminClientInformation.Topics.Details {
			for _, p := range broadTopicPatterns {
				if p.re.MatchString(td.Name) {
					hitsByPattern[p.label] = append(hitsByPattern[p.label], fmt.Sprintf("%s/%s", c.Name, td.Name))
					anyHits = true
				}
			}
		}
	}
	if !anyHits {
		rf.Status = types.RedFlagNotTriggered
		return rf
	}
	var parts []string
	for _, p := range broadTopicPatterns {
		hits := hitsByPattern[p.label]
		if len(hits) == 0 {
			continue
		}
		// Take the first 3 into a fresh slice so the "(+N more)"
		// suffix doesn't clobber the underlying backing array of
		// hitsByPattern[p.label]. The previous `append(hits[:3], …)`
		// form was an aliasing trap — single-iteration read meant it
		// didn't fire in practice today, but future re-reads would
		// see corrupted lengths.
		const sample = 3
		shown := hits
		if len(hits) > sample {
			shown = append(append([]string(nil), hits[:sample]...), fmt.Sprintf("(+%d more)", len(hits)-sample))
		}
		parts = append(parts, fmt.Sprintf("%s — %s", p.label, strings.Join(shown, ", ")))
	}
	rf.Status = types.RedFlagTriggered
	rf.Evidence = strings.Join(parts, "; ")
	return rf
}
