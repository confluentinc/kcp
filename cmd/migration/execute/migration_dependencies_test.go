package execute

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	k8stypes "k8s.io/apimachinery/pkg/types"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
)

// stubDeps is executorDependencies over the shared stubs below, with gw and cl
// substituted when non-nil so a test can record what reaches them. Both
// branches' tests drive their runs through it.
func stubDeps(gw gateway.Service, cl clusterlink.Service) executorDependencies {
	if gw == nil {
		gw = stubGatewayServiceImpl{}
	}
	if cl == nil {
		cl = stubClusterLinkServiceImpl{}
	}
	return executorDependencies{
		offsets:     stubOffsetProviders,
		gateway:     func(*manifest.GatewayMigration) (gateway.Service, error) { return gw, nil },
		clusterLink: func(*manifest.GatewayMigration) (clusterlink.Service, error) { return cl, nil },
	}
}

// --- stub downstream services, shared by both branches' tests ---

// zeroLagOffsetProvider implements offset.Provider, reporting the same fixed
// offset for every topic requested — used for both source and destination, so
// every topic sees zero lag (wait_for_lags passes any threshold; promote sees
// exact zero lag) without dialing anything.
type zeroLagOffsetProvider struct{}

func (zeroLagOffsetProvider) GetMany(_ context.Context, topics []string) (map[string]map[int32]int64, error) {
	out := make(map[string]map[int32]int64, len(topics))
	for _, topic := range topics {
		out[topic] = map[int32]int64{0: 1000}
	}
	return out, nil
}

func stubOffsetProviders(*manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
	return zeroLagOffsetProvider{}, zeroLagOffsetProvider{}, func() error { return nil }, nil
}

// stubGatewayServiceImpl implements gateway.Service with always-succeeding
// no-op behavior (VerifyRollout, so no configId is injected), so the fence and
// switch transitions walk without reaching a real Kubernetes cluster. The
// engine's own gateway behavior is exercised in internal/services/gateway and
// internal/services/migration/tbm, not duplicated here.
type stubGatewayServiceImpl struct{}

func (stubGatewayServiceImpl) GetGatewayYAML(context.Context, string, string) ([]byte, error) {
	return nil, fmt.Errorf("stubGatewayServiceImpl.GetGatewayYAML not implemented")
}

func (stubGatewayServiceImpl) DetectCapability(context.Context, string, string, int, []byte, []byte) (gateway.Capability, error) {
	return gateway.Capability{Mode: gateway.VerifyRollout}, nil
}

func (stubGatewayServiceImpl) WaitForGatewayConfigID(context.Context, string, string, gateway.ConfigWaitOptions) error {
	return nil
}

func (stubGatewayServiceImpl) CheckPermissions(context.Context, string, string, string, string) (bool, error) {
	return true, nil
}

func (stubGatewayServiceImpl) PatchGatewayRoute(context.Context, string, string, gateway.RoutePatch, string) (string, error) {
	return "", nil
}

func (stubGatewayServiceImpl) PatchGatewayConfigID(context.Context, string, string, string) (string, error) {
	return "", nil
}

func (stubGatewayServiceImpl) WaitForGatewayAccepted(context.Context, string, string, time.Duration, time.Duration) error {
	return nil
}

func (stubGatewayServiceImpl) GetGatewayPodUIDs(context.Context, string, string) (map[k8stypes.UID]struct{}, error) {
	return nil, fmt.Errorf("stubGatewayServiceImpl.GetGatewayPodUIDs not implemented")
}

func (stubGatewayServiceImpl) GetGatewayDeploymentGeneration(context.Context, string, string) (int64, error) {
	return 0, nil
}

func (stubGatewayServiceImpl) WaitForGatewayPods(context.Context, string, string, map[k8stypes.UID]struct{}, int64, time.Duration, time.Duration, func(gateway.PodRolloutProgress)) error {
	return fmt.Errorf("stubGatewayServiceImpl.WaitForGatewayPods not implemented")
}

func (stubGatewayServiceImpl) WaitForGatewayReady(context.Context, string, string, int64, time.Duration, time.Duration, func(gateway.GatewayReadinessProgress)) error {
	return nil
}

// stubClusterLinkServiceImpl implements clusterlink.Service, reporting every
// requested topic as already STOPPED and accepting every promote, so the
// promote transition walks without reaching a real cluster-link REST endpoint.
type stubClusterLinkServiceImpl struct{}

func (stubClusterLinkServiceImpl) GetClusterLink(context.Context, clusterlink.Config) (*clusterlink.ClusterLink, error) {
	return nil, fmt.Errorf("stubClusterLinkServiceImpl.GetClusterLink not implemented")
}

func (stubClusterLinkServiceImpl) ListMirrorTopics(_ context.Context, config clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	out := make([]clusterlink.MirrorTopic, len(config.Topics))
	for i, t := range config.Topics {
		out[i] = clusterlink.MirrorTopic{MirrorTopicName: t, MirrorStatus: clusterlink.MirrorStatusStopped}
	}
	return out, nil
}

func (stubClusterLinkServiceImpl) ListConfigs(context.Context, clusterlink.Config) (map[string]string, error) {
	return nil, fmt.Errorf("stubClusterLinkServiceImpl.ListConfigs not implemented")
}

