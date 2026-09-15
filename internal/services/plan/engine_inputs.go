package plan

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/goccy/go-yaml"
)

// appQuestionKeys is the set of plan-inputs keys that describe one application's
// migration (switchover, schema, connectors, topics, historical) and so may be
// overridden per app. Everything else is infrastructure (cluster type,
// networking, auth) — set once on the cluster, never per app.
var appQuestionKeys = map[string]bool{
	"move_existing_data":           true,
	"downtime_tolerance":           true,
	"client_coordination":          true,
	"eos_streams":                  true,
	"schema_registry":              true,
	"schema_registry_edition":      true,
	"schema_strategy":              true,
	"schema_reachable_to_cc":       true,
	"connector_destination":        true,
	"consumer_history_requirement": true,
}

// DeclaredCluster holds one cluster's declared answers: its own cluster-level
// inputs plus optional per-application overrides (keyed by app name). The cluster
// level doubles as the default for its apps.
type DeclaredCluster struct {
	Inputs IntakeInputs
	Apps   map[string]IntakeInputs // nil when no `applications:` block is declared
}

// DeclaredInputs is the parsed plan-inputs.yaml: fleet-wide `defaults:` plus
// per-cluster answers under a `clusters:` map (keyed by cluster display key —
// bare name, or name@region on a name collision). Answers layer
// app → cluster → defaults, each level optional.
type DeclaredInputs struct {
	Defaults IntakeInputs
	Clusters map[string]DeclaredCluster
}

// For returns the effective cluster-level inputs for a cluster key: defaults with
// the cluster's own answers layered on top.
func (d DeclaredInputs) For(key string) IntakeInputs {
	if d.Clusters == nil {
		return d.Defaults
	}
	return mergeInputs(d.Defaults, d.Clusters[key].Inputs)
}

