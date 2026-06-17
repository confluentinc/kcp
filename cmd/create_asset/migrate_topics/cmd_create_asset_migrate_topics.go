package migrate_topics

import (
	"encoding/json"
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	stateFile                 string
	ccType                    string
	clusterId                 string
	sourceType                string
	targetClusterId           string
	targetClusterRestEndpoint string
	clusterLinkName           string
	outputDir                 string
	mode                      string
	topicsInclude             []string
	topicsExclude             []string
)

func NewMigrateTopicsCmd() *cobra.Command {
	migrationCmd := &cobra.Command{
		Use:   "migrate-topics",
		Short: "Create assets for the migrate topics",
		Long:  "Create Terraform files for migrating topics to a target Confluent Cloud cluster. Supports --mode mirror (cluster-link mirror topics, forwards data) and --mode new (plain Confluent Cloud topics, no data).",
		Example: `  # Mirror mode (forwards data via cluster link)
  kcp create-asset migrate-topics \
      --mode mirror \
      --cc-type commercial \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.private.confluent.cloud:443 \
      --cluster-link-name msk-to-cc-link

  # New mode (greenfield CC topics, no data forward)
  kcp create-asset migrate-topics \
      --mode new \
      --cc-type commercial \
      --state-file kcp-state.json \
      --source-type msk \
      --cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --target-cluster-id lkc-xyz123 \
      --target-rest-endpoint https://lkc-xyz123.eu-west-3.aws.private.confluent.cloud:443 \
      --topics-include 'orders.*' --topics-exclude '*.dlq'`,
		SilenceErrors: true,
		PreRunE:       preRunMigrateTopics,
		RunE:          runMigrateTopics,
	}

	groups := map[*pflag.FlagSet]string{}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&stateFile, "state-file", "", "The path to the kcp state file where the cluster discovery reports have been written to.")
	requiredFlags.StringVar(&ccType, "cc-type", "", "The Confluent Cloud destination type: 'commercial' (Standard) or 'government' (Confluent Cloud for Government).")
	requiredFlags.StringVar(&sourceType, "source-type", "msk", "Source type: 'msk' or 'apache-kafka'")
	requiredFlags.StringVar(&clusterId, "cluster-id", "", "The cluster identifier (ARN for MSK, cluster ID from credentials file for Apache Kafka).")
	requiredFlags.StringVar(&mode, "mode", "", "Migration mode: 'mirror' (cluster-link mirror topics, forwards data) or 'new' (plain CC topics, no data).")
	requiredFlags.StringVar(&targetClusterId, "target-cluster-id", "", "The Confluent Cloud cluster ID (e.g., lkc-xxxxxx).")
	requiredFlags.StringVar(&targetClusterRestEndpoint, "target-rest-endpoint", "", "The Confluent Cloud cluster REST endpoint (e.g., https://xxx.xxx.aws.confluent.cloud:443).")
	requiredFlags.StringVar(&clusterLinkName, "cluster-link-name", "", "The name of the cluster link that was created as part of the migration. Required for --mode mirror; rejected for --mode new.")
	migrationCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Optional flags.
	optionalFlags := pflag.NewFlagSet("optional", pflag.ExitOnError)
	optionalFlags.SortFlags = false
	optionalFlags.StringVar(&outputDir, "output-dir", "migrate_topics", "The directory to output the Terraform files to. (default: 'migrate_topics')")
	optionalFlags.StringSliceVar(&topicsInclude, "topics-include", []string{}, "Glob patterns of topics to include (comma separated or repeated flag, e.g. --topics-include 'orders.*,events.*'). Empty = all non-internal topics.")
	optionalFlags.StringSliceVar(&topicsExclude, "topics-exclude", []string{}, "Glob patterns of topics to exclude (comma separated or repeated flag, e.g. --topics-exclude '*.dlq'). Exclude wins on overlap with include.")
	migrationCmd.Flags().AddFlagSet(optionalFlags)
	groups[optionalFlags] = "Optional Flags"

	migrationCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags}
		groupNames := []string{"Required Flags", "Optional Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	_ = migrationCmd.MarkFlagRequired("state-file")
	_ = migrationCmd.MarkFlagRequired("cluster-id")
	_ = migrationCmd.MarkFlagRequired("target-cluster-id")
	_ = migrationCmd.MarkFlagRequired("target-rest-endpoint")
	// --mode and --cluster-link-name are validated in parseMigrateTopicsOpts
	// because --cluster-link-name is conditionally required (mirror only).

	return migrationCmd
}

