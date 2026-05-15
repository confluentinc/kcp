package clusters

import (
	"bytes"
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeSource is a minimal sources.Source stub for scanner unit tests. It does
// not delegate to real Kafka — GetKafkaAdminForCluster returns whatever the
// test configures.
type fakeSource struct {
	sourceType types.SourceType
	adminByID  map[string]client.KafkaAdmin
	errByID    map[string]error
}

func (f *fakeSource) Type() types.SourceType {
	if f.sourceType == "" {
		return types.SourceTypeOSK
	}
	return f.sourceType
}
func (f *fakeSource) LoadCredentials(_ string) error { return nil }
func (f *fakeSource) Scan(_ context.Context, _ sources.ScanOptions) (*sources.ScanResult, error) {
	return nil, nil
}
func (f *fakeSource) GetClusters() []sources.ClusterIdentifier { return nil }
func (f *fakeSource) GetKafkaAdminForCluster(id string, _ *types.State) (client.KafkaAdmin, error) {
	if err, ok := f.errByID[id]; ok {
		return nil, err
	}
	return f.adminByID[id], nil
}

func TestScanner_ClusterNotInCredentials_ReturnsError(t *testing.T) {
	src := &fakeSource{
		errByID: map[string]error{
			"missing-cluster": errors.New(`cluster "missing-cluster" not found in OSK credentials`),
		},
	}
	scanner := NewConnectClustersScanner(ConnectClustersScannerOpts{
		Source:    src,
		ClusterID: "missing-cluster",
		Topics:    []string{"connect-status"},
		Stdout:    &bytes.Buffer{},
		Stderr:    &bytes.Buffer{},
	})

	err := scanner.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "missing-cluster")
}

func TestScanner_AdminCreationFails_ReturnsError(t *testing.T) {
	src := &fakeSource{
		errByID: map[string]error{
			"c1": errors.New("auth failed"),
		},
	}
	var stdout, stderr bytes.Buffer
	scanner := NewConnectClustersScanner(ConnectClustersScannerOpts{
		Source:    src,
		ClusterID: "c1",
		Topics:    []string{"connect-status"},
		Stdout:    &stdout,
		Stderr:    &stderr,
	})

	err := scanner.Run(context.Background())
	require.Error(t, err)
	assert.Contains(t, err.Error(), "auth failed")
	assert.Empty(t, stdout.String())
}
