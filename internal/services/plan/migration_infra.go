package plan

import (
	"path/filepath"
	"strconv"
	"strings"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
)

// MigrationInfra names the concrete `kcp create-asset migration-infra --type N`
// for a cluster, mapping the source's accessibility and authentication onto the
// migration-infra type (1–5). Type 0 means "no standard type fits" — a specialist
// designs it.
type MigrationInfra struct {
	// Kind is the machine discriminator, since Type 0 covers several outcomes:
	// "cluster_link" (Type 1-5), "replicator", "start_fresh", or "specialist".
	Kind            string `json:"kind"`
	Pending         bool   `json:"pending,omitempty"` // set at JSON marshal when the infra/data-migration verdicts it derives from are unanswered; provisional fields are blanked
	Type            int    `json:"type"`              // 1-5 for cluster_link, else 0
	Label           string `json:"label"`
	Rationale       string `json:"rationale"`
	CCType          string `json:"cc_type"`                    // "commercial" | "government"
	Alternative     string `json:"alternative,omitempty"`      // an alternative type worth noting
	AlternativeType int    `json:"alternative_type,omitempty"` // the --type N of that alternative
	JumpCluster     bool   `json:"jump_cluster,omitempty"`     // types 4/5 need extra jump-cluster inputs
	// SpecialistClusterLinkable: for a Type-0 specialist outcome, whether the standard
	// Cluster Linking cutover steps still apply (mTLS/undetected auth, CL available) vs
	// no Cluster Linking at all (government). Render-only discriminator; not serialized.
	SpecialistClusterLinkable bool `json:"-"`
}

// migrationInfraFor maps one cluster's plan onto its migration-infra type. The
// topology decision is the engine's (MigrationInfraDecision); this adds the
// kcp-specific cc-type and the withheld handling.
func migrationInfraFor(p engine.Profile, plan engine.PlanResult) MigrationInfra {
	ccType := "commercial"
	if p.TargetIsGovCloud == "Yes" {
		ccType = "government"
	}
	if plan.Withheld {
		return MigrationInfra{
			Kind:      "specialist",
			CCType:    ccType,
			Label:     "Determined with a specialist",
			Rationale: "This cluster is going through an assessment, so the migration infrastructure is chosen together with a specialist rather than auto-mapped.",
		}
	}
	// The migration-infra types all build a Cluster Link. When the data migration
	// runs a different way, there is no cluster link to stand up.
	switch {
	case plan.Switchover.StartFresh:
		return MigrationInfra{
			Kind:      "start_fresh",
			CCType:    ccType,
			Label:     "Nothing to build",
			Rationale: "You're starting fresh, so there's no existing data to move. Point your producers and consumers at the new cluster and retire the old one.",
		}
	case plan.Switchover.Replicator:
		return MigrationInfra{
			Kind:      "replicator",
			CCType:    ccType,
			Label:     "Confluent Replicator",
			Rationale: "Your data migration runs on Confluent Replicator. See the Data migration step above for how to set it up.",
		}
	}
	ch := engine.MigrationInfraDecision(p, plan.ClusterType.Tier)
	// Type 1-5 is a real cluster-link topology; Type 0 here means no standard type
	// fits (mTLS / government / other), which a specialist designs.
	kind := "cluster_link"
	if ch.Type == 0 {
		kind = "specialist"
	}
	return MigrationInfra{
		Kind:                      kind,
		Type:                      ch.Type,
		Label:                     ch.Label,
		Rationale:                 ch.Rationale,
		Alternative:               ch.Alternative,
		AlternativeType:           ch.AlternativeType,
		JumpCluster:               ch.JumpCluster,
		CCType:                    ccType,
		SpecialistClusterLinkable: ch.SpecialistClusterLinkable,
	}
}

// sourcePublicAccess reports whether the scanned MSK cluster exposes public
// broker endpoints (Provisioned only; Serverless has no public access).
func sourcePublicAccess(c report.ProcessedCluster) bool {
	prov := c.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil || prov.BrokerNodeGroupInfo == nil {
		return false
	}
	ci := prov.BrokerNodeGroupInfo.ConnectivityInfo
	if ci == nil || ci.PublicAccess == nil || ci.PublicAccess.Type == nil {
		return false
	}
	return *ci.PublicAccess.Type != "DISABLED" && *ci.PublicAccess.Type != ""
}

