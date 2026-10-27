package migplan

import (
	"context"
	"fmt"

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

// GatewayLive loads a route from the Gateway custom resource live in Kubernetes,
// replacing the static-file stand-in. It reads the CR through the existing
// gateway service and extracts the named route with the same findRoute the file
// source uses, so the two paths agree on route shape.
type GatewayLive struct {
	svc       gatewayCRReader
	namespace string
	crName    string
	routeName string
}

// NewGatewayLive builds a live gateway source. namespace and crName come from the
// manifest's spec.gateway; the service carries the kubeconfig.
func NewGatewayLive(svc gatewayCRReader, namespace, crName, routeName string) *GatewayLive {
	return &GatewayLive{svc: svc, namespace: namespace, crName: crName, routeName: routeName}
}

func (g *GatewayLive) Load(ctx context.Context) (*reconcile.GatewayConfig, error) {
	raw, err := g.svc.GetGatewayYAML(ctx, g.namespace, g.crName)
	if err != nil {
		return nil, fmt.Errorf("reading gateway CR %q in namespace %q: %w", g.crName, g.namespace, err)
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
	return &reconcile.GatewayConfig{Route: route, RawYAML: string(cleaned), RawObj: doc}, nil
}
