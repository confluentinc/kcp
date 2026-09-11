package migplan

import (
	"context"
	"fmt"
	"io"
	"os"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
)

// defaultKafkaVersion mirrors the version the other migrate/scan admin builders
// pin (internal/migrate/source.go, internal/sources/osk). The exact protocol
// version is inert for topic listing; parity with the rest of KCP is what matters.
const defaultKafkaVersion = "3.6.0"

// Result is the reconciliation outcome for an in-code caller (e.g. the migration
// state machine). On success it carries the three artifacts; on an infeasible
// plan Refused is true, the three outputs are empty, and Reasons explains why.
// A returned error is an I/O failure, NOT a refusal — a refusal is data.
type Result struct {
	Route          string   // the gateway route the fence/switchover rules apply to (spec.topicGroup.route)
	Topics         []string // the promote list to feed to cluster-link promotion
	FenceYAML      string   // the whole rules: block, fenced
	SwitchoverYAML string   // the whole rules: block, switched over
	Refused        bool     // true ⇔ infeasible; the three above are empty
	Reasons        []string // why, when Refused (failed checks + blocked topics)

	// GatewayYAML is the whole gateway CR the engine pulled, exactly as read.
	// Set on both success and refusal (the pull precedes the checks). A caller
	// can re-pull the CR just before mutating it and diff against this to detect
	// drift since the plan was computed — diffing a stable sub-tree (e.g. spec),
	// not the raw bytes, which also carry volatile status/resourceVersion/
	// managedFields that change on every unrelated update.
	GatewayYAML string

	// Report is the full per-topic report, for rendering/diagnostics (the CLI
	// uses it). In-code callers can ignore it and use the fields above.
	Report reconcile.Report
}

// reconcileOptions holds the two dependencies Reconcile builds by default but
// lets a caller override: the gateway source and the report's destination.
type reconcileOptions struct {
	gateway GatewayConfigSource // nil ⇒ pull the live CR from spec.gateway
	out     io.Writer           // nil ⇒ os.Stdout
}

// Option customises Reconcile. Production and the state machine pass none.
type Option func(*reconcileOptions)

// WithGatewaySource overrides the gateway source. Tests use it to feed a static
// gateway fixture (NewGatewayFile) so the whole composition can be exercised
// without reaching Kubernetes; production pulls the live CR.
func WithGatewaySource(s GatewayConfigSource) Option {
	return func(o *reconcileOptions) { o.gateway = s }
}

// WithOutput redirects the plan report (default os.Stdout), so a caller can
// capture, quiet, or relocate it.
func WithOutput(w io.Writer) Option {
	return func(o *reconcileOptions) { o.out = w }
}

// Reconcile is the single in-code entry point: given the parsed manifest, it
// derives the selector from spec.topicGroup, pulls the live Gateway CR named in
// spec.gateway, reads live source/target/link state, runs the engine, renders
// the plan report, and returns the Result. It opens the cluster connections and
// closes them before returning. err is an I/O failure only; a refusal is
// Result.Refused.
//
// The engine owns its own narrative: every caller (the command, the migration
// state machine) gets the report shown without rendering it themselves, and
// consumes the returned Result for the machine-facing outputs. The gateway
// source and the report destination are injectable (WithGatewaySource /
// WithOutput) for testing without live Kubernetes; both default to production.
func Reconcile(ctx context.Context, g *manifest.GatewayMigration, opts ...Option) (*Result, error) {
	o := reconcileOptions{}
	for _, opt := range opts {
		opt(&o)
	}

	in, err := buildReconcileInput(g)
	if err != nil {
		return nil, err
	}

	gw := o.gateway
	if gw == nil {
		gw, err = buildGatewaySource(g, in.Route)
		if err != nil {
			return nil, err
		}
	}

	link, err := buildLinkStatusProvider(g)
	if err != nil {
		return nil, err
	}

	src, srcCloser, err := buildSourceTopicLister(g)
	if err != nil {
		return nil, err
	}
	defer func() { _ = srcCloser.Close() }()

	tgt, tgtCloser, err := buildTargetTopicLister(g)
	if err != nil {
		return nil, err
	}
	defer func() { _ = tgtCloser.Close() }()

	plan, err := NewReconciliationEngine(gw, src, tgt, link).Run(ctx, in)
	if err != nil {
		return nil, err
	}
	res := newResult(plan)
	// The route the artifacts apply to — where the caller grafts the rules.
	res.Route = in.Route

	// The engine renders its own report so no caller has to.
	out := o.out
	if out == nil {
		out = os.Stdout
	}
	tg := g.Spec.TopicGroup[0]
	RenderReport(out, res.Report, RenderView{
		Route:        tg.Route,
		TargetDomain: tg.TargetStreamingDomain,
		ArtifactNote: "plan ready",
	})
	return res, nil
}

