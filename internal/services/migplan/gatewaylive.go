package migplan

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/goccy/go-yaml"
)

var _ GatewayConfigSource = (*GatewayLive)(nil)

// gatewayCRReader is the one operation the live source needs from the gateway
// service: read a single Gateway CR as YAML. Narrowed to keep the seam small and
// mockable in unit tests. gateway.K8sService satisfies it.
type gatewayCRReader interface {
	GetGatewayYAML(ctx context.Context, namespace, gatewayName string) ([]byte, error)
}

// podSyncChecker is the pod check the live source runs on the CR it read.
// gateway.K8sService satisfies it.
type podSyncChecker interface {
	CheckPodSync(ctx context.Context, namespace, gatewayName string, crYAML []byte, port int) (gateway.PodSync, error)
}

// GatewayLive loads a route from the Gateway custom resource live in Kubernetes,
// replacing the static-file stand-in. It reads the CR through the existing
// gateway service and extracts the named route with the same findRoute the file
// source uses, so the two paths agree on route shape.
type GatewayLive struct {
	svc       gatewayCRReader
	namespace string
	crName    string
	routeName string
	podCheck  podSyncChecker
	podPort   int
}

// NewGatewayLive builds a live gateway source. namespace and crName come from the
// manifest's spec.gateway; the service carries the kubeconfig.
func NewGatewayLive(svc gatewayCRReader, namespace, crName, routeName string) *GatewayLive {
	return &GatewayLive{svc: svc, namespace: namespace, crName: crName, routeName: routeName}
}

// WithPodCheck makes Load also check whether the gateway pods serve the CR it
// read, reporting the verdict on GatewayConfig.Sync. port is the pods' /config
// port; 0 uses the gateway default. Without it Sync stays SyncUnchecked.
func (g *GatewayLive) WithPodCheck(c podSyncChecker, port int) *GatewayLive {
	g.podCheck, g.podPort = c, port
	return g
}

func (g *GatewayLive) Load(ctx context.Context) (*reconcile.GatewayConfig, error) {
	raw, err := g.svc.GetGatewayYAML(ctx, g.namespace, g.crName)
	if err != nil {
		return nil, fmt.Errorf("reading gateway CR %q in namespace %q: %w", g.crName, g.namespace, err)
	}
	// The pod check reads status, generation and managedFields, so it gets the
	// CR before cleanGatewayDoc strips them.
	var sync reconcile.GatewaySync
	if g.podCheck != nil {
		ps, err := g.podCheck.CheckPodSync(ctx, g.namespace, g.crName, raw, g.podPort)
		if err != nil {
			return nil, fmt.Errorf("checking gateway %q pods: %w", g.crName, err)
		}
		sync = toGatewaySync(ps)
		slog.Debug("gateway pod check", "gateway", g.crName, "verdict", ps.Verdict, "detail", ps.Detail)
		for _, e := range ps.Evidence {
			slog.Debug("gateway pod check evidence", "gateway", g.crName, "evidence", e)
		}
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing gateway CR %q: %w", g.crName, err)
	}
	cleanGatewayDoc(doc)
	route, err := findRoute(doc, g.routeName)
	if err != nil {
		return nil, err
	}
	cleaned, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling cleaned gateway CR %q: %w", g.crName, err)
	}
	return &reconcile.GatewayConfig{Route: route, RawYAML: string(cleaned), RawObj: doc, Sync: sync}, nil
}

// toGatewaySync maps the pod check's result onto the reconcile core's.
func toGatewaySync(ps gateway.PodSync) reconcile.GatewaySync {
	verdict := reconcile.SyncCannotTell
	switch ps.Verdict {
	case gateway.PodsInSync:
		verdict = reconcile.SyncInSync
	case gateway.PodsOutOfSync:
		verdict = reconcile.SyncOutOfSync
	}
	return reconcile.GatewaySync{Verdict: verdict, Detail: ps.Detail, Evidence: ps.Evidence}
}
