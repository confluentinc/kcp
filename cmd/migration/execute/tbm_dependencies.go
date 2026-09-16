package execute

import (
	"errors"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
)

// offsetProvidersFunc builds the source and destination offset providers a
// dynamic-mode (TBM) run needs, plus a close function for both. Injected via
// newMigrationExecuteCmd so this package's own tests (whose manifests point
// at unreachable placeholder endpoints) can pass a stub; production dials
// real Kafka connections. Ported from cmd/migration/executetbm, deleted in
// Task 1.
type offsetProvidersFunc func(g *manifest.GatewayMigration) (source, destination offset.Provider, closeFn func() error, err error)

// gatewayServiceFunc builds the gateway.Service a dynamic-mode run's
// fence/switch steps use. Injected via newMigrationExecuteCmd so tests can
// pass a stub instead of dialing a real Kubernetes cluster; production
// builds a real K8sService from the manifest's kubeconfig.
type gatewayServiceFunc func(g *manifest.GatewayMigration) (gateway.Service, error)

// clusterLinkServiceFunc builds the clusterlink.Service a dynamic-mode run's
// promote step uses. Injected via newMigrationExecuteCmd so tests can pass a
// stub instead of dialing a real cluster-link REST endpoint; production
// builds a real ConfluentCloudService from the manifest's destination REST
// credentials.
type clusterLinkServiceFunc func(g *manifest.GatewayMigration) (clusterlink.Service, error)

// buildTBMOffsetProviders opens real Kafka connections to the source and
// destination clusters described in the manifest, for wait_for_lags. Ported
// verbatim from cmd/migration/executetbm/cmd_migration_executetbm.go's
// buildOffsetProviders.
func buildTBMOffsetProviders(g *manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
	srcCreds, errs := g.SourceCredentials()
	if len(errs) > 0 {
		return nil, nil, nil, manifest.JoinProblems("spec.source.credentials", errs)
	}
	srcConn := types.MigrateConn(g.Spec.Source.BootstrapServers, srcCreds)
	srcClient, err := newTBMKafkaClientForConn(srcConn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connecting to source cluster: %w", err)
	}

	if g.Spec.Target.Kafka == nil {
		_ = srcClient.Close()
		return nil, nil, nil, fmt.Errorf("spec.target.kafka: required")
	}
	destCreds, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		_ = srcClient.Close()
		return nil, nil, nil, manifest.JoinProblems("spec.target.kafka.clusterCredentials", errs)
	}
	destConn := types.MigrateConn(g.Spec.Target.Kafka.BootstrapServers, destCreds)
	if sp := destConn.AuthMethod.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}
	destClient, err := newTBMKafkaClientForConn(destConn)
	if err != nil {
		_ = srcClient.Close()
		return nil, nil, nil, fmt.Errorf("connecting to destination cluster: %w", err)
	}

	closeFn := func() error {
		return errors.Join(srcClient.Close(), destClient.Close())
	}
	return offset.NewOffsetService(srcClient), offset.NewOffsetService(destClient), closeFn, nil
}

// newTBMKafkaClientForConn resolves conn's auth option and dials it as a
// sarama.Client, for offset.NewOffsetService. Ported verbatim from
// cmd/migration/executetbm's newKafkaClientForConn.
func newTBMKafkaClientForConn(conn types.KafkaSourceConn) (sarama.Client, error) {
	authType, err := conn.GetSelectedAuthType()
	if err != nil {
		return nil, fmt.Errorf("determining auth type: %w", err)
	}
	region := ""
	if authType == types.AuthTypeIAM && conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	authOpt, err := client.AdminOptionForAuthMethod(authType, conn.AuthMethod, conn.InsecureSkipTLSVerify)
	if err != nil {
		return nil, fmt.Errorf("resolving auth option: %w", err)
	}
	return client.NewKafkaClient(conn.BootstrapServers, region, authOpt)
}

// buildTBMGatewayService opens a real gateway.Service using the manifest's
// spec.gateway.kubeconfig. Ported verbatim from
// cmd/migration/executetbm's buildGatewayService.
func buildTBMGatewayService(g *manifest.GatewayMigration) (gateway.Service, error) {
	kubeconfig, err := g.KubeconfigPath()
	if err != nil {
		return nil, err
	}
	return gateway.NewK8sService(kubeconfig), nil
}

// buildTBMClusterLinkService opens a real clusterlink.Service using the
// manifest's cluster-link REST credentials. Ported verbatim from
// cmd/migration/executetbm's buildClusterLinkService.
func buildTBMClusterLinkService(g *manifest.GatewayMigration) (clusterlink.Service, error) {
	restCreds, err := g.RestCredentials()
	if err != nil {
		return nil, err
	}
	httpClient, err := restCreds.HTTPClient()
	if err != nil {
		return nil, err
	}
	return clusterlink.NewConfluentCloudService(httpClient), nil
}
