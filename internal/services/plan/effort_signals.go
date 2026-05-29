package plan

import (
	"regexp"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// Stable Effort Signal IDs. Matches the spec row order.
const (
	EffortSignalIDIAMClientCount           = "iam_to_scram_client_count"
	EffortSignalIDMM2CheckpointTopics      = "mm2_checkpoint_topic_count"
	EffortSignalIDSelfManagedConnectFleets = "self_managed_connect_fleet_count"
	EffortSignalIDGlueSerializerMigration  = "glue_serializer_migration_count"
)

// DetectEffortSignals produces the per-fleet effort signals — the
// four quantitative inputs the customer's PM consumes to scope
// migration effort. Counts only; no day-estimate.
//
// Returns nil when there are no MSK clusters to evaluate (renderer
// omits the section).
func DetectEffortSignals(state types.ProcessedState, plan *types.Plan) *types.EffortSignalsSection {
	clusters := collectMSKClusters(state)
	if len(clusters) == 0 {
		return nil
	}
	signals := []types.EffortSignal{
		signalIAMClientCount(clusters),
		signalMM2CheckpointTopics(clusters),
		signalSelfManagedConnectFleets(clusters),
		signalGlueSerializerMigration(state, clusters),
	}
	return &types.EffortSignalsSection{Signals: signals}
}

// ----- Signal 1: IAM → SCRAM workstream size -----

// Count of `discovered_clients[]` where Auth == "AWS_MSK_IAM" across
// the fleet. This is the count of client apps a customer would need
// to migrate off IAM to use the CC Gateway path (IAM clients can't
// connect to the gateway).
func signalIAMClientCount(clusters []types.ProcessedCluster) types.EffortSignal {
	count := 0
	for _, c := range clusters {
		for _, dc := range c.DiscoveredClients {
			if dc.Auth == "AWS_MSK_IAM" {
				count++
			}
		}
	}
	sig := types.EffortSignal{
		ID:    EffortSignalIDIAMClientCount,
		Label: "IAM → SCRAM workstream size — clients that need re-credentialing before the CC Gateway path",
		Count: count,
	}
	if count == 0 {
		sig.Note = "no clients with `AWS_MSK_IAM` auth detected by `kcp scan client-inventory`; if you have IAM clients, the inventory may be incomplete"
	}
	return sig
}

// ----- Signal 2: MM2 checkpoint topic count -----

// `<source-alias>.checkpoints.internal` is MM2's checkpoint topic
// naming convention. Caveat (spec line 720): MM2 deployments using
// IdentityReplicationPolicy suppress the prefix — those won't show
// up here. The renderer surfaces the caveat in the Note.
var mm2CheckpointPattern = regexp.MustCompile(`\.checkpoints\.internal$`)

func signalMM2CheckpointTopics(clusters []types.ProcessedCluster) types.EffortSignal {
	count := 0
	for _, c := range clusters {
		if c.KafkaAdminClientInformation.Topics == nil {
			continue
		}
		for _, td := range c.KafkaAdminClientInformation.Topics.Details {
			if mm2CheckpointPattern.MatchString(td.Name) {
				count++
			}
		}
	}
	sig := types.EffortSignal{
		ID:    EffortSignalIDMM2CheckpointTopics,
		Label: "MirrorMaker 2 checkpoint topics — replication fleets to enumerate",
		Count: count,
		Note:  "Counts topics matching `*.checkpoints.internal`. MM2 deployments using `IdentityReplicationPolicy` (Confluent-recommended best practice) suppress the prefix entirely and won't be counted here; cross-check with consumer-group naming patterns.",
	}
	return sig
}

// ----- Signal 3: self-managed Connect fleets -----

// Count of distinct fleet prefixes where two of the canonical Connect
// internal topics are present (`<prefix>connect-configs` AND
// `<prefix>connect-status`). The third triad topic
// (`connect-offsets`) may not exist with the same prefix, so we
// require only two of three — AND-of-all-three would miss real
// fleets per the spec.
var connectInternalTopicPattern = regexp.MustCompile(`^(.*?)(connect-(configs|offsets|status))$`)

func signalSelfManagedConnectFleets(clusters []types.ProcessedCluster) types.EffortSignal {
	// Walk every topic, group by prefix, collect which suffixes appear.
	prefixSuffixes := map[string]map[string]struct{}{}
	for _, c := range clusters {
		if c.KafkaAdminClientInformation.Topics == nil {
			continue
		}
		for _, td := range c.KafkaAdminClientInformation.Topics.Details {
			m := connectInternalTopicPattern.FindStringSubmatch(td.Name)
			if m == nil {
				continue
			}
			prefix := m[1]
			suffix := m[3] // configs | offsets | status
			if _, ok := prefixSuffixes[prefix]; !ok {
				prefixSuffixes[prefix] = map[string]struct{}{}
			}
			prefixSuffixes[prefix][suffix] = struct{}{}
		}
	}
	fleets := 0
	for _, suffixes := range prefixSuffixes {
		if _, hasConfigs := suffixes["configs"]; !hasConfigs {
			continue
		}
		if _, hasStatus := suffixes["status"]; !hasStatus {
			continue
		}
		fleets++
	}
	// Cross-check against the scanner-reported self-managed connectors —
	// when the scan ran successfully and returned a non-zero count, use
	// the higher of the two signals (the scan can see fleets whose
	// internal topics use entirely custom names).
	scannerCount := 0
	for _, c := range clusters {
		smc := c.KafkaAdminClientInformation.SelfManagedConnectors
		if smc == nil {
			continue
		}
		scannerCount += len(smc.Connectors)
	}
	if scannerCount > fleets {
		fleets = scannerCount
	}
	return types.EffortSignal{
		ID:    EffortSignalIDSelfManagedConnectFleets,
		Label: "Self-managed Connect fleets — review surface area beyond what kcp can describe",
		Count: fleets,
		Note:  "Counts distinct prefixes with `connect-configs` AND `connect-status` topics (the two-of-three triad), plus any connectors reported by `kcp scan self-managed-connectors`. Fleets with entirely custom internal-topic naming may not be counted.",
	}
}

// ----- Signal 4: Glue → CC SR client serializer migration size -----

// Cross-references `discovered_clients[]` against the Glue schemas:
// count clients whose Topic name matches a Glue schema name directly
// or via the standard `<topic>-value` / `<topic>-key` subject suffix.
// Server-side schema export is automated by `kcp create-asset
// migrate-schemas --glue-registry`; this row scopes the *client-side*
// serializer-swap workstream.
func signalGlueSerializerMigration(state types.ProcessedState, clusters []types.ProcessedCluster) types.EffortSignal {
	sig := types.EffortSignal{
		ID:    EffortSignalIDGlueSerializerMigration,
		Label: "Glue → CC SR client serializer migration size — clients that need `AWSKafkaAvroSerializer` → `KafkaAvroSerializer`",
	}
	if state.SchemaRegistries == nil || len(state.SchemaRegistries.AWSGlue) == 0 {
		sig.Count = 0
		sig.Note = "no Glue Schema Registry detected; signal not applicable"
		return sig
	}
	// Build lookup of Glue subject names (schema name + standard
	// `-value` / `-key` suffix variants).
	glueSubjects := map[string]struct{}{}
	for _, gr := range state.SchemaRegistries.AWSGlue {
		for _, gs := range gr.Schemas {
			glueSubjects[gs.SchemaName] = struct{}{}
		}
	}
	count := 0
	for _, c := range clusters {
		for _, dc := range c.DiscoveredClients {
			if matchesGlueSubject(dc.Topic, glueSubjects) {
				count++
			}
		}
	}
	sig.Count = count
	sig.Note = "Cross-references `discovered_clients[].topic` against Glue `schema_name` (direct match + standard `-value` / `-key` subject suffix variants). Custom subject-name strategies may not match."
	return sig
}

// matchesGlueSubject reports whether `topic` (or its standard
// `-value` / `-key` subject derivative) is a Glue schema name.
func matchesGlueSubject(topic string, subjects map[string]struct{}) bool {
	if topic == "" {
		return false
	}
	candidates := []string{topic, topic + "-value", topic + "-key"}
	// Also reverse the suffix strip — if the schema names are stored
	// without the suffix, a topic like "orders-value" should still
	// match a schema named "orders".
	if strings.HasSuffix(topic, "-value") {
		candidates = append(candidates, strings.TrimSuffix(topic, "-value"))
	}
	if strings.HasSuffix(topic, "-key") {
		candidates = append(candidates, strings.TrimSuffix(topic, "-key"))
	}
	for _, cand := range candidates {
		if _, ok := subjects[cand]; ok {
			return true
		}
	}
	return false
}
