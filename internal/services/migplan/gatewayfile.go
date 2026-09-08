// Package providers holds the live implementations of the migplan provider
// interfaces, wrapping existing KCP clients and, for the prototype, static files.
package migplan

import (
	"context"
	"fmt"
	"os"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/goccy/go-yaml"
)

var _ GatewayConfigSource = (*GatewayFile)(nil)

// GatewayFile loads a route from a static gateway CR YAML file — the prototype
// stand-in for the live k8s pull.
type GatewayFile struct {
	path      string
	routeName string
}

func NewGatewayFile(path, routeName string) *GatewayFile {
	return &GatewayFile{path: path, routeName: routeName}
}

func (g *GatewayFile) Load(_ context.Context) (*reconcile.GatewayConfig, error) {
	raw, err := os.ReadFile(g.path)
	if err != nil {
		return nil, fmt.Errorf("reading gateway config %q: %w", g.path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("parsing gateway config %q: %w", g.path, err)
	}
	route, err := findRoute(doc, g.routeName)
	if err != nil {
		return nil, err
	}
	return &reconcile.GatewayConfig{Route: route, RawYAML: string(raw)}, nil
}

// findRoute extracts the named route from spec.routes[] into a RouteConfig:
// Mode from the route's `mode` verbatim (a missing `mode` defaults to "static",
// never inferred from other fields — the "route is dynamic" precondition then
// refuses safely rather than a static route slipping through as dynamic),
// BoundDomains from streamingDomains[].name, Rules = the route's `rules` subtree
// (passed through untouched for the core to parse).
func findRoute(doc map[string]any, name string) (*reconcile.RouteConfig, error) {
	spec, _ := doc["spec"].(map[string]any)
	if spec == nil {
		return nil, fmt.Errorf("gateway config has no spec")
	}
	routes, _ := spec["routes"].([]any)
	for _, r := range routes {
		route, ok := r.(map[string]any)
		if !ok {
			continue
		}
		if rn, _ := route["name"].(string); rn != name {
			continue
		}
		rc := &reconcile.RouteConfig{Name: name}

		sds, _ := route["streamingDomains"].([]any)
		for _, sd := range sds {
			if m, ok := sd.(map[string]any); ok {
				if dn, ok := m["name"].(string); ok {
					rc.BoundDomains = append(rc.BoundDomains, dn)
				}
			}
		}

		if mode, ok := route["mode"].(string); ok && mode != "" {
			rc.Mode = mode
		} else {
			rc.Mode = "static"
		}

		if rules, ok := route["rules"].(map[string]any); ok {
			rc.Rules = rules
		} else {
			rc.Rules = map[string]any{}
		}
		return rc, nil
	}
	return nil, fmt.Errorf("route %q not found in gateway config", name)
}
