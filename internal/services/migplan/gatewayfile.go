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
	cleanGatewayDoc(doc)
	route, err := findRoute(doc, g.routeName)
	if err != nil {
		return nil, err
	}
	cleaned, err := yaml.Marshal(doc)
	if err != nil {
		return nil, fmt.Errorf("marshalling cleaned gateway config %q: %w", g.path, err)
	}
	return &reconcile.GatewayConfig{Route: route, RawYAML: string(cleaned), RawObj: doc}, nil
}

// cleanGatewayDoc strips the Kubernetes server-managed fields a live CR read
// carries — managedFields, resourceVersion, uid, creationTimestamp,
// generation, and top-level status — that a later re-apply rejects. Mutates
// doc in place. Centralizes what internal/services/migration's
// cleanInitialCR and (until this change) tbm's own cleanGatewayYAML each
// independently re-implemented from this same source — every migplan
// caller now gets an already-clean snapshot.
func cleanGatewayDoc(doc map[string]any) {
	if metadata, ok := doc["metadata"].(map[string]any); ok {
		delete(metadata, "managedFields")
		delete(metadata, "resourceVersion")
		delete(metadata, "uid")
		delete(metadata, "creationTimestamp")
		delete(metadata, "generation")
	}
	delete(doc, "status")
}

// resolveModeStructurally infers a route's mode when it carries no explicit
// mode field: a singular streamingDomain object binding (with a non-empty
// name — see the CRD-default note below) means static; a non-empty plural
// streamingDomains array means dynamic. Mirrors gateway.ResolveRouteMode's
// structural resolution exactly, including its strictness: a route declaring
// neither or both bindings is an error, not a silent default — the real
// Gateway CRD enforces mutual exclusivity via a CEL XOR, so this shape only
// occurs on a hand-edited or malformed CR, which should fail loudly here
// rather than resolve to "static" and fail more confusingly later.
func resolveModeStructurally(route map[string]any) (string, error) {
	// CFK's Gateway CRD schema defaults a zero-valued streamingDomain object
	// (name: "", bootstrapServerId: "") onto EVERY route, including
	// dynamic-mode ones that only ever set streamingDomains (plural) — a
	// present-but-unnamed singular binding is that default, not a
	// user-declared static binding, so it doesn't count toward hasSingular.
	singularDomain, _ := route["streamingDomain"].(map[string]any)
	singularName, _ := singularDomain["name"].(string)
	hasSingular := singularDomain != nil && singularName != ""
	sds, _ := route["streamingDomains"].([]any)
	hasPlural := len(sds) > 0
	switch {
	case hasSingular && hasPlural:
		return "", fmt.Errorf("route declares both a singular streamingDomain and a plural streamingDomains binding")
	case hasPlural:
		return "dynamic", nil
	case hasSingular:
		return "static", nil
	default:
		return "", fmt.Errorf("route declares neither a streamingDomain nor a streamingDomains binding")
	}
}

// findRoute extracts the named route from spec.routes[] into a RouteConfig:
// Mode from the route's `mode` field verbatim when present, else inferred
// structurally by resolveModeStructurally (field-first, structural-fallback —
// see its doc comment), BoundDomains from streamingDomains[].name, Rules = the
// route's `rules` subtree (passed through untouched for the core to parse),
// and Raw = the route's own raw map, for the static-route strategy's
// route-level reads (security.cluster, streamingDomain) that none of this
// struct's other fields carry.
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
			mode, err := resolveModeStructurally(route)
			if err != nil {
				return nil, fmt.Errorf("resolving mode for route %q: %w", name, err)
			}
			rc.Mode = mode
		}
		rc.Raw = route

		if rules, ok := route["rules"].(map[string]any); ok {
			rc.Rules = rules
		} else {
			rc.Rules = map[string]any{}
		}
		return rc, nil
	}
	return nil, fmt.Errorf("route %q not found in gateway config", name)
}