func preRunMigrateTopics(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	// Validate the destination declaration and mode here (PreRunE) — before
	// cobra's required-flag check — so a missing/invalid --cc-type or a
	// Confluent Cloud for Government refusal surfaces consistently with the
	// other create-asset commands rather than behind an unrelated required-flag
	// error.
	if err := validateModeFlags(ccType, mode, clusterLinkName); err != nil {
		return err
	}

	return nil
}

func runMigrateTopics(cmd *cobra.Command, args []string) error {
	opts, err := parseMigrateTopicsOpts()
	if err != nil {
		return fmt.Errorf("failed to parse migrate topics opts: %v", err)
	}

	migrateTopicsAssetGenerator := NewMigrateTopicsAssetGenerator(*opts)
	if err := migrateTopicsAssetGenerator.Run(); err != nil {
		return fmt.Errorf("failed to create migration assets: %v", err)
	}

	return nil
}

func parseMigrateTopicsOpts() (*MigrateTopicsOpts, error) {
	// --cc-type / --mode / --cluster-link-name are validated in
	// preRunMigrateTopics (PreRunE), so by here they are known-good.

	// "apache-kafka" is the user-facing value; normalize to the internal "osk" token.
	normalizedSourceType, err := types.ParseSourceTypeFlag(sourceType)
	if err != nil {
		return nil, err
	}
	sourceType = string(normalizedSourceType)

	// __consumer_offsets survives the internal-topic filter in mirror mode only:
	// a cluster link can mirror the offset topic, but `--mode new` emits a
	// confluent_kafka_topic that CC rejects at apply time (reserved __ prefix).
	var internalTopicsToInclude []string
	if mode == types.MigrateTopicsModeMirror {
		internalTopicsToInclude = []string{"__consumer_offsets"}
	}

	file, err := os.ReadFile(stateFile)
	if err != nil {
		return nil, fmt.Errorf("failed to read cluster file: %v", err)
	}

	var state types.State
	if err := json.Unmarshal(file, &state); err != nil {
		return nil, fmt.Errorf("failed to parse statefile JSON: %w", err)
	}

	var kafkaAdminInfo *types.KafkaAdminClientInformation

	switch sourceType {
	case "msk":
		cluster, err := state.GetClusterByArn(clusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get cluster: %w", err)
		}
		kafkaAdminInfo = &cluster.KafkaAdminClientInformation
	case "osk":
		cluster, err := state.GetOSKClusterByID(clusterId)
		if err != nil {
			return nil, fmt.Errorf("failed to get Apache Kafka cluster: %w", err)
		}
		kafkaAdminInfo = &cluster.KafkaAdminClientInformation
	default:
		return nil, fmt.Errorf("invalid --source-type: %s (must be 'msk' or 'apache-kafka')", sourceType)
	}

	var allTopics []types.TopicDetails
	if kafkaAdminInfo.Topics != nil {
		allTopics = kafkaAdminInfo.Topics.Details
	}
	selected := selectTopics(allTopics, internalTopicsToInclude, topicsInclude, topicsExclude)

	// When the user explicitly provided filter patterns, an empty selection is
	// almost always a typo or a stale state file — fail loudly with the
	// patterns + candidate names rather than silently writing an empty TF
	// directory the user only discovers at `terraform apply`. No filters set
	// and 0 topics → no error (state-file shape problem, not a filter problem).
	if len(selected) == 0 && (len(topicsInclude) > 0 || len(topicsExclude) > 0) {
		return nil, noMatchError(allTopics, internalTopicsToInclude, topicsInclude, topicsExclude)
	}

	opts := MigrateTopicsOpts{
		Topics:                    selected,
		TargetClusterId:           targetClusterId,
		TargetClusterRestEndpoint: targetClusterRestEndpoint,
		ClusterLinkName:           clusterLinkName,
		OutputDir:                 outputDir,
		Mode:                      mode,
	}

	return &opts, nil
}

