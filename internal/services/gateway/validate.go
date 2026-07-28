package gateway

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"strings"

	"github.com/goccy/go-yaml"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/tools/clientcmd"
)

// The three roles a gateway CR plays in a migration. Used to name the offending
// file in findings, so an operator knows which flag to go and fix.
const (
	roleInitial    = "initial"
	roleFenced     = "fenced"
	roleSwitchover = "switchover"
)

// CRValidationResult records what ValidateGatewayCRs actually verified, so the
// caller can report the outcome truthfully instead of printing a tick that only
// means "the function returned".
type CRValidationResult struct {
	// SecretRefsChecked is how many distinct secret references were confirmed
	// present in the namespace. Zero with an empty SecretCheckSkipped means the
	// CRs reference no secrets.
	SecretRefsChecked int
	// SecretCheckSkipped is non-empty when the live secret lookup could not run,
	// and carries the reason. The static checks still ran.
	SecretCheckSkipped string
	// Warnings are non-fatal findings the operator should see. They do not block
	// the migration.
	Warnings []string
}

// findings accumulates validation output. A problem blocks the migration; a
// warning is surfaced to the operator but lets it proceed. Checks append rather
// than return early so one run reports every fixable issue instead of making the
// operator re-run to discover the next one.
type findings struct {
	problems []string
	warnings []string
}

func (f *findings) problem(format string, a ...any) {
	f.problems = append(f.problems, fmt.Sprintf(format, a...))
}

func (f *findings) warn(format string, a ...any) {
	f.warnings = append(f.warnings, fmt.Sprintf(format, a...))
}

// gatewayCR is a parsed gateway CR tagged with the role it plays.
type gatewayCR struct {
	role string
	obj  map[string]any
}

