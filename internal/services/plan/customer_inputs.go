package plan

import (
	"sort"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

// bytesPerMB — binary MB, matches sizing.go's conversion.
const bytesPerMB = 1024.0 * 1024.0

// applyClusterDeclarations merges customer-declared per-cluster facts
// from plan-inputs.yaml into `state.Sources`. Two modes per entry:
//   - Override: cluster name matches a scanned cluster — declared
//     fields overlay; scan values stay where the customer didn't override.
//   - Synthesise: no scan match — build a fresh ProcessedCluster from
//     the declaration and attach to `entry.Region` (required).
//
// Runs once at the top of Build, after backfillAggregates. Every
// downstream consumer reads the merged state — no awareness needed of
// whether a value came from the scanner or the customer.
func applyClusterDeclarations(state *types.ProcessedState, raw *types.PlanInputs) {
	if raw == nil {
		return
	}
	applyFleetCSRDeclaration(state, raw)
	if len(raw.Clusters) == 0 {
		return
	}
	known := indexClustersByName(state)
	// Iterate cluster declarations in a stable order so synthesised
	// clusters land deterministically.
	names := make([]string, 0, len(raw.Clusters))
	for name := range raw.Clusters {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		entry := raw.Clusters[name]
		if !hasClusterDeclaration(entry) {
			continue
		}
		if ptr := known[name]; ptr != nil {
			applyClusterOverlay(ptr, entry)
			continue
		}
		// Synthesis path: requires Region to know which region bucket
		// to attach the new cluster to.
		if entry.Region == nil || *entry.Region == "" {
			continue
		}
		c := synthesiseCluster(name, entry)
		attachClusterToRegion(state, c, *entry.Region)
	}
}

// applyFleetCSRDeclaration synthesises a stub Confluent SR entry into
// `state.SchemaRegistries` when the customer declared CSR facts
// (confluent_sr_cp_version) in plan-inputs but no SR is in the state.
// Declaring `confluent_sr_cp_version` is a strong signal the source HAS
// a CSR — without the stub, decideSchema sees `source: none`, the
// schema_linking_ineligible OQ can't fire, and the customer-declared
// CP version is silently ignored. The stub URL is a placeholder ("from
// plan-inputs.yaml") that surfaces in §5's Source-detected line so
// readers know it came from a declaration rather than a scan.
func applyFleetCSRDeclaration(state *types.ProcessedState, raw *types.PlanInputs) {
	if raw.ConfluentSRCPVersion == nil || *raw.ConfluentSRCPVersion == "" {
		return
	}
	if state.SchemaRegistries == nil {
		state.SchemaRegistries = &types.SchemaRegistriesState{}
	}
	if len(state.SchemaRegistries.ConfluentSchemaRegistry) > 0 {
		return // scan already populated a CSR; trust it
	}
	state.SchemaRegistries.ConfluentSchemaRegistry = []types.SchemaRegistryInformation{
		{URL: "declared in plan-inputs.yaml"},
	}
}

// indexClustersByName returns name → pointer-to-cluster for every MSK
// cluster in state, so callers mutate the canonical instance (not a
// copy). OSK clusters are excluded — they fall through to the
// osk_source_unsupported OQ.
func indexClustersByName(state *types.ProcessedState) map[string]*types.ProcessedCluster {
	out := map[string]*types.ProcessedCluster{}
	for i := range state.Sources {
		if state.Sources[i].MSKData == nil {
			continue
		}
		for j := range state.Sources[i].MSKData.Regions {
			for k := range state.Sources[i].MSKData.Regions[j].Clusters {
				c := &state.Sources[i].MSKData.Regions[j].Clusters[k]
				out[c.Name] = c
			}
		}
	}
	return out
}

// hasClusterDeclaration reports whether the entry carries at least one
// scan-equivalent field. Entries with only decision-level overrides
// (TargetAuthMethod / DowntimeTolerance) need no state mutation.
func hasClusterDeclaration(e types.ClusterPlanInputs) bool {
	return e.Region != nil ||
		e.ClusterType != nil ||
		e.KafkaVersion != nil ||
		e.BrokerCount != nil ||
		e.BrokerInstanceType != nil ||
		e.StorageMode != nil ||
		len(e.AuthMethods) > 0 ||
		e.NetworkAccessibility != nil ||
		e.PeakIngressMBps != nil ||
		e.PeakEgressMBps != nil ||
		e.P95IngressMBps != nil ||
		e.P95EgressMBps != nil ||
		e.PartitionCount != nil ||
		e.TopicCount != nil ||
		e.ACLCount != nil
}

// applyClusterOverlay overlays declared fields onto a scanned cluster.
// Undeclared fields keep their scan values.
func applyClusterOverlay(c *types.ProcessedCluster, e types.ClusterPlanInputs) {
	prov, isServerlessOverride := provisionedShape(&c.AWSClientInformation.MskClusterConfig, e)
	if isServerlessOverride {
		// If the customer pivoted to SERVERLESS, clear Provisioned and
		// build the Serverless block fresh.
		c.AWSClientInformation.MskClusterConfig.Provisioned = nil
		c.AWSClientInformation.MskClusterConfig.Serverless = buildServerlessBlock(e)
		c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterTypeServerless
	} else if prov != nil {
		c.AWSClientInformation.MskClusterConfig.Provisioned = prov
		if e.ClusterType != nil {
			c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterType(*e.ClusterType)
		}
	}
	if e.BrokerCount != nil {
		c.AWSClientInformation.Nodes = makeBrokerNodes(*e.BrokerCount)
	}
	if e.TopicCount != nil || e.PartitionCount != nil {
		ensureTopicsSummary(c, e)
	}
	if e.ACLCount != nil {
		c.KafkaAdminClientInformation.Acls = makeACLs(*e.ACLCount)
	}
	if e.PeakIngressMBps != nil || e.PeakEgressMBps != nil || e.P95IngressMBps != nil || e.P95EgressMBps != nil {
		applyThroughputAggregates(c, e)
	}
}

// synthesiseCluster builds a fresh ProcessedCluster from a customer
// declaration when no scan exists for `name`.
func synthesiseCluster(name string, e types.ClusterPlanInputs) types.ProcessedCluster {
	c := types.ProcessedCluster{Name: name}
	if e.Region != nil {
		c.Region = *e.Region
	}
	// Default to PROVISIONED when the customer doesn't declare a type.
	clusterType := "PROVISIONED"
	if e.ClusterType != nil {
		clusterType = *e.ClusterType
	}
	c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterType(clusterType)
	if clusterType == "SERVERLESS" {
		c.AWSClientInformation.MskClusterConfig.Serverless = buildServerlessBlock(e)
	} else {
		c.AWSClientInformation.MskClusterConfig.Provisioned = buildProvisionedBlock(e)
	}
	if e.BrokerCount != nil {
		c.AWSClientInformation.Nodes = makeBrokerNodes(*e.BrokerCount)
	}
	// Topics non-nil so topic_inventory_empty branches on "scanned and
	// empty" not "scan didn't run" — the declaration implies intent.
	c.KafkaAdminClientInformation.Topics = &types.Topics{
		Details: nil,
		Summary: types.TopicSummary{},
	}
	if e.TopicCount != nil || e.PartitionCount != nil {
		ensureTopicsSummary(&c, e)
	}
	if e.ACLCount != nil {
		c.KafkaAdminClientInformation.Acls = makeACLs(*e.ACLCount)
	} else {
		// Non-nil empty slice → aclScanRan returns true; declaration replaces scan.
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
	}
	if e.PeakIngressMBps != nil || e.PeakEgressMBps != nil || e.P95IngressMBps != nil || e.P95EgressMBps != nil {
		applyThroughputAggregates(&c, e)
	}
	return c
}

// attachClusterToRegion drops `c` into the named region, creating
// the region bucket (and the MSK source) if needed.
func attachClusterToRegion(state *types.ProcessedState, c types.ProcessedCluster, region string) {
	// Find or create the MSK source.
	var msk *types.ProcessedMSKSource
	for i := range state.Sources {
		if state.Sources[i].Type == types.SourceTypeMSK && state.Sources[i].MSKData != nil {
			msk = state.Sources[i].MSKData
			break
		}
	}
	if msk == nil {
		state.Sources = append(state.Sources, types.ProcessedSource{
			Type:    types.SourceTypeMSK,
			MSKData: &types.ProcessedMSKSource{},
		})
		msk = state.Sources[len(state.Sources)-1].MSKData
	}
	// Find or create the region bucket.
	for i := range msk.Regions {
		if msk.Regions[i].Name == region {
			msk.Regions[i].Clusters = append(msk.Regions[i].Clusters, c)
			return
		}
	}
	msk.Regions = append(msk.Regions, types.ProcessedRegion{Name: region, Clusters: []types.ProcessedCluster{c}})
}

// provisionedShape returns the Provisioned block with customer
// overrides applied, plus a flag set when the override pivoted to
// Serverless (caller switches paths; the returned *Provisioned is
// unused in that case).
func provisionedShape(cfg *kafkatypes.Cluster, e types.ClusterPlanInputs) (*kafkatypes.Provisioned, bool) {
	if e.ClusterType != nil && *e.ClusterType == "SERVERLESS" {
		return nil, true
	}
	prov := cfg.Provisioned
	if prov == nil {
		prov = &kafkatypes.Provisioned{}
	}
	if e.KafkaVersion != nil {
		v := *e.KafkaVersion
		prov.CurrentBrokerSoftwareInfo = &kafkatypes.BrokerSoftwareInfo{KafkaVersion: &v}
	}
	if e.BrokerInstanceType != nil {
		t := *e.BrokerInstanceType
		if prov.BrokerNodeGroupInfo == nil {
			prov.BrokerNodeGroupInfo = &kafkatypes.BrokerNodeGroupInfo{}
		}
		prov.BrokerNodeGroupInfo.InstanceType = &t
	}
	if e.StorageMode != nil {
		prov.StorageMode = kafkatypes.StorageMode(*e.StorageMode)
	}
	if len(e.AuthMethods) > 0 {
		prov.ClientAuthentication = buildClientAuth(e.AuthMethods)
	}
	return prov, false
}

// buildProvisionedBlock — fresh Provisioned for the synthesis path.
func buildProvisionedBlock(e types.ClusterPlanInputs) *kafkatypes.Provisioned {
	prov := &kafkatypes.Provisioned{}
	if e.KafkaVersion != nil {
		v := *e.KafkaVersion
		prov.CurrentBrokerSoftwareInfo = &kafkatypes.BrokerSoftwareInfo{KafkaVersion: &v}
	}
	if e.BrokerInstanceType != nil {
		t := *e.BrokerInstanceType
		prov.BrokerNodeGroupInfo = &kafkatypes.BrokerNodeGroupInfo{InstanceType: &t}
	}
	if e.StorageMode != nil {
		prov.StorageMode = kafkatypes.StorageMode(*e.StorageMode)
	}
	if len(e.AuthMethods) > 0 {
		prov.ClientAuthentication = buildClientAuth(e.AuthMethods)
	}
	return prov
}

// buildServerlessBlock — Serverless struct from declared auth.
// Serverless only supports IAM SASL today; non-IAM declarations are
// silently dropped (auth_posture_unknown OQ surfaces the gap).
func buildServerlessBlock(e types.ClusterPlanInputs) *kafkatypes.Serverless {
	srv := &kafkatypes.Serverless{
		VpcConfigs: []kafkatypes.VpcConfig{}, // non-nil so isServerless resolves correctly
	}
	for _, m := range e.AuthMethods {
		if m == SourceAuthIAM {
			enabled := true
			srv.ClientAuthentication = &kafkatypes.ServerlessClientAuthentication{
				Sasl: &kafkatypes.ServerlessSasl{Iam: &kafkatypes.Iam{Enabled: &enabled}},
			}
			break
		}
	}
	return srv
}

// buildClientAuth maps declared auth methods to an AWS
// ClientAuthentication struct. Tokens: scram | iam | mtls | unauth.
// Unknown tokens are silently dropped — typos surface via the
// auth_posture_unknown OQ when the block ends up empty.
func buildClientAuth(methods []string) *kafkatypes.ClientAuthentication {
	auth := &kafkatypes.ClientAuthentication{}
	for _, m := range methods {
		enabled := true
		switch m {
		case SourceAuthSCRAM:
			if auth.Sasl == nil {
				auth.Sasl = &kafkatypes.Sasl{}
			}
			auth.Sasl.Scram = &kafkatypes.Scram{Enabled: &enabled}
		case SourceAuthIAM:
			if auth.Sasl == nil {
				auth.Sasl = &kafkatypes.Sasl{}
			}
			auth.Sasl.Iam = &kafkatypes.Iam{Enabled: &enabled}
		case SourceAuthMTLS:
			auth.Tls = &kafkatypes.Tls{Enabled: &enabled}
		case SourceAuthUnauth:
			auth.Unauthenticated = &kafkatypes.Unauthenticated{Enabled: &enabled}
		}
	}
	return auth
}

// makeBrokerNodes — N zero-value NodeInfo entries; the Plan reads
// `len(c.AWSClientInformation.Nodes)` only.
func makeBrokerNodes(n int) []kafkatypes.NodeInfo {
	if n <= 0 {
		return nil
	}
	return make([]kafkatypes.NodeInfo, n)
}

// makeACLs — N zero-value ACL entries; the cap rule reads `len(...)` only.
func makeACLs(n int) []types.Acls {
	if n <= 0 {
		return []types.Acls{}
	}
	return make([]types.Acls, n)
}

// ensureTopicsSummary applies topic / partition counts onto
// Topics.Summary, creating Topics if it was nil.
func ensureTopicsSummary(c *types.ProcessedCluster, e types.ClusterPlanInputs) {
	if c.KafkaAdminClientInformation.Topics == nil {
		c.KafkaAdminClientInformation.Topics = &types.Topics{}
	}
	if e.TopicCount != nil {
		c.KafkaAdminClientInformation.Topics.Summary.Topics = *e.TopicCount
	}
	if e.PartitionCount != nil {
		c.KafkaAdminClientInformation.Topics.Summary.TotalPartitions = *e.PartitionCount
	}
}

// applyThroughputAggregates writes declared peak / P95 MBps into
// ClusterMetrics.Aggregates (converted to bytes/sec, the unit
// sizing.go reads). When only peak is declared, it doubles as P95 —
// conservative oversizing vs. real CloudWatch P95, but the
// alternative (sizing-degraded) is worse when the customer
// deliberately provided a value.
func applyThroughputAggregates(c *types.ProcessedCluster, e types.ClusterPlanInputs) {
	if c.ClusterMetrics.Aggregates == nil {
		c.ClusterMetrics.Aggregates = map[string]types.MetricAggregate{}
	}
	in := c.ClusterMetrics.Aggregates["BytesInPerSec"]
	out := c.ClusterMetrics.Aggregates["BytesOutPerSec"]
	if e.PeakIngressMBps != nil {
		v := *e.PeakIngressMBps * bytesPerMB
		in.Maximum = &v
		if e.P95IngressMBps == nil {
			in.P95 = &v // peak → P95 fallback
		}
	}
	if e.P95IngressMBps != nil {
		v := *e.P95IngressMBps * bytesPerMB
		in.P95 = &v
	}
	if e.PeakEgressMBps != nil {
		v := *e.PeakEgressMBps * bytesPerMB
		out.Maximum = &v
		if e.P95EgressMBps == nil {
			out.P95 = &v
		}
	}
	if e.P95EgressMBps != nil {
		v := *e.P95EgressMBps * bytesPerMB
		out.P95 = &v
	}
	c.ClusterMetrics.Aggregates["BytesInPerSec"] = in
	c.ClusterMetrics.Aggregates["BytesOutPerSec"] = out
}