// selectTopics applies the migrate-topics selection pipeline:
//  1. Drop __*-prefixed topics, except those in internalTopicsToInclude
//     (currently just __consumer_offsets).
//  2. Apply include/exclude globs (empty include = all; exclude wins on overlap).
//
// Input order is preserved.
func selectTopics(all []types.TopicDetails, internalTopicsToInclude, include, exclude []string) []types.TopicDetails {
	candidates := make([]types.TopicDetails, 0, len(all))
	for _, t := range all {
		if !strings.HasPrefix(t.Name, "__") || slices.Contains(internalTopicsToInclude, t.Name) {
			candidates = append(candidates, t)
		}
	}

	names := make([]string, len(candidates))
	for i, t := range candidates {
		names[i] = t.Name
	}
	kept := utils.FilterByGlob(names, include, exclude)
	keep := make(map[string]struct{}, len(kept))
	for _, n := range kept {
		keep[n] = struct{}{}
	}

	out := make([]types.TopicDetails, 0, len(kept))
	for _, t := range candidates {
		if _, ok := keep[t.Name]; ok {
			out = append(out, t)
		}
	}
	return out
}

// noMatchError builds the "filters selected 0 topics" error. The message is
// single-line and intentionally never lists topic names — terminal output
// stays free of source topic data even when filters fail.
func noMatchError(all []types.TopicDetails, internalTopicsToInclude, include, exclude []string) error {
	candidateCount := 0
	for _, t := range all {
		if !strings.HasPrefix(t.Name, "__") || slices.Contains(internalTopicsToInclude, t.Name) {
			candidateCount++
		}
	}
	if candidateCount == 0 {
		return fmt.Errorf("--topics-include/--topics-exclude were provided but the state file has no topics to filter (run `kcp scan clusters` first)")
	}
	return fmt.Errorf("--topics-include=%v --topics-exclude=%v selected 0 topics from %d candidates", include, exclude, candidateCount)
}

// validateModeFlags enforces the destination declaration and mode-dependent
// flag combinations.
//   - --cc-type is required and must be "commercial" or "government".
//   - --cc-type government refuses --mode mirror (mirror topics rely on
//     Cluster Linking, unsupported on Confluent Cloud for Government); --mode new
//     proceeds.
//   - --mode is required and must be one of "mirror" or "new".
//   - --mode mirror requires --cluster-link-name.
//   - --mode new rejects --cluster-link-name.
//
// Destination validation is ordered before mode validation so a missing or
// invalid declaration errors regardless of mode.
func validateModeFlags(ccType, mode, clusterLinkName string) error {
	if ccType == "" {
		return fmt.Errorf("--cc-type is required (values: %s, %s)", types.DestinationCommercial, types.DestinationGovernment)
	}
	destination, err := types.ToDestinationType(ccType)
	if err != nil {
		return fmt.Errorf("invalid --cc-type: %v", err)
	}
	if destination.IsGov() && mode == types.MigrateTopicsModeMirror {
		return fmt.Errorf("--mode %s is not supported on Confluent Cloud for Government: mirror topics rely on Cluster Linking, which Confluent Cloud for Government does not provide. Use --mode %s instead", types.MigrateTopicsModeMirror, types.MigrateTopicsModeNew)
	}

	switch mode {
	case "":
		return fmt.Errorf("--mode is required (values: %s, %s)", types.MigrateTopicsModeMirror, types.MigrateTopicsModeNew)
	case types.MigrateTopicsModeMirror:
		if clusterLinkName == "" {
			return fmt.Errorf("--cluster-link-name is required when --mode %s", types.MigrateTopicsModeMirror)
		}
	case types.MigrateTopicsModeNew:
		if clusterLinkName != "" {
			return fmt.Errorf("--cluster-link-name is not valid when --mode %s", types.MigrateTopicsModeNew)
		}
	default:
		return fmt.Errorf("invalid --mode: %q (values: %s, %s)", mode, types.MigrateTopicsModeMirror, types.MigrateTopicsModeNew)
	}
	return nil
}