// ValidateGatewayCRs checks the initial, fenced and switchover gateway CRs
// before a migration starts. It is a pre-flight gate: everything it catches
// would otherwise surface mid-migration, after client traffic has been fenced,
// when the only way out is a rollback.
//
// Most checks are static, but the one that matters most is not — confirming the
// secrets the CRs reference actually exist needs a live namespace lookup. That
// is why this takes a ctx and a namespace: on 2026-07-27 a switchover CR named
// a Secret that did not exist, the operator refused the spec, and the gateway
// stayed fenced with every client blocked. Nothing ahead of the apply looked at
// that secret at all; this does, before any client traffic has been touched.
func (s *K8sService) ValidateGatewayCRs(ctx context.Context, namespace, gatewayName string, initialYAML, fencedYAML, switchoverYAML []byte) (CRValidationResult, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return CRValidationResult{}, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return CRValidationResult{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	return validateGatewayCRs(ctx, clientset, namespace, gatewayName, initialYAML, fencedYAML, switchoverYAML)
}

// validateGatewayCRs is the inner orchestration used by ValidateGatewayCRs.
// Split from the method so unit tests can inject a fake clientset, matching
// waitForGatewayPods and waitForGatewayReady.
func validateGatewayCRs(ctx context.Context, clientset kubernetes.Interface, namespace, gatewayName string, initialYAML, fencedYAML, switchoverYAML []byte) (CRValidationResult, error) {
	var result CRValidationResult

	// Parsing is terminal rather than one problem among many: every later check
	// reads the parsed tree, so there is nothing useful left to report.
	initial, err := parseGatewayCR(roleInitial, initialYAML)
	if err != nil {
		return result, err
	}
	fenced, err := parseGatewayCR(roleFenced, fencedYAML)
	if err != nil {
		return result, err
	}
	switchover, err := parseGatewayCR(roleSwitchover, switchoverYAML)
	if err != nil {
		return result, err
	}

	var f findings

	for _, cr := range []gatewayCR{initial, fenced, switchover} {
		checkGatewayShape(cr, &f)
	}

	// The initial CR is fetched from the cluster by name, so its identity and
	// wiring are correct by construction and the operator has already accepted
	// its spec — checking it again would only add noise.
	for _, cr := range []gatewayCR{fenced, switchover} {
		checkGatewayIdentity(cr, namespace, gatewayName, &f)
		checkStreamingDomainWiring(cr, &f)
	}

	checkFenceBlocks(fenced, switchover, &f)
	checkSpecsDiffer(initial, fenced, switchover, &f)
	checkSecretRefsExist(ctx, clientset, namespace, []gatewayCR{fenced, switchover}, &result, &f)

	// Findings reach the terminal through the caller's reporter (which mirrors
	// them into kcp.log), and the returned error is logged by main. Logging them
	// again here would double every line on the console, so these stay at Debug.
	result.Warnings = f.warnings

	if len(f.problems) > 0 {
		slog.Debug("gateway CR validation found problems",
			"namespace", namespace, "gateway", gatewayName,
			"problems", f.problems, "warnings", f.warnings)
		return result, fmt.Errorf("%d problem(s) found:\n  - %s", len(f.problems), strings.Join(f.problems, "\n  - "))
	}

	slog.Debug("gateway CRs validated",
		"namespace", namespace, "gateway", gatewayName,
		"secretRefsChecked", result.SecretRefsChecked,
		"secretCheckSkipped", result.SecretCheckSkipped,
		"warnings", f.warnings)
	return result, nil
}

// parseGatewayCR unmarshals one CR, keeping the role for error messages.
func parseGatewayCR(role string, data []byte) (gatewayCR, error) {
	if len(bytes.TrimSpace(data)) == 0 {
		return gatewayCR{}, fmt.Errorf("the %s gateway CR is empty", role)
	}

	var obj map[string]any
	if err := yaml.Unmarshal(data, &obj); err != nil {
		return gatewayCR{}, fmt.Errorf("failed to parse the %s gateway CR: %w", role, err)
	}
	if len(obj) == 0 {
		return gatewayCR{}, fmt.Errorf("the %s gateway CR contains no fields", role)
	}

	return gatewayCR{role: role, obj: obj}, nil
}

// checkGatewayShape confirms the file is a Confluent Gateway CR with a spec.
// ApplyGatewayYAML pushes whatever it is handed through the pinned Gateway GVR
// and overwrites name and namespace, so a Deployment or a Kafka CR passed to
// --fenced-cr-yaml would be applied *as* this gateway.
func checkGatewayShape(cr gatewayCR, f *findings) {
	if kind, _ := stringField(cr.obj, "kind"); kind != GatewayKind {
		f.problem("the %s gateway CR has kind %q, expected %q", cr.role, kind, GatewayKind)
	}

	apiVersion, _ := stringField(cr.obj, "apiVersion")
	group, version := splitAPIVersion(apiVersion)
	switch {
	case group != GatewayGroup:
		f.problem("the %s gateway CR has apiVersion %q, expected group %q", cr.role, apiVersion, GatewayGroup)
	case version != GatewayVersion:
		// kcp applies through the pinned v1beta1 GVR whatever the file declares,
		// so the declared version is quietly ignored — worth surfacing, but not
		// worth refusing a migration over.
		f.warn("the %s gateway CR declares apiVersion %q but kcp applies it as %s/%s", cr.role, apiVersion, GatewayGroup, GatewayVersion)
	}

	if spec, ok := mapField(cr.obj, "spec"); !ok || len(spec) == 0 {
		f.problem("the %s gateway CR has no spec", cr.role)
	}
}

// checkGatewayIdentity confirms a CR names the gateway the migration targets.
// ApplyGatewayYAML calls SetName/SetNamespace with the migration's target, so a
// file for a different gateway is not rejected — it is silently applied over
// this one.
func checkGatewayIdentity(cr gatewayCR, namespace, gatewayName string, f *findings) {
	metadata, _ := mapField(cr.obj, "metadata")

	if name, _ := stringField(metadata, "name"); name != gatewayName {
		f.problem("the %s gateway CR is named %q but the migration targets gateway %q (kcp would apply this file under the target name)", cr.role, name, gatewayName)
	}

	if ns, ok := stringField(metadata, "namespace"); ok && ns != namespace {
		f.problem("the %s gateway CR declares namespace %q but the migration targets namespace %q", cr.role, ns, namespace)
	}
}

// checkFenceBlocks confirms the fenced CR actually fences, and that the
// switchover CR lifts the fence it applied.
//
// Both failures are silent in the same way: the migration runs to completion and
// reports success. Without a fence, topics are promoted while producers are
// still writing to the source. With a fence left in the switchover CR, every
// client stays blocked after kcp declares the migration complete.
//
// Fences are tracked per route because a gateway legitimately fences one route
// while leaving another serving — the SCRAM pre-registration route in
// docs/assets/gateway-switchover stays available throughout.
func checkFenceBlocks(fenced, switchover gatewayCR, f *findings) {
	fencedRoutes, routesPresent := fencedRouteNames(fenced)
	switch {
	case len(fencedRoutes) > 0:
		// The fenced CR blocks at least one named route — nothing to report.
	case treeContainsKey(fenced.obj, "fence"):
		// A fence exists somewhere kcp cannot attribute to a route. Don't refuse:
		// the CRD may grow other placements. Say only what is true — that the
		// fence could not be confirmed.
		f.warn("the fenced gateway CR has a fence block that is not on a route, so kcp cannot confirm which routes it blocks")
	case !routesPresent:
		f.problem("the fenced gateway CR declares no spec.routes, so it cannot fence client traffic")
	default:
		f.problem("the fenced gateway CR contains no fence block, so applying it would not block client traffic — the migration would promote topics while producers are still writing to the source")
	}

	fencedSet := make(map[string]struct{}, len(fencedRoutes))
	for _, route := range fencedRoutes {
		fencedSet[route] = struct{}{}
	}

	switchoverRoutes, _ := fencedRouteNames(switchover)
	var stillFenced, otherFenced []string
	for _, route := range switchoverRoutes {
		if _, migrationRoute := fencedSet[route]; migrationRoute {
			stillFenced = append(stillFenced, route)
		} else {
			otherFenced = append(otherFenced, route)
		}
	}

	if len(stillFenced) > 0 {
		f.problem("the switchover gateway CR still fences the route(s) the fenced CR blocks (%s), so clients would stay blocked after switchover", strings.Join(stillFenced, ", "))
	}
	// kcp cannot know the intent behind fencing a route the migration never
	// fenced — it may be deliberate — so this warns rather than refuses.
	if len(otherFenced) > 0 {
		f.warn("the switchover gateway CR fences route(s) the migration does not fence (%s); clients on those routes will be blocked after switchover", strings.Join(otherFenced, ", "))
	}
}

// checkSpecsDiffer catches the wrong-file mistakes that yield a "successful"
// migration with nothing switched over: the same file passed to both flags, or
// the live gateway's own spec handed back as the switchover. Either way the
// apply is a no-op, which does not bump metadata.generation, so even
// WaitForGatewayAccepted returns immediately and the step reports success.
//
// Only exact equality is reported. The live initial CR carries operator defaults
// the user's file omits, so a *difference* from it proves nothing — this is a
// cheap identity test, not a diff.
func checkSpecsDiffer(initial, fenced, switchover gatewayCR, f *findings) {
	initialSpec, _ := mapField(initial.obj, "spec")
	fencedSpec, _ := mapField(fenced.obj, "spec")
	switchoverSpec, _ := mapField(switchover.obj, "spec")

	// A missing spec is already reported by checkGatewayShape; comparing absent
	// specs here would only produce a second, misleading finding.
	if len(switchoverSpec) == 0 {
		return
	}

	if len(fencedSpec) > 0 && reflect.DeepEqual(fencedSpec, switchoverSpec) {
		f.problem("the fenced and switchover gateway CRs have identical specs, so the switchover would change nothing (are --fenced-cr-yaml and --switchover-cr-yaml the same file?)")
	}
	if len(initialSpec) > 0 && reflect.DeepEqual(initialSpec, switchoverSpec) {
		f.problem("the switchover gateway CR spec is identical to the live gateway's, so the switchover would not route traffic to Confluent Cloud")
	}
}

// checkStreamingDomainWiring confirms each route points at a streaming domain
// the same CR declares, and at a bootstrap server id that domain defines. The
// operator rejects a dangling reference — but not until the CR is applied, i.e.
// mid-migration.
//
// Only what the file states is checked: a route with no streamingDomain, or a
// domain reference with no bootstrapServerId, is left alone rather than assumed
// mandatory.
func checkStreamingDomainWiring(cr gatewayCR, f *findings) {
	spec, _ := mapField(cr.obj, "spec")

	// domain name -> the bootstrap server ids it defines
	domains := map[string]map[string]struct{}{}
	rawDomains, _ := sliceField(spec, "streamingDomains")
	for _, raw := range rawDomains {
		domain, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		name, ok := stringField(domain, "name")
		if !ok {
			continue
		}
		ids := map[string]struct{}{}
		cluster, _ := mapField(domain, "kafkaCluster")
		servers, _ := sliceField(cluster, "bootstrapServers")
		for _, rawServer := range servers {
			server, ok := rawServer.(map[string]any)
			if !ok {
				continue
			}
			if id, ok := stringField(server, "id"); ok {
				ids[id] = struct{}{}
			}
		}
		domains[name] = ids
	}

	routes, _ := sliceField(spec, "routes")
	for i, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		ref, ok := mapField(route, "streamingDomain")
		if !ok {
			continue
		}
		domainName, ok := stringField(ref, "name")
		if !ok || domainName == "" {
			continue
		}

		ids, declared := domains[domainName]
		if !declared {
			f.problem("in the %s gateway CR, route %s points at streaming domain %q, which the CR does not declare (declared: %s)",
				cr.role, routeLabel(route, i), domainName, describeSet(domains))
			continue
		}

		id, ok := stringField(ref, "bootstrapServerId")
		if !ok || id == "" {
			continue
		}
		if _, defined := ids[id]; !defined {
			f.problem("in the %s gateway CR, route %s points at bootstrapServerId %q, which streaming domain %q does not define (defined: %s)",
				cr.role, routeLabel(route, i), id, domainName, describeSet(ids))
		}
	}
}

