package plan

import (
	"sort"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

// bytesPerMB matches the conversion sizing.go uses (binary MB).
const bytesPerMB = 1024.0 * 1024.0

// applyClusterDeclarations merges customer-declared per-cluster facts
// from plan-inputs.yaml into `state.Sources`. Two modes per entry under
// `inputs.Clusters`:
//
//   - **Override**: cluster name matches an existing scanned cluster —
//     customer fields overlay on top of the scan struct. Scan values
//     stay when the customer didn't declare an override.
//   - **Synthesise**: no scan match — a fresh ProcessedCluster is built
//     from the declarations and attached to the region named in
//     `entry.Region`. Synthesis requires `Region` so the cluster lands
//     in a real region bucket.
//
// Runs once at the top of `Build`, after `backfillAggregates`. Every
// downstream consumer (collectClusters, decideClusterType, sizing,
// detectRedFlags, etc.) reads the merged state and doesn't need to be
// aware that values came from plan-inputs vs the scanner.
func applyClusterDeclarations(state *types.ProcessedState, raw *types.PlanInputs) {
	if raw == nil || len(raw.Clusters) == 0 {
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

// indexClustersByName returns name → pointer-to-cluster for every MSK
// cluster currently in state, so callers can mutate the canonical
// instance (not a copy). Only MSK clusters are indexed; OSK clusters
// have their own out-of-scope path (osk_source_unsupported OQ).
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
// scan-equivalent field (anything beyond the existing decision-level
// overrides like TargetAuthMethod / DowntimeTolerance). Used to skip
// entries that ONLY override decision-level inputs — those need no
// state mutation.
func hasClusterDeclaration(e types.ClusterPlanInputs) bool {
	return e.Region != nil ||
		e.ClusterTypeFromScan != nil ||
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

// applyClusterOverlay overlays customer-declared fields onto an
// existing scanned cluster. Fields the customer didn't declare keep
// their scan-derived values.
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
		if e.ClusterTypeFromScan != nil {
			c.AWSClientInformation.MskClusterConfig.ClusterType = kafkatypes.ClusterType(*e.ClusterTypeFromScan)
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
// declaration. Used when no scan exists for `name`.
func synthesiseCluster(name string, e types.ClusterPlanInputs) types.ProcessedCluster {
	c := types.ProcessedCluster{Name: name}
	if e.Region != nil {
		c.Region = *e.Region
	}
	// Build the MskClusterConfig from declared facts. Default to
	// PROVISIONED when the customer doesn't say (matches AWS's most
	// common shape).
	clusterType := "PROVISIONED"
	if e.ClusterTypeFromScan != nil {
		clusterType = *e.ClusterTypeFromScan
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
	// Always create a Topics struct on a synthesised cluster so the
	// topic_inventory_empty OQ branches on "scanned and empty" rather
	// than "scan didn't run". The customer declared the cluster
	// existence; we treat that as an implicit "this part of the state
	// is intentional", not a scan gap.
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
		// Empty slice (not nil) so aclScanRan returns true — the
		// customer's declaration replaces the scan.
		c.KafkaAdminClientInformation.Acls = []types.Acls{}
	}
	if e.PeakIngressMBps != nil || e.PeakEgressMBps != nil || e.P95IngressMBps != nil || e.P95EgressMBps != nil {
		applyThroughputAggregates(&c, e)
	}
	return c
}

// attachClusterToRegion drops `c` into the region named `region`,
// creating the region bucket (and the MSK source bucket itself) if
// neither exists.
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

// provisionedShape returns the Provisioned block to write back to the
// cluster after applying customer overrides, plus a flag indicating
// whether the override pivoted to Serverless (in which case the
// returned *Provisioned is meaningless — caller switches to the
// Serverless path).
func provisionedShape(cfg *kafkatypes.Cluster, e types.ClusterPlanInputs) (*kafkatypes.Provisioned, bool) {
	if e.ClusterTypeFromScan != nil && *e.ClusterTypeFromScan == "SERVERLESS" {
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

// buildProvisionedBlock returns a fresh Provisioned struct for the
// synthesis path.
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

// buildServerlessBlock returns a Serverless struct with the customer's
// declared auth methods. Serverless only supports IAM SASL today; any
// other declared auth is silently dropped (with the assumption that
// the customer made a typo — the Plan's existing auth_posture_unknown
// OQ surfaces it).
func buildServerlessBlock(e types.ClusterPlanInputs) *kafkatypes.Serverless {
	srv := &kafkatypes.Serverless{
		VpcConfigs: []kafkatypes.VpcConfig{}, // empty but non-nil so isServerless still resolves correctly
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

// buildClientAuth maps the declared auth-method enum to an AWS
// ClientAuthentication struct. Recognised tokens: scram, iam, mtls,
// unauth. Unknown tokens are silently dropped — the customer's typo
// surfaces via the existing auth_posture_unknown OQ if the resulting
// block has zero enabled methods.
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

// makeBrokerNodes returns N zero-value NodeInfo entries — the Plan
// only reads `len(c.AWSClientInformation.Nodes)`, so the contents
// don't matter beyond the count.
func makeBrokerNodes(n int) []kafkatypes.NodeInfo {
	if n <= 0 {
		return nil
	}
	return make([]kafkatypes.NodeInfo, n)
}

// makeACLs returns N zero-value ACL entries. Same rationale as
// makeBrokerNodes — the cap rule reads `len(...)` only.
func makeACLs(n int) []types.Acls {
	if n <= 0 {
		return []types.Acls{}
	}
	return make([]types.Acls, n)
}

// ensureTopicsSummary applies the topic / partition counts to the
// cluster's Topics.Summary block, creating Topics if it was nil.
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

// applyThroughputAggregates writes the customer's declared peak / p95
// MBps values into the cluster's metric-aggregate map (converted to
// bytes/sec, the unit sizing.go reads). When only peak is declared,
// peak doubles as P95 — conservative oversizing relative to a real
// CloudWatch P95, but the alternative (sizing-degraded) is worse for
// a customer who deliberately declared a value.
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
			in.P95 = &v // peak doubles as P95 when P95 not declared
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
