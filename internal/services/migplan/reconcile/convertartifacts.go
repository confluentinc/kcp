package reconcile

import "github.com/goccy/go-yaml"

// targetBinding returns the bootstrapServerId the route binds domain to in its
// own streamingDomains[] list. ok is false when the route doesn't bind domain
// or binds it with no id.
func targetBinding(route map[string]any, domain string) (string, bool) {
	sds, _ := sliceField(route, "streamingDomains")
	for _, raw := range sds {
		sd, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if stringField(sd, "name") == domain {
			id := stringField(sd, "bootstrapServerId")
			return id, id != ""
		}
	}
	return "", false
}

// BuildConvertedStaticRoute returns a converted route, wrapped as {route: …}:
// raw without its two-domain streamingDomains binding and its rules tree, bound
// to targetDomain alone through bootstrapServerID (the id the route already
// used for it — see targetBinding), with mode set to static when the route
// declares a mode at all. Every other key is kept. It is a whole route because
// a field patch can't remove keys. raw is not modified.
func BuildConvertedStaticRoute(raw map[string]any, targetDomain, bootstrapServerID string) ([]byte, error) {
	route := make(map[string]any, len(raw))
	for k, v := range raw {
		route[k] = v
	}
	delete(route, "streamingDomains")
	delete(route, "rules")
	route["streamingDomain"] = map[string]any{
		"name":              targetDomain,
		"bootstrapServerId": bootstrapServerID,
	}
	if _, ok := route["mode"]; ok {
		route["mode"] = "static"
	}
	return yaml.Marshal(map[string]any{"route": route})
}