// secretRefKeys are the CRD fields that name a Kubernetes Secret.
//
// These are collected by walking the whole CR rather than by fixed paths: across
// the worked examples in docs/assets/gateway-switchover they appear at six
// different depths (secret stores, per-bootstrap-server TLS, route client and
// cluster authentication, SCRAM admin credentials, JAAS pass-through), and the
// CRD keeps growing new ones. A path-based collector would silently stop
// covering the field that matters.
var secretRefKeys = map[string]struct{}{
	"secretRef":       {},
	"configSecretRef": {},
}

// checkSecretRefsExist confirms every Secret the fenced and switchover CRs
// reference already exists in the namespace. This is the check that would have
// caught the 2026-07-27 failure.
//
// The live initial CR is excluded: it is already running, so its references
// resolve by construction.
//
// Reading Secrets is a privilege plenty of migration operators are not granted,
// and this validation is new — a denial must not block a migration that would
// otherwise succeed. So a denial downgrades to a recorded skip, and only a
// confirmed NotFound refuses.
func checkSecretRefsExist(ctx context.Context, clientset kubernetes.Interface, namespace string, crs []gatewayCR, result *CRValidationResult, f *findings) {
	refs := map[string]struct{}{}
	for _, cr := range crs {
		collectSecretRefs(cr.obj, refs)
	}
	if len(refs) == 0 {
		slog.Debug("no secret references found in gateway CRs", "namespace", namespace)
		return
	}

	names := slices.Sorted(maps.Keys(refs))
	slog.Debug("🔍 checking gateway CR secret references", "namespace", namespace, "count", len(names), "secrets", names)

	var missing []string
	for _, name := range names {
		_, err := clientset.CoreV1().Secrets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			result.SecretRefsChecked++
			slog.Debug("gateway CR secret reference resolved", "namespace", namespace, "secret", name)
			continue
		}
		// Every absent secret is collected rather than returned on: fixing them
		// one re-run at a time is the loop this check exists to avoid.
		if apierrors.IsNotFound(err) {
			missing = append(missing, name)
			continue
		}
		if apierrors.IsForbidden(err) || apierrors.IsUnauthorized(err) {
			// Report a partial count as no count: "3 of 7 verified" reads as a
			// pass. Any NotFound already seen is still reported below.
			result.SecretRefsChecked = 0
			result.SecretCheckSkipped = fmt.Sprintf("no permission to read secrets in namespace %s", namespace)
			// The skip itself is reported by the caller; this carries the raw API
			// error the reported line cannot.
			slog.Debug("⏭️ cannot verify gateway CR secret references", "namespace", namespace, "secret", name, "error", err)
			break
		}
		// Neither absent nor denied — the API is not answering, so stop instead
		// of reporting a partial sweep as a complete one.
		f.problem("failed to check whether secret %q exists in namespace %s: %v", name, namespace, err)
		break
	}

	if len(missing) > 0 {
		f.problem("the fenced/switchover gateway CRs reference %d secret(s) that do not exist in namespace %s: %s — the Confluent operator will refuse the spec and the gateway will stay fenced",
			len(missing), namespace, strings.Join(missing, ", "))
	}
}