func (stubClusterLinkServiceImpl) ValidateTopics([]string, []string) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.ValidateTopics not implemented")
}

func (stubClusterLinkServiceImpl) PromoteMirrorTopics(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	return promoteAll(topicNames), nil
}

func (stubClusterLinkServiceImpl) CreateMirrorTopic(context.Context, clusterlink.Config, string, string) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.CreateMirrorTopic not implemented")
}

func (stubClusterLinkServiceImpl) ListTopics(context.Context, clusterlink.Config) ([]string, error) {
	return nil, fmt.Errorf("stubClusterLinkServiceImpl.ListTopics not implemented")
}

func (stubClusterLinkServiceImpl) CreateTopic(context.Context, clusterlink.Config, clusterlink.CreateTopicRequest) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.CreateTopic not implemented")
}

func (stubClusterLinkServiceImpl) AlterConfigs(context.Context, clusterlink.Config, []clusterlink.ConfigAlteration) error {
	return fmt.Errorf("stubClusterLinkServiceImpl.AlterConfigs not implemented")
}

// promoteAll builds a response accepting every named topic (error_code 0).
func promoteAll(topicNames []string) *clusterlink.PromoteMirrorTopicsResponse {
	resp := &clusterlink.PromoteMirrorTopicsResponse{}
	for _, name := range topicNames {
		resp.Data = append(resp.Data, struct {
			MirrorTopicName string `json:"mirror_topic_name"`
			ErrorMessage    string `json:"error_message,omitempty"`
			ErrorCode       int    `json:"error_code,omitempty"`
		}{MirrorTopicName: name})
	}
	return resp
}

// recordingClusterLinkService is stubClusterLinkServiceImpl plus a record of
// the batch size passed to each PromoteMirrorTopics call, so a test can observe
// how many topics the promote transition submitted per batch — the only
// externally visible effect of tbm.TBMActions.SetPromoteBatchSize.
type recordingClusterLinkService struct {
	stubClusterLinkServiceImpl
	mu      sync.Mutex
	batches []int
}

func (r *recordingClusterLinkService) PromoteMirrorTopics(_ context.Context, _ clusterlink.Config, topicNames []string) (*clusterlink.PromoteMirrorTopicsResponse, error) {
	r.mu.Lock()
	r.batches = append(r.batches, len(topicNames))
	r.mu.Unlock()
	return promoteAll(topicNames), nil
}

func (r *recordingClusterLinkService) maxBatch() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	max := 0
	for _, n := range r.batches {
		if n > max {
			max = n
		}
	}
	return max
}

// recordingGatewayService is stubGatewayServiceImpl plus a record of the port
// passed to DetectCapability — the first place a gateway transition reads
// config.GatewayConfigPort (see tbm.resolveGatewayCapability). Observing it here
// proves the override reached config.GatewayConfigPort BEFORE the capability
// probe ran.
type recordingGatewayService struct {
	stubGatewayServiceImpl
	mu       sync.Mutex
	seenPort int
}

func (r *recordingGatewayService) DetectCapability(_ context.Context, _ string, _ string, port int, _ []byte, _ []byte) (gateway.Capability, error) {
	r.mu.Lock()
	r.seenPort = port
	r.mu.Unlock()
	return gateway.Capability{Mode: gateway.VerifyRollout}, nil
}

func (r *recordingGatewayService) port() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.seenPort
}

// TestSourceConn_CACertReachesEveryTLSPath: a source ca_cert must reach the
// CACert of every TLS-fronted auth method (SASL/SCRAM, SASL/PLAIN over TLS,
// mTLS, unauthenticated TLS) — not only mTLS — so a source behind a private CA
// can be verified. Driven from real manifest credential files through
// sourceConn, which both branches use.
func TestSourceConn_CACertReachesEveryTLSPath(t *testing.T) {
	dir := t.TempDir()
	file := func(name string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, []byte("pem"), 0o600))
		return p
	}
	ca, cert, key := file("source-ca.pem"), file("client.pem"), file("client-key.pem")

	for name, tc := range map[string]struct {
		block  string
		caCert func(types.AuthMethodConfig) string
	}{
		"sasl_scram": {"sasl_scram:\n  username: u\n  password: p\n  mechanism: SHA512\n  ca_cert: " + ca + "\n",
			func(m types.AuthMethodConfig) string { return m.SASLScram.CACert }},
		"sasl_plain": {"sasl_plain:\n  username: u\n  password: p\n  ca_cert: " + ca + "\n",
			func(m types.AuthMethodConfig) string { return m.SASLPlain.CACert }},
		"mtls": {"mtls:\n  ca_cert: " + ca + "\n  client_cert: " + cert + "\n  client_key: " + key + "\n",
			func(m types.AuthMethodConfig) string { return m.TLS.CACert }},
		"unauthenticated_tls": {"unauthenticated_tls:\n  ca_cert: " + ca + "\n",
			func(m types.AuthMethodConfig) string { return m.UnauthenticatedTLS.CACert }},
	} {
		t.Run(name, func(t *testing.T) {
			f := newFixtureCreds(t, credOverrides{source: tc.block}, nil)
			conn, err := sourceConn(loadGateway(t, f.manifestPath))
			require.NoError(t, err)
			assert.Equal(t, ca, tc.caCert(conn.AuthMethod))
		})
	}
}