func newResult(plan *reconcile.Plan) *Result {
	r := &Result{Refused: plan.Report.Refused(), GatewayYAML: plan.GatewayYAML, Report: plan.Report}
	if plan.Artifacts != nil {
		r.Topics = plan.Artifacts.Topics
		r.FenceYAML = string(plan.Artifacts.FenceRules)
		r.SwitchoverYAML = string(plan.Artifacts.SwitchoverRules)
	}
	if r.Refused {
		for _, p := range plan.Report.Preconditions {
			if !p.OK {
				r.Reasons = append(r.Reasons, p.Name+": "+p.Detail)
			}
		}
		for _, tv := range plan.Report.FailFast {
			r.Reasons = append(r.Reasons, tv.Topic+": "+tv.Reason)
		}
	}
	return r
}

// buildGatewaySource wires the live Gateway CR pull from spec.gateway: the CR is
// read from Kubernetes by namespace + cr-name via the existing gateway service,
// using the manifest's kubeconfig (a leading ~/ is expanded).
func buildGatewaySource(g *manifest.GatewayMigration, route string) (GatewayConfigSource, error) {
	kubeconfig, err := g.KubeconfigPath()
	if err != nil {
		return nil, err
	}
	svc := gateway.NewK8sService(kubeconfig)
	return NewGatewayLive(svc, g.Spec.Gateway.Namespace, g.Spec.Gateway.CrName, route), nil
}

// buildReconcileInput maps the manifest's spec.topicGroup onto the engine-owned
// ReconcileInput. The reconcile engine handles one route per run, so exactly one
// topicGroup entry is required.
func buildReconcileInput(g *manifest.GatewayMigration) (reconcile.ReconcileInput, error) {
	tgs := g.Spec.TopicGroup
	if len(tgs) != 1 {
		return reconcile.ReconcileInput{}, fmt.Errorf("spec.topicGroup: exactly one entry is required, got %d", len(tgs))
	}
	tg := tgs[0]

	var topics, patterns []string
	if tg.Topics != nil {
		topics = *tg.Topics
	}
	if tg.TopicPatterns != nil {
		patterns = *tg.TopicPatterns
	}
	if len(topics) == 0 && len(patterns) == 0 {
		return reconcile.ReconcileInput{}, fmt.Errorf("spec.topicGroup[0]: at least one of topics / topicPatterns is required")
	}
	if tg.Route == "" {
		return reconcile.ReconcileInput{}, fmt.Errorf("spec.topicGroup[0].route: required")
	}
	if tg.TargetStreamingDomain == "" {
		return reconcile.ReconcileInput{}, fmt.Errorf("spec.topicGroup[0].targetStreamingDomain: required")
	}
	return reconcile.ReconcileInput{
		Topics:          topics,
		TopicPatterns:   patterns,
		Route:           tg.Route,
		TargetDomain:    tg.TargetStreamingDomain,
		TargetClusterID: g.Spec.Target.ClusterID,
	}, nil
}