// collectSecretRefs walks an arbitrary CR subtree collecting the names of the
// Secrets it references. A secretRef whose value is not a string is descended
// into rather than recorded, so a future object-valued form still gets walked.
func collectSecretRefs(v any, into map[string]struct{}) {
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			if _, isRef := secretRefKeys[key]; isRef {
				if name, ok := child.(string); ok && name != "" {
					into[name] = struct{}{}
					continue
				}
			}
			collectSecretRefs(child, into)
		}
	case []any:
		for _, child := range node {
			collectSecretRefs(child, into)
		}
	}
}

// fencedRouteNames returns the names of spec.routes entries carrying a fence
// block, and whether spec.routes was present at all — "no routes" and "routes
// but none fenced" are different failures.
func fencedRouteNames(cr gatewayCR) (fenced []string, routesPresent bool) {
	spec, _ := mapField(cr.obj, "spec")
	routes, ok := sliceField(spec, "routes")
	if !ok {
		return nil, false
	}

	for i, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if _, hasFence := route["fence"]; !hasFence {
			continue
		}
		fenced = append(fenced, routeLabel(route, i))
	}
	slices.Sort(fenced)
	return fenced, true
}

// treeContainsKey reports whether key appears anywhere in the subtree.
func treeContainsKey(v any, key string) bool {
	switch node := v.(type) {
	case map[string]any:
		if _, found := node[key]; found {
			return true
		}
		for _, child := range node {
			if treeContainsKey(child, key) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if treeContainsKey(child, key) {
				return true
			}
		}
	}
	return false
}

