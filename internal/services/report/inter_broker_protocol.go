package report

import (
	"strconv"
	"strings"

	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"github.com/confluentinc/kcp/internal/types"
)

// resolveInterBrokerProtocol derives a cluster's inter-broker protocol (IBP)
// relative to the Cluster Linking floor of 2.8, from the MSK configuration the
// cluster runs. It matches the cluster's current configuration ARN against the
// region's captured configuration revisions and reads
// `inter.broker.protocol.version` from that revision's server.properties.
//
// Returns "Yes" (IBP >= 2.8), "No" (below 2.8), or "" when it can't be determined
// (no configuration, no property, or an unparseable value). "" means the plan asks
// for it, but only where it matters (the 2.4-2.9 Kafka band).
func resolveInterBrokerProtocol(configs []kafka.DescribeConfigurationRevisionOutput, cluster types.DiscoveredCluster) string {
	prov := cluster.AWSClientInformation.MskClusterConfig.Provisioned
	if prov == nil || prov.CurrentBrokerSoftwareInfo == nil || prov.CurrentBrokerSoftwareInfo.ConfigurationArn == nil {
		return ""
	}
	wantArn := *prov.CurrentBrokerSoftwareInfo.ConfigurationArn
	wantRev := int64(0)
	if prov.CurrentBrokerSoftwareInfo.ConfigurationRevision != nil {
		wantRev = *prov.CurrentBrokerSoftwareInfo.ConfigurationRevision
	}

	// Choose the configuration revision the cluster actually runs. When the cluster
	// reports its revision (wantRev != 0), match it exactly — never fall back to a
	// different (or unknown) revision of the same ARN, whose server.properties may
	// carry a different inter.broker.protocol.version. When the revision is missing
	// (wantRev == 0), the exact revision is indeterminate, so pick the highest
	// captured revision for the ARN (the latest) rather than whichever config merely
	// happens to come first in the slice.
	var chosen *kafka.DescribeConfigurationRevisionOutput
	chosenRev := int64(-1)
	for i := range configs {
		cfg := &configs[i]
		if cfg.Arn == nil || *cfg.Arn != wantArn {
			continue
		}
		if wantRev != 0 {
			if cfg.Revision != nil && *cfg.Revision == wantRev {
				chosen = cfg
				break
			}
			continue
		}
		rev := int64(0)
		if cfg.Revision != nil {
			rev = *cfg.Revision
		}
		if rev > chosenRev {
			chosenRev = rev
			chosen = cfg
		}
	}
	if chosen == nil {
		return ""
	}
	v := serverProperty(chosen.ServerProperties, "inter.broker.protocol.version")
	if v == "" {
		return ""
	}
	return ibpMeetsFloor(v)
}

// serverProperty reads one key from a Kafka server.properties byte blob. Blank and
// `#`-commented lines are skipped; the first match wins.
func serverProperty(props []byte, key string) string {
	for _, line := range strings.Split(string(props), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, val, ok := strings.Cut(line, "=")
		if ok && strings.TrimSpace(k) == key {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// ibpMeetsFloor reports whether an IBP version string is at or above 2.8 (the
// Cluster Linking floor): "Yes" if >= 2.8, "No" if below, "" if unparseable. IBP
// values can carry an "-IVn" suffix (for example "2.8-IV0"), which is ignored.
func ibpMeetsFloor(version string) string {
	version = strings.SplitN(version, "-", 2)[0]
	parts := strings.SplitN(version, ".", 3)
	if len(parts) < 2 {
		return ""
	}
	major, err1 := strconv.Atoi(parts[0])
	minor, err2 := strconv.Atoi(parts[1])
	if err1 != nil || err2 != nil {
		return ""
	}
	if major > 2 || (major == 2 && minor >= 8) {
		return "Yes"
	}
	return "No"
}
