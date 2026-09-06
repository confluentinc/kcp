package kafka

import (
	"errors"
	"testing"

	"github.com/IBM/sarama"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/mocks"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestKafkaService_scanConsumerGroups_TypeDispatch exercises the corrected
// type-dispatch rule (design §14.5): DescribeGroups (classic API 15) is called
// ONLY for classic and unknown-type ("") groups. Consumer/share/streams groups
// must NOT be described — asked about one, a real 4.x broker answers API 15 with
// state "Dead" and no members (reproduced live against MSK 4.0), which would
// wrongly overwrite the correct state ListGroupsWithType already reported. The
// test proves those groups keep their ListGroups v5 state, carry no members, and
// are flagged DetailComplete=false — and that DescribeGroups is never asked about
// them in the first place.
func TestKafkaService_scanConsumerGroups_TypeDispatch(t *testing.T) {
	listings := []types.ConsumerGroupListing{
		{GroupID: "classic-group", Type: types.ConsumerGroupTypeClassic, State: "Stable"},
		{GroupID: "consumer-group", Type: types.ConsumerGroupTypeConsumer, State: "Stable"},
		{GroupID: "share-group", Type: types.ConsumerGroupTypeShare, State: "Stable"},
		{GroupID: "legacy-group", Type: "", State: "Stable"},
	}

	// The broker's classic-API-15 answers. Note consumer/share carry State "Dead"
	// with no members — exactly what a 4.x broker returns for a non-classic group
	// over API 15. If the fix regressed and these were described, the "Dead"
	// state would leak into the result and the assertions below would fail.
	describeByID := map[string]*sarama.GroupDescription{
		"classic-group": {
			GroupId:      "classic-group",
			State:        "Stable",
			ProtocolType: "consumer",
			Members: map[string]*sarama.GroupMemberDescription{
				"m1": {MemberId: "m1", ClientId: "client-1", ClientHost: "/10.0.0.1"},
			},
		},
		"consumer-group": {
			GroupId: "consumer-group",
			State:   "Dead",
		},
		"share-group": {
			GroupId: "share-group",
			State:   "Dead",
		},
		"legacy-group": {
			GroupId:      "legacy-group",
			State:        "Stable",
			ProtocolType: "consumer",
		},
	}

	coordinators := map[string]string{
		"classic-group":  "broker-1:9092",
		"consumer-group": "broker-2:9092",
	}

	var describeCalledWith []string
	mockScanner := &mocks.MockConsumerGroupScanner{
		ListGroupsWithTypeFunc: func() ([]types.ConsumerGroupListing, error) {
			return listings, nil
		},
		// Honour the requested ids, like a real broker — return a description
		// only for a group actually asked about.
		DescribeGroupsFunc: func(groupIDs []string) ([]*sarama.GroupDescription, error) {
			describeCalledWith = append([]string(nil), groupIDs...)
			out := make([]*sarama.GroupDescription, 0, len(groupIDs))
			for _, id := range groupIDs {
				if gd, ok := describeByID[id]; ok {
					out = append(out, gd)
				}
			}
			return out, nil
		},
		CoordinatorsFunc: func(groupIDs []string) map[string]string {
			return coordinators
		},
	}

	ks := &KafkaService{
		groupScanner: mockScanner,
		clusterArn:   "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
	}

	groups, err := ks.scanConsumerGroups()
	require.NoError(t, err)
	require.NotNil(t, groups)
	require.Len(t, groups.Details, 4)

	// DescribeGroups must have been asked about ONLY the classic + unknown groups.
	assert.ElementsMatch(t, []string{"classic-group", "legacy-group"}, describeCalledWith,
		"DescribeGroups must be called only for classic/unknown-type groups")

	byID := make(map[string]types.ConsumerGroupDetails, len(groups.Details))
	for _, d := range groups.Details {
		byID[d.GroupID] = d
	}

	classic := byID["classic-group"]
	assert.True(t, classic.DetailComplete, "classic groups must be DetailComplete")
	assert.Equal(t, types.ConsumerGroupTypeClassic, classic.Type)
	assert.Equal(t, "Stable", classic.State)
	assert.Equal(t, "broker-1:9092", classic.Coordinator)
	require.Len(t, classic.Members, 1)
	assert.Equal(t, "client-1", classic.Members[0].ClientID)

	legacy := byID["legacy-group"]
	assert.True(t, legacy.DetailComplete, "groups with unreported type (\"\") must be DetailComplete")
	assert.Equal(t, "", legacy.Type)
	assert.Equal(t, "Stable", legacy.State)

	// The crux: consumer/share groups keep their ListGroups v5 state (Stable),
	// NOT the broker's misleading classic-API "Dead", and carry no members.
	consumer := byID["consumer-group"]
	assert.False(t, consumer.DetailComplete, "consumer groups must not be DetailComplete")
	assert.Equal(t, types.ConsumerGroupTypeConsumer, consumer.Type)
	assert.Equal(t, "Stable", consumer.State, "consumer-group must keep its ListGroups v5 state, not the classic-API 'Dead'")
	assert.Empty(t, consumer.Members, "consumer groups get no member detail from the classic API")
	assert.Equal(t, "broker-2:9092", consumer.Coordinator)

	share := byID["share-group"]
	assert.False(t, share.DetailComplete, "share groups must not be DetailComplete")
	assert.Equal(t, types.ConsumerGroupTypeShare, share.Type)
	assert.Equal(t, "Stable", share.State, "share-group must keep its ListGroups v5 state, not the classic-API 'Dead'")
	assert.Empty(t, share.Members)
}

// TestKafkaService_ScanKafkaResources_ConsumerGroupsResilience proves a
// group-discovery failure degrades rather than aborts the scan (design §8):
// when ListGroupsWithType fails (e.g. missing DescribeGroup authz),
// ScanKafkaResources must still succeed and record an empty (non-nil)
// ConsumerGroups rather than failing the whole scan.
func TestKafkaService_ScanKafkaResources_ConsumerGroupsResilience(t *testing.T) {
	mockAdmin := &mocks.MockKafkaAdmin{
		GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
			return &client.ClusterKafkaMetadata{ClusterID: "test-cluster"}, nil
		},
		ListTopicsWithConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{}, nil
		},
		ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
			return []sarama.ResourceAcls{}, nil
		},
	}

	mockScanner := &mocks.MockConsumerGroupScanner{
		ListGroupsWithTypeFunc: func() ([]types.ConsumerGroupListing, error) {
			return nil, errors.New("authorization failed: missing DescribeGroup")
		},
	}

	ks := NewKafkaService(mockAdmin, mockScanner, KafkaServiceOpts{
		AuthType:   types.AuthTypeIAM,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
	})

	result, err := ks.ScanKafkaResources(kafkatypes.ClusterTypeProvisioned)
	require.NoError(t, err, "a consumer-group scan failure must not abort the overall scan")
	require.NotNil(t, result)
	require.NotNil(t, result.ConsumerGroups, "ConsumerGroups must be recorded as scanned-but-empty, not left unset")
	assert.Len(t, result.ConsumerGroups.Details, 0)
}