// migrationInfraCommand renders the ready-to-fill `create-asset migration-infra`
// invocation for a cluster. Values kcp knows from the scan/plan are filled; the
// Confluent Cloud–side values are left as <placeholders>. Returns "" when no
// standard type applies.
// targetInfraCommand builds the `kcp create-asset target-infra` command that
// provisions the Confluent Cloud environment, cluster, and private networking the
// plan recommends. Cluster type is pulled from the verdict; environment, cluster
// name, and subnet CIDRs are the customer's to fill in.
func targetInfraCommand(cp ClusterPlan, stateFilePath string) string {
	clusterID := cp.Arn
	if clusterID == "" {
		clusterID = cp.ClusterID
	}
	state := "kcp-state.json"
	if stateFilePath != "" {
		state = filepath.Base(stateFilePath)
	}
	tier := targetClusterTypeFlag(cp)
	if tier == "" {
		tier = "enterprise" // the migration path targets Enterprise or Dedicated
	}
	lines := []string{
		"kcp create-asset target-infra \\",
		"  --state-file " + state + " \\",
		"  --source-cluster-id " + clusterID + " \\",
		"  --needs-environment --env-name <your-env-name> \\",
		"  --needs-cluster --cluster-name <your-cluster-name> --cluster-type " + tier + " \\",
		"  --needs-private-link --subnet-cidrs <your-subnet-cidrs>",
	}
	return strings.Join(lines, "\n")
}

func migrationInfraCommand(cp ClusterPlan, stateFilePath string) string {
	mi := cp.MigrationInfra
	if mi.Type == 0 {
		return ""
	}
	clusterID := cp.Arn
	if clusterID == "" {
		clusterID = cp.ClusterID
	}
	// File name only, not the absolute path — the plan is a shareable artifact.
	state := "kcp-state.json"
	if stateFilePath != "" {
		state = filepath.Base(stateFilePath)
	}
	// Emit every flag the CLI requires for this type, so the printed command runs
	// as-is (see the per-type MarkFlagRequired switch in the migration-infra command).
	flags := []string{
		"--type " + itoa(mi.Type),
		sourceTypeFlag(stateFilePath),
		"--state-file " + state,
		"--cluster-id " + clusterID,
		"--cc-type " + mi.CCType,
	}
	if tier := targetClusterTypeFlag(cp); tier != "" {
		flags = append(flags, "--target-cluster-type "+tier)
	}
	flags = append(flags,
		"--cluster-link-name <your-link-name>",
		"--target-cluster-id <cc-cluster-id>",
		"--target-rest-endpoint <cc-rest-endpoint>",
	)
	// Every private type (2-5) needs the target environment.
	if mi.Type != 1 {
		flags = append(flags, "--target-environment-id <cc-env-id>")
	}
	// Jump clusters (types 4/5) need the bootstrap endpoint, an existing PrivateLink
	// endpoint, and the jump-cluster subnets; type 5 (IAM) also needs the IAM role.
	if mi.JumpCluster {
		flags = append(flags,
			"--target-bootstrap-endpoint <cc-bootstrap-endpoint>",
			"--existing-private-link-vpce-id <your-vpce-id>",
			"--jump-cluster-broker-subnet-cidr <subnet-cidr[,subnet-cidr...]>",
			"--jump-cluster-setup-host-subnet-cidr <subnet-cidr>",
		)
		if mi.Type == 5 {
			flags = append(flags, "--jump-cluster-iam-auth-role-name <your-msk-iam-role>")
		}
		// MSK Serverless has no broker config to default from, so these are required.
		if cp.IsServerless {
			flags = append(flags,
				"--jump-cluster-instance-type <instance-type>",
				"--jump-cluster-broker-storage <gb>",
			)
		}
	}
	return "kcp create-asset migration-infra \\\n  " + strings.Join(flags, " \\\n  ")
}

// migrateTopicsCommand renders the `create-asset migrate-topics` invocation for a
// cluster. mode is "mirror" (cluster-link path: the link forwards data, so topics
// mirror it, and --cluster-link-name is required) or "new" (Replicator/start-fresh:
// plain empty topics, no link). Scan/plan values are filled; the Confluent
// Cloud–side values stay <placeholders>, matching the other create-asset commands.
func migrateTopicsCommand(cp ClusterPlan, mode, stateFilePath string) string {
	clusterID := cp.Arn
	if clusterID == "" {
		clusterID = cp.ClusterID
	}
	state := "kcp-state.json"
	if stateFilePath != "" {
		state = filepath.Base(stateFilePath)
	}
	flags := []string{
		"--mode " + mode,
		sourceTypeFlag(stateFilePath),
		"--state-file " + state,
		"--cluster-id " + clusterID,
		"--cc-type " + cp.MigrationInfra.CCType,
		"--target-cluster-id <cc-cluster-id>",
		"--target-rest-endpoint <cc-rest-endpoint>",
	}
	// Mirror mode forwards data over the cluster link, so it needs the link's name;
	// new mode rejects the flag (validateModeFlags).
	if mode == migrateTopicsModeMirror {
		flags = append(flags, "--cluster-link-name <your-link-name>")
	}
	return "kcp create-asset migrate-topics \\\n  " + strings.Join(flags, " \\\n  ")
}

