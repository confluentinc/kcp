package execute

import (
	"context"
	"errors"
	"fmt"

	"github.com/IBM/sarama"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/groupoffsets"
	"github.com/confluentinc/kcp/internal/services/migplan"
	"github.com/confluentinc/kcp/internal/services/migration/d2s"
	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/types"
)

// offsetProvidersFunc builds the source and destination offset providers a
// run needs, plus a close function for both.
type offsetProvidersFunc func(g *manifest.GatewayMigration) (source, destination offset.Provider, closeFn func() error, err error)

// gatewayServiceFunc builds the gateway.Service the fence/switch steps use.
type gatewayServiceFunc func(g *manifest.GatewayMigration) (gateway.Service, error)

// clusterLinkServiceFunc builds the clusterlink.Service the promote step (and
// the static branch's offset-sync pause/restore) uses.
type clusterLinkServiceFunc func(g *manifest.GatewayMigration) (clusterlink.Service, error)

// executorDependencies builds every branch's downstream services from the
// manifest. newMigrationExecuteCmd injects it so this package's tests (whose
// manifests point at unreachable placeholder endpoints) can pass stubs;
// production uses liveExecutorDependencies.
type executorDependencies struct {
	offsets     offsetProvidersFunc
	gateway     gatewayServiceFunc
	clusterLink clusterLinkServiceFunc
	// convert builds a route conversion's services besides the gateway. Only
	// runConvertBranch calls it.
	convert convertServicesFunc
}

// liveExecutorDependencies dials the real source and destination Kafka
// clusters, Kubernetes, and the cluster-link REST endpoint.
var liveExecutorDependencies = executorDependencies{
	offsets:     buildOffsetProviders,
	gateway:     buildGatewayService,
	clusterLink: buildClusterLinkService,
	convert:     buildConvertServices,
}

// executorServices is everything a branch drives its FSM with, built the same
// way for both.
type executorServices struct {
	sourceOffset      offset.Provider
	destinationOffset offset.Provider
	gateway           gateway.Service
	clusterLink       clusterlink.Service
	// restAuth authenticates the cluster-link REST API with the link
	// credentials, which may name a broader principal than the destination
	// Kafka credentials — the two are kept separate so neither leg silently
	// authenticates as the other.
	restAuth clusterlink.Authenticator
	close    func() error
}

// buildExecutorServices builds a run's services through deps. The caller
// defers close.
func buildExecutorServices(g *manifest.GatewayMigration, deps executorDependencies) (executorServices, error) {
	restCreds, err := g.RestCredentials()
	if err != nil {
		return executorServices{}, fmt.Errorf("failed to resolve cluster-link REST credentials: %w", err)
	}
	sourceOffset, destinationOffset, closeOffsets, err := deps.offsets(g)
	if err != nil {
		return executorServices{}, fmt.Errorf("failed to connect to source/destination clusters: %w", err)
	}
	gatewayService, err := deps.gateway(g)
	if err != nil {
		_ = closeOffsets()
		return executorServices{}, fmt.Errorf("failed to build gateway service: %w", err)
	}
	clusterLinkService, err := deps.clusterLink(g)
	if err != nil {
		_ = closeOffsets()
		return executorServices{}, fmt.Errorf("failed to build cluster-link service: %w", err)
	}
	return executorServices{
		sourceOffset:      sourceOffset,
		destinationOffset: destinationOffset,
		gateway:           gatewayService,
		clusterLink:       clusterLinkService,
		restAuth:          restCreds.Authenticator(),
		close:             closeOffsets,
	}, nil
}

// sourceConn is the source leg's bootstrap servers, auth method and TLS trust,
// passed through from the manifest credentials.
func sourceConn(g *manifest.GatewayMigration) (types.KafkaSourceConn, error) {
	creds, errs := g.SourceCredentials()
	if len(errs) > 0 {
		return types.KafkaSourceConn{}, manifest.JoinProblems("spec.source.credentials", errs)
	}
	return types.MigrateConn(g.Spec.Source.BootstrapServers, creds), nil
}

// destinationConn is the destination Kafka leg's connection, from its own
// clusterCredentials — never the cluster-link REST credentials.
//
// A SASL/PLAIN destination with neither ca_cert nor tls set defaults to TLS:
// the destination is a managed/production cluster, always TLS, unlike a source
// which may legitimately be on-prem plaintext.
func destinationConn(g *manifest.GatewayMigration) (types.KafkaSourceConn, error) {
	if g.Spec.Target.Kafka == nil {
		return types.KafkaSourceConn{}, fmt.Errorf("spec.target.kafka: required")
	}
	creds, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		return types.KafkaSourceConn{}, manifest.JoinProblems("spec.target.kafka.clusterCredentials", errs)
	}
	conn := types.MigrateConn(g.Spec.Target.Kafka.BootstrapServers, creds)
	if sp := conn.AuthMethod.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}
	return conn, nil
}

// buildOffsetProviders opens real Kafka connections to the source and
// destination clusters described in the manifest.
func buildOffsetProviders(g *manifest.GatewayMigration) (offset.Provider, offset.Provider, func() error, error) {
	srcConn, err := sourceConn(g)
	if err != nil {
		return nil, nil, nil, err
	}
	destConn, err := destinationConn(g)
	if err != nil {
		return nil, nil, nil, err
	}
	srcClient, err := newKafkaClientForConn(srcConn)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("connecting to source cluster: %w", err)
	}
	destClient, err := newKafkaClientForConn(destConn)
	if err != nil {
		_ = srcClient.Close()
		return nil, nil, nil, fmt.Errorf("connecting to destination cluster: %w", err)
	}
	closeFn := func() error {
		return errors.Join(srcClient.Close(), destClient.Close())
	}
	return offset.NewOffsetService(srcClient), offset.NewOffsetService(destClient), closeFn, nil
}

