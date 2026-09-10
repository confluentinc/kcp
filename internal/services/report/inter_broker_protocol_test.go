package report

import (
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
)

const ibpArn = "arn:aws:kafka:us-east-1:111122223333:configuration/demo/abc-123"

// clusterWithConfig builds a DiscoveredCluster whose provisioned broker points at
// the given configuration ARN and (optionally) revision. A nil revision models the
// missing-revision case the resolver must handle without guessing.
func clusterWithConfig(arn string, revision *int64) types.DiscoveredCluster {
	return types.DiscoveredCluster{
		AWSClientInformation: types.AWSClientInformation{
			MskClusterConfig: kafkatypes.Cluster{
				Provisioned: &kafkatypes.Provisioned{
					CurrentBrokerSoftwareInfo: &kafkatypes.BrokerSoftwareInfo{
						ConfigurationArn:      aws.String(arn),
						ConfigurationRevision: revision,
					},
				},
			},
		},
	}
}

func ibpRevision(rev int64, ibp string) kafka.DescribeConfigurationRevisionOutput {
	return kafka.DescribeConfigurationRevisionOutput{
		Arn:              aws.String(ibpArn),
		Revision:         aws.Int64(rev),
		ServerProperties: []byte("auto.create.topics.enable=false\ninter.broker.protocol.version=" + ibp + "\n"),
	}
}

func TestResolveInterBrokerProtocol(t *testing.T) {
	tests := []struct {
		name    string
		configs []kafka.DescribeConfigurationRevisionOutput
		cluster types.DiscoveredCluster
		want    string
	}{
		{
			name: "missing revision picks the latest captured revision, not the first in the slice",
			// Revisions are out of order and the FIRST entry is below the floor. The
			// old logic returned that first match; the fix must select revision 3.
			configs: []kafka.DescribeConfigurationRevisionOutput{
				ibpRevision(1, "2.6"),
				ibpRevision(3, "2.8"),
				ibpRevision(2, "2.7"),
			},
			cluster: clusterWithConfig(ibpArn, nil),
			want:    "Yes",
		},
		{
			name: "missing revision, latest is below the floor",
			configs: []kafka.DescribeConfigurationRevisionOutput{
				ibpRevision(2, "2.7"),
				ibpRevision(1, "3.0"),
			},
			cluster: clusterWithConfig(ibpArn, nil),
			want:    "No",
		},
		{
			name: "known revision matches exactly, not merely the first ARN match",
			configs: []kafka.DescribeConfigurationRevisionOutput{
				ibpRevision(1, "2.5"),
				ibpRevision(2, "2.8"),
			},
			cluster: clusterWithConfig(ibpArn, aws.Int64(2)),
			want:    "Yes",
		},
		{
			name: "known revision with no exact match is indeterminate, never a wrong-revision fallback",
			configs: []kafka.DescribeConfigurationRevisionOutput{
				ibpRevision(1, "2.5"),
			},
			cluster: clusterWithConfig(ibpArn, aws.Int64(5)),
			want:    "",
		},
		{
			name:    "no configuration ARN",
			configs: []kafka.DescribeConfigurationRevisionOutput{ibpRevision(1, "2.8")},
			cluster: types.DiscoveredCluster{},
			want:    "",
		},
		{
			name: "matching config but no IBP property is indeterminate",
			configs: []kafka.DescribeConfigurationRevisionOutput{
				{Arn: aws.String(ibpArn), Revision: aws.Int64(1), ServerProperties: []byte("auto.create.topics.enable=false\n")},
			},
			cluster: clusterWithConfig(ibpArn, aws.Int64(1)),
			want:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := resolveInterBrokerProtocol(tt.configs, tt.cluster)
			if got != tt.want {
				t.Errorf("resolveInterBrokerProtocol() = %q, want %q", got, tt.want)
			}
		})
	}
}