// buildLinkStatusProvider mirrors lagcheck.buildLagCheckConfig: the destination
// REST leg (whichever auth form the manifest resolves) drives the cluster-link
// service. Topics is empty ⇒ the provider reports every mirror on the link.
func buildLinkStatusProvider(g *manifest.GatewayMigration) (LinkStatusProvider, error) {
	if g.Spec.Target.Kafka == nil {
		return nil, fmt.Errorf("spec.target.kafka: required")
	}
	restCreds, err := g.RestCredentials()
	if err != nil {
		return nil, fmt.Errorf("resolving destination REST credentials: %w", err)
	}
	httpClient, err := restCreds.HTTPClient()
	if err != nil {
		return nil, fmt.Errorf("building destination REST client: %w", err)
	}
	svc := clusterlink.NewConfluentCloudService(httpClient)
	cfg := clusterlink.Config{
		RestEndpoint: g.Spec.Target.Kafka.RestEndpoint,
		ClusterID:    g.Spec.Target.ClusterID,
		LinkName:     g.Spec.ClusterLink.Name,
		Auth:         restCreds.Authenticator(),
		Topics:       []string{}, // empty ⇒ all mirrors
	}
	return NewClusterLinkStatus(svc, cfg), nil
}

// buildSourceTopicLister builds the source-cluster topic lister from the manifest
// source credentials, following the same auth resolution as `kcp migration
// execute`. The returned io.Closer is the underlying Kafka admin; the caller owns
// closing it.
func buildSourceTopicLister(g *manifest.GatewayMigration) (TopicLister, io.Closer, error) {
	creds, errs := g.SourceCredentials()
	if len(errs) > 0 {
		return nil, nil, manifest.JoinProblems("spec.source.credentials", errs)
	}
	conn := types.MigrateConn(g.Spec.Source.BootstrapServers, creds)
	return buildTopicLister(conn)
}

// buildTargetTopicLister builds the destination-cluster topic lister from the
// destination KAFKA leg (not the REST leg — they may differ). The returned
// io.Closer is the underlying Kafka admin; the caller owns closing it.
func buildTargetTopicLister(g *manifest.GatewayMigration) (TopicLister, io.Closer, error) {
	if g.Spec.Target.Kafka == nil {
		return nil, nil, fmt.Errorf("spec.target.kafka: required")
	}
	creds, errs := g.DestinationKafkaCredentials()
	if len(errs) > 0 {
		return nil, nil, manifest.JoinProblems("spec.target.kafka.clusterCredentials", errs)
	}
	conn := types.MigrateConn(g.Spec.Target.Kafka.BootstrapServers, creds)

	// Backward-compat parity with migration execute: a Confluent Cloud destination
	// that supplies neither ca_cert nor an explicit tls signal must still dial
	// SASL_SSL, not cleartext SASL_PLAINTEXT — the destination is a managed
	// cluster, always TLS. (A source may legitimately be plaintext.)
	if sp := conn.AuthMethod.SASLPlain; sp != nil && sp.CACert == "" && !sp.UseTLS {
		sp.UseTLS = true
	}
	return buildTopicLister(conn)
}

// buildTopicLister opens a Kafka admin for conn and wraps it as a TopicLister.
// Auth is dispatched through the shared client.AdminOptionForAuthMethod mapper;
// the encryption-in-transit arg is inert (the auth option determines TLS). The
// returned io.Closer is the admin itself; the caller owns closing it.
func buildTopicLister(conn types.KafkaSourceConn) (TopicLister, io.Closer, error) {
	authType, err := conn.GetSelectedAuthType()
	if err != nil {
		return nil, nil, fmt.Errorf("determining auth type: %w", err)
	}
	region := ""
	if authType == types.AuthTypeIAM && conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	authOpt, err := client.AdminOptionForAuthMethod(authType, conn.AuthMethod, conn.InsecureSkipTLSVerify)
	if err != nil {
		return nil, nil, fmt.Errorf("resolving auth option: %w", err)
	}
	admin, err := client.NewKafkaAdmin(conn.BootstrapServers, kafkatypes.ClientBrokerTls, region, defaultKafkaVersion, authOpt)
	if err != nil {
		return nil, nil, fmt.Errorf("connecting to cluster: %w", err)
	}
	return NewKafkaTopicLister(admin), admin, nil
}