// authForConn resolves conn's auth into the shared admin option, and the IAM
// region when the auth is IAM.
func authForConn(conn types.KafkaSourceConn) (string, client.AdminOption, error) {
	authType, err := conn.GetSelectedAuthType()
	if err != nil {
		return "", nil, fmt.Errorf("determining auth type: %w", err)
	}
	region := ""
	if authType == types.AuthTypeIAM && conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	authOpt, err := client.AdminOptionForAuthMethod(authType, conn.AuthMethod, conn.InsecureSkipTLSVerify)
	if err != nil {
		return "", nil, fmt.Errorf("resolving auth option: %w", err)
	}
	return region, authOpt, nil
}

// newKafkaClientForConn resolves conn's auth option and dials it as a
// sarama.Client, for offset.NewOffsetService. extra options follow the auth
// option (a conversion's high-water-mark sweep passes
// client.WithTopicAutoCreationDisabled).
func newKafkaClientForConn(conn types.KafkaSourceConn, extra ...client.AdminOption) (sarama.Client, error) {
	region, authOpt, err := authForConn(conn)
	if err != nil {
		return nil, err
	}
	return client.NewKafkaClient(conn.BootstrapServers, region, append([]client.AdminOption{authOpt}, extra...)...)
}

// newConsumerGroupClientForConn dials conn as a consumer-group client (pinned
// at Kafka 3.8.0), for a conversion's per-worker offset fetches and commits.
func newConsumerGroupClientForConn(conn types.KafkaSourceConn) (*client.ConsumerGroupClient, error) {
	region, authOpt, err := authForConn(conn)
	if err != nil {
		return nil, err
	}
	return client.NewConsumerGroupClient(conn.BootstrapServers, region, authOpt)
}

// buildGatewayService opens a real gateway.Service against the manifest's
// kubeconfig (see manifest.GatewayMigration.KubeconfigPath for its default).
func buildGatewayService(g *manifest.GatewayMigration) (gateway.Service, error) {
	kubeconfig, err := g.KubeconfigPath()
	if err != nil {
		return nil, err
	}
	return gateway.NewK8sService(kubeconfig), nil
}

// buildClusterLinkService opens a real clusterlink.Service using the
// manifest's cluster-link REST credentials.
func buildClusterLinkService(g *manifest.GatewayMigration) (clusterlink.Service, error) {
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

// buildConvertServices builds a route conversion's services from the manifest
// (spec "Command-layer wiring"): the post-fence facts through
// migplan.GatherConvertFacts over providers built the way migplan.Reconcile
// builds its own; the strict source and destination group listings and the
// destination topic listing from those same providers; one consumer-group
// client per worker on each cluster for the snapshots and the commits; and the
// high-water-mark sweep over a destination client that never auto-creates a
// topic.
func buildConvertServices(g *manifest.GatewayMigration) (convertServices, error) {
	if g.Spec.Route.ConvertTo == "" {
		return convertServices{}, fmt.Errorf("spec.route.convertTo is not set: this manifest is not a route conversion")
	}
	in, err := migplan.BuildReconcileInput(g)
	if err != nil {
		return convertServices{}, err
	}
	srcConn, err := sourceConn(g)
	if err != nil {
		return convertServices{}, err
	}
	dstConn, err := destinationConn(g)
	if err != nil {
		return convertServices{}, err
	}
	providers, providersCloser, err := migplan.BuildConvertProviders(g)
	if err != nil {
		return convertServices{}, err
	}
	sweepClient, err := newKafkaClientForConn(dstConn, client.WithTopicAutoCreationDisabled())
	if err != nil {
		_ = providersCloser.Close()
		return convertServices{}, fmt.Errorf("connecting to the destination cluster for the high-water-mark sweep: %w", err)
	}

	return convertServices{
		deps: d2s.Dependencies{
			Input: in,
			Gather: func(ctx context.Context) (*migplan.ConvertFacts, error) {
				return migplan.GatherConvertFacts(ctx, providers)
			},
			SourceGroups:      groupIDs(providers.SourceGroups),
			DestinationGroups: providers.TargetGroups.ListGroups,
			Fetchers: func() (groupoffsets.GroupOffsetFetcher, func(), error) {
				c, err := newConsumerGroupClientForConn(srcConn)
				if err != nil {
					return nil, nil, fmt.Errorf("connecting to the source cluster: %w", err)
				}
				return c, func() { _ = c.Close() }, nil
			},
			Wait:              groupoffsets.Sleep,
			DestinationTopics: providers.Target.ListTopics,
			HighWaterMarks:    offset.NewOffsetService(sweepClient),
			Committers: func() (groupoffsets.GroupCommitter, func(), error) {
				c, err := newConsumerGroupClientForConn(dstConn)
				if err != nil {
					return nil, nil, fmt.Errorf("connecting to the destination cluster: %w", err)
				}
				return c, func() { _ = c.Close() }, nil
			},
		},
		close: func() error { return errors.Join(sweepClient.Close(), providersCloser.Close()) },
	}, nil
}

// groupIDs lists l's groups by id only: the snapshots' group lister.
func groupIDs(l migplan.GroupLister) groupoffsets.GroupLister {
	return func(ctx context.Context) ([]string, error) {
		listings, err := l.ListGroups(ctx)
		if err != nil {
			return nil, err
		}
		ids := make([]string, 0, len(listings))
		for _, g := range listings {
			ids = append(ids, g.GroupID)
		}
		return ids, nil
	}
}