// routeLabel names a route for a finding, falling back to its index when the
// route has no name.
func routeLabel(route map[string]any, index int) string {
	if name, ok := stringField(route, "name"); ok && name != "" {
		return name
	}
	return fmt.Sprintf("routes[%d]", index)
}

// describeSet renders a set's keys for a finding, so an operator can see what
// the CR does declare next to what the route asked for.
func describeSet[V any](set map[string]V) string {
	names := slices.Sorted(maps.Keys(set))
	if len(names) == 0 {
		return "none"
	}
	return strings.Join(names, ", ")
}

// splitAPIVersion splits "group/version" into its parts. A bare version (no
// slash) belongs to the core group, which no Gateway CR does.
func splitAPIVersion(apiVersion string) (group, version string) {
	if group, version, found := strings.Cut(apiVersion, "/"); found {
		return group, version
	}
	return "", apiVersion
}

// The CR tree is untyped YAML, so these read one level with a type assertion
// instead of unstructured.Nested*: those deep-copy the subtree through
// runtime.DeepCopyJSONValue, which panics on the plain ints goccy/go-yaml
// produces for fields like nodeIdRanges.start.

func mapField(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key].(map[string]any)
	return v, ok
}

func sliceField(m map[string]any, key string) ([]any, bool) {
	v, ok := m[key].([]any)
	return v, ok
}

func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(string)
	return v, ok
}