// migrate-topics --mode values (see cmd/create_asset/migrate_topics): mirror forwards
// data via a cluster link; new creates plain empty topics.
const (
	migrateTopicsModeMirror = "mirror"
	migrateTopicsModeNew    = "new"
)

// migrateSchemasCommand renders the `create-asset migrate-schemas` invocation for a
// cluster, chosen by the schema-migration method: Glue re-registration
// (--glue-registry) or Schema Linking from a Confluent Schema Registry (--url).
// Source identifiers come from the scan when unambiguous, else a <placeholder>.
// Returns "" for any Schema kind that has no such command (schemaless, fresh
// registry, Replicator, pending).
func migrateSchemasCommand(cp ClusterPlan, schemaKind engine.SchemaKind, stateFilePath string) string {
	var srcFlags []string
	switch schemaKind {
	case engine.SchemaKindGlueBulk:
		reg := "<glue-registry-name>"
		if cp.SchemaSource != nil && cp.SchemaSource.GlueRegistry != "" {
			reg = cp.SchemaSource.GlueRegistry
		}
		srcFlags = []string{"--glue-registry " + reg}
		if cp.SchemaSource != nil && cp.SchemaSource.GlueRegion != "" {
			srcFlags = append(srcFlags, "--region "+cp.SchemaSource.GlueRegion)
		}
	case engine.SchemaKindLinking:
		url := "<source-sr-url>"
		if cp.SchemaSource != nil && cp.SchemaSource.ConfluentURL != "" {
			url = cp.SchemaSource.ConfluentURL
		}
		srcFlags = []string{"--url " + url}
	default:
		return ""
	}
	state := "kcp-state.json"
	if stateFilePath != "" {
		state = filepath.Base(stateFilePath)
	}
	flags := append([]string{}, srcFlags...)
	flags = append(flags,
		"--state-file "+state,
		"--cc-type "+cp.MigrationInfra.CCType,
		"--cc-sr-rest-endpoint <cc-sr-rest-endpoint>",
	)
	return "kcp create-asset migrate-schemas \\\n  " + strings.Join(flags, " \\\n  ")
}

// migrateConnectorsCommands renders the `create-asset migrate-connectors`
// invocations for a cluster, one per source connector runtime present: `msk` for
// MSK Connect, `self-managed` for a self-managed Kafka Connect cluster. Both
// translate the source configs into Confluent Cloud fully-managed connectors.
// Scan/plan values are filled; Confluent Cloud–side values (env, cluster, API
// key/secret) stay <placeholders>. Returns nil when no runtime is known.
func migrateConnectorsCommands(cp ClusterPlan, src ConnectorSource, stateFilePath string) []string {
	clusterID := cp.Arn
	if clusterID == "" {
		clusterID = cp.ClusterID
	}
	state := "kcp-state.json"
	if stateFilePath != "" {
		state = filepath.Base(stateFilePath)
	}
	// Flags shared by both subcommands.
	ccFlags := []string{
		"--state-file " + state,
		"--cluster-id " + clusterID,
		"--cc-environment-id <cc-env-id>",
		"--cc-cluster-id <cc-cluster-id>",
		"--cc-api-key <cc-api-key>",
		"--cc-api-secret <cc-api-secret>",
	}
	var cmds []string
	if src.MSKConnect {
		cmds = append(cmds, "kcp create-asset migrate-connectors msk \\\n  "+strings.Join(ccFlags, " \\\n  "))
	}
	if src.SelfManaged {
		// self-managed carries the source type (msk in a scan-based run); msk-connect does not.
		smFlags := append([]string{sourceTypeFlag(stateFilePath)}, ccFlags...)
		cmds = append(cmds, "kcp create-asset migrate-connectors self-managed \\\n  "+strings.Join(smFlags, " \\\n  "))
	}
	return cmds
}

// targetClusterTypeFlag maps the tier verdict to the create-asset --target-cluster-type
// value. The migration path targets Enterprise or Dedicated (private networking);
// other tiers return "" (no flag, and the caller notes it isn't auto-generated).
func targetClusterTypeFlag(cp ClusterPlan) string {
	switch cp.Plan.ClusterType.Value {
	case engine.TierEnterprise:
		return "enterprise"
	case engine.TierDedicated:
		return "dedicated"
	}
	return ""
}

// sourceTypeFlag renders the `--source-type` flag for a create-asset command. In a
// scan-based run (a state file is present) the source is known to be MSK. In a
// scanless questionnaire run the source is only inferred, so emit a placeholder the
// reader fills in rather than asserting `msk`.
func sourceTypeFlag(stateFilePath string) string {
	if stateFilePath == "" {
		return "--source-type <msk|apache-kafka>"
	}
	return "--source-type msk"
}

// itoa is the plan package's small int-to-string helper.
func itoa(n int) string { return strconv.Itoa(n) }