// AppNames returns the declared application names for a cluster, in sorted order
// (empty when the cluster declares no apps).
func (d DeclaredInputs) AppNames(key string) []string {
	dc, ok := d.Clusters[key]
	if !ok || len(dc.Apps) == 0 {
		return nil
	}
	names := make([]string, 0, len(dc.Apps))
	for name := range dc.Apps {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ForApp returns the effective inputs for one application: defaults, then the
// cluster, then the app's own answers layered on top.
func (d DeclaredInputs) ForApp(key, app string) IntakeInputs {
	cluster := d.For(key)
	dc, ok := d.Clusters[key]
	if !ok {
		return cluster
	}
	return mergeInputs(cluster, dc.Apps[app])
}

// allQuestions is the declared catalog plus the scan-override catalog — every key
// a customer can set in plan-inputs.yaml.
func allQuestions() []question { return append(append([]question{}, catalog...), scanCatalog...) }

// LoadDeclaredInputs reads a per-cluster, token-based plan-inputs.yaml and
// resolves each cluster's answers to IntakeInputs (engine strings), validating each
// value against its option vocabulary. Unknown values are reported as warnings
// and skipped. An empty path returns no inputs.
func LoadDeclaredInputs(path string) (DeclaredInputs, []string, error) {
	if path == "" {
		return DeclaredInputs{}, nil, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return DeclaredInputs{}, nil, fmt.Errorf("read %s: %w", path, err)
	}
	d, warnings, err := ParseDeclaredInputs(data)
	if err != nil {
		return DeclaredInputs{}, nil, fmt.Errorf("parse %s: %w", path, err)
	}
	return d, warnings, nil
}

// ParseDeclaredInputs resolves a plan-inputs.yaml byte slice into DeclaredInputs
// (the same logic LoadDeclaredInputs runs on a file). Empty input returns no
// inputs. For library callers that hold the YAML in memory rather than on disk.
func ParseDeclaredInputs(data []byte) (DeclaredInputs, []string, error) {
	if len(data) == 0 {
		return DeclaredInputs{}, nil, nil
	}
	var doc struct {
		AllClusters map[string]any            `yaml:"all_clusters"`
		Defaults    map[string]any            `yaml:"defaults"` // accepted as an alias for all_clusters
		Clusters    map[string]map[string]any `yaml:"clusters"`
	}
	if err := yaml.Unmarshal(data, &doc); err != nil {
		return DeclaredInputs{}, nil, err
	}
	out := DeclaredInputs{Clusters: map[string]DeclaredCluster{}}
	var warnings []string
	fleetRaw := doc.AllClusters
	if fleetRaw == nil {
		fleetRaw = doc.Defaults
	}
	if fleetRaw != nil {
		in, ws := resolveDeclaredDefaults(fleetRaw)
		out.Defaults = in
		for _, w := range ws {
			warnings = append(warnings, "all_clusters: "+w)
		}
	}
	for name, raw := range doc.Clusters {
		// Peel off the optional `applications:` block before resolving the
		// cluster's own answers (it isn't a question key).
		appsRaw, appWarnings := extractApps(raw)
		delete(raw, "applications")
		for _, w := range appWarnings {
			warnings = append(warnings, name+": "+w)
		}

		in, ws := resolveDeclared(raw)
		dc := DeclaredCluster{Inputs: in}
		for _, w := range ws {
			warnings = append(warnings, name+": "+w)
		}
		if len(appsRaw) > 0 {
			dc.Apps = map[string]IntakeInputs{}
			for _, appName := range sortedKeys(appsRaw) {
				appIn, aws := resolveDeclaredApp(appsRaw[appName])
				dc.Apps[appName] = appIn
				for _, w := range aws {
					warnings = append(warnings, name+".applications."+appName+": "+w)
				}
			}
		}
		out.Clusters[name] = dc
	}
	return out, warnings, nil
}

// extractApps reads the `applications:` sub-map from a cluster's raw map,
// returning app-name → raw-answer-map plus warnings for malformed shapes (an
// `applications:` that isn't a map, or an app whose value isn't a map).
func extractApps(raw map[string]any) (map[string]map[string]any, []string) {
	appsAny, ok := raw["applications"]
	if !ok || appsAny == nil {
		return nil, nil
	}
	appsMap, ok := toStringMap(appsAny)
	if !ok {
		return nil, []string{"`applications:` must be a mapping of app name → answers"}
	}
	out := map[string]map[string]any{}
	var warnings []string
	for name, v := range appsMap {
		if m, ok := toStringMap(v); ok {
			out[name] = m
		} else if v == nil {
			out[name] = map[string]any{} // declared but empty → inherits cluster
		} else {
			warnings = append(warnings, fmt.Sprintf("application %q must be a mapping of key → value", name))
		}
	}
	return out, warnings
}

// toStringMap normalises a YAML-decoded map value into map[string]any.
func toStringMap(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

func sortedKeys(m map[string]map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// resolveDeclared resolves a cluster's answers onto IntakeInputs. It iterates the raw
// keys so an unknown/misspelled key is reported, not silently dropped.
func resolveDeclared(raw map[string]any) (IntakeInputs, []string) {
	return resolveKeys(raw, keyScopeCluster)
}

// resolveDeclaredDefaults resolves the fleet `defaults:` block. Scan-fact keys are
// per-cluster and don't belong at the fleet level — they're reported and skipped.
func resolveDeclaredDefaults(raw map[string]any) (IntakeInputs, []string) {
	return resolveKeys(raw, keyScopeDefaults)
}

// resolveDeclaredApp resolves one application's answers, accepting only app-scoped
// keys. An infrastructure key under an app is ignored with a warning (infra is set
// on the cluster); an unknown key is reported too.
func resolveDeclaredApp(raw map[string]any) (IntakeInputs, []string) {
	return resolveKeys(raw, keyScopeApp)
}

type keyScope int

const (
	keyScopeCluster keyScope = iota
	keyScopeDefaults
	keyScopeApp
)

// resolveKeys maps a raw token map onto IntakeInputs (engine strings), iterating the
// raw keys so every unknown key is reported. The scope restricts which keys are
// accepted: an app rejects infrastructure keys. Scanned facts may be set under
// defaults (a value there overrides the scan for every cluster) or per cluster.
func resolveKeys(raw map[string]any, scope keyScope) (IntakeInputs, []string) {
	byKey := map[string]question{}
	for _, q := range allQuestions() {
		byKey[q.Key] = q
	}
	var in IntakeInputs
	var warnings []string
	for _, key := range sortedRawKeys(raw) {
		q, known := byKey[key]
		switch {
		case !known:
			warnings = append(warnings, fmt.Sprintf("unknown key %q", key))
			continue
		case scope == keyScopeApp && !appQuestionKeys[key]:
			warnings = append(warnings, fmt.Sprintf("%q is an infrastructure setting; set it on the cluster, not per application", key))
			continue
		}
		if raw[key] == nil {
			continue
		}
		var engVals []string
		for _, tok := range toTokens(raw[key]) {
			if tok == "" {
				continue
			}
			eng, valid := q.engineFor(tok)
			if !valid {
				warnings = append(warnings, fmt.Sprintf("%s: unknown value %q. Valid values: %s", key, tok, strings.Join(q.tokens(), ", ")))
				continue
			}
			engVals = append(engVals, eng)
		}
		if len(engVals) > 0 && q.set != nil {
			q.set(&in, engVals)
		}
	}
	return in, warnings
}

func sortedRawKeys(m map[string]any) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// toTokens normalises a YAML scalar/list value into token strings. Booleans map
// to "true"/"false" tokens.
func toTokens(v any) []string {
	switch t := v.(type) {
	case bool:
		if t {
			return []string{"true"}
		}
		return []string{"false"}
	case string:
		if t == "" {
			return nil
		}
		return []string{t}
	case []any:
		var out []string
		for _, e := range t {
			switch s := e.(type) {
			case string:
				if s != "" {
					out = append(out, s)
				}
			case bool:
				if s {
					out = append(out, "true")
				} else {
					out = append(out, "false")
				}
			}
		}
		return out
	case []string:
		return t
	}
	return nil
}

// engineFor maps one token to its engine string. valid is false for an unknown token.
func (q question) engineFor(token string) (string, bool) {
	for _, o := range q.Opts {
		if o.Token == token {
			return o.Engine, true
		}
	}
	return "", false
}

// tokens lists a question's valid tokens (for error messages).
func (q question) tokens() []string {
	out := make([]string, len(q.Opts))
	for i, o := range q.Opts {
		out[i] = o.Token
	}
	return out
}