// TestKafkaService_ScanKafkaResources_ConsumerGroupsSkip proves
// SkipConsumerGroups bypasses group discovery entirely: ConsumerGroups stays
// nil (not scanned), and the scanner is never invoked.
func TestKafkaService_ScanKafkaResources_ConsumerGroupsSkip(t *testing.T) {
	mockAdmin := &mocks.MockKafkaAdmin{
		GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
			return &client.ClusterKafkaMetadata{ClusterID: "test-cluster"}, nil
		},
		ListTopicsWithConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{}, nil
		},
		ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
			return []sarama.ResourceAcls{}, nil
		},
	}

	scannerCalled := false
	mockScanner := &mocks.MockConsumerGroupScanner{
		ListGroupsWithTypeFunc: func() ([]types.ConsumerGroupListing, error) {
			scannerCalled = true
			return nil, nil
		},
	}

	ks := NewKafkaService(mockAdmin, mockScanner, KafkaServiceOpts{
		AuthType:           types.AuthTypeIAM,
		ClusterArn:         "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
		SkipConsumerGroups: true,
	})

	result, err := ks.ScanKafkaResources(kafkatypes.ClusterTypeProvisioned)
	require.NoError(t, err)
	require.NotNil(t, result)
	assert.Nil(t, result.ConsumerGroups, "ConsumerGroups must stay nil when SkipConsumerGroups is set")
	assert.False(t, scannerCalled, "the consumer-group scanner must not be invoked when SkipConsumerGroups is set")
}

// TestKafkaService_ScanKafkaResources_PerGroupDescribeDenialTolerated proves a
// per-group describe denial (sarama returns the group with Err set to a KError
// field, e.g. ErrGroupAuthorizationFailed, rather than as a Go error) is
// tolerated: the group is still recorded (without full detail) rather than the
// whole scan aborting (design §8, fix 1b).
func TestKafkaService_ScanKafkaResources_PerGroupDescribeDenialTolerated(t *testing.T) {
	mockAdmin := &mocks.MockKafkaAdmin{
		GetClusterKafkaMetadataFunc: func() (*client.ClusterKafkaMetadata, error) {
			return &client.ClusterKafkaMetadata{ClusterID: "test-cluster"}, nil
		},
		ListTopicsWithConfigsFunc: func() (map[string]sarama.TopicDetail, error) {
			return map[string]sarama.TopicDetail{}, nil
		},
		ListAclsFunc: func() ([]sarama.ResourceAcls, error) {
			return []sarama.ResourceAcls{}, nil
		},
	}

	listings := []types.ConsumerGroupListing{
		{GroupID: "denied-group", Type: types.ConsumerGroupTypeClassic, State: "Stable"},
	}
	descriptions := []*sarama.GroupDescription{
		{
			GroupId: "denied-group",
			Err:     sarama.ErrGroupAuthorizationFailed,
			Members: map[string]*sarama.GroupMemberDescription{},
		},
	}

	mockScanner := &mocks.MockConsumerGroupScanner{
		ListGroupsWithTypeFunc: func() ([]types.ConsumerGroupListing, error) {
			return listings, nil
		},
		DescribeGroupsFunc: func(groupIDs []string) ([]*sarama.GroupDescription, error) {
			return descriptions, nil
		},
		CoordinatorsFunc: func(groupIDs []string) map[string]string {
			return map[string]string{}
		},
	}

	ks := NewKafkaService(mockAdmin, mockScanner, KafkaServiceOpts{
		AuthType:   types.AuthTypeIAM,
		ClusterArn: "arn:aws:kafka:us-east-1:123456789012:cluster/test/abc-123",
	})

	result, err := ks.ScanKafkaResources(kafkatypes.ClusterTypeProvisioned)
	require.NoError(t, err, "a per-group describe denial must not abort the overall scan")
	require.NotNil(t, result)
	require.NotNil(t, result.ConsumerGroups)
	require.Len(t, result.ConsumerGroups.Details, 1, "the denied group is still recorded, without full detail")
	assert.Equal(t, "denied-group", result.ConsumerGroups.Details[0].GroupID)
}
