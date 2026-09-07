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

// CheckRedundantAuthStaged proves, for each fence route's switchover target,
// that flipping routes[].streamingDomain at cutover is safe. It is a
// pre-flight gate: everything it catches would otherwise surface mid-migration,
// after client traffic has been fenced, when the only way out is a rollback.
//
// Most checks are static, but the one that matters most is not — confirming the
// secrets the target's staged auth references actually exist needs a live
// namespace lookup. That is why this takes a ctx and a namespace: on
// 2026-07-27 a hand-authored switchover CR named a Secret that did not exist,
// the operator refused the spec, and the gateway stayed fenced with every
// client blocked. The redundant-auth design removes the hand-authored file,
// but the same failure mode is possible here too — the target domain's block
// is pre-staged and never yet exercised, so a typo'd secret name would
// otherwise surface only at cutover.
func (s *K8sService) CheckRedundantAuthStaged(ctx context.Context, namespace string, initialYAML []byte, targets []RouteSwitchoverTarget) (CRValidationResult, error) {
	config, err := clientcmd.BuildConfigFromFlags("", s.kubeConfigPath)
	if err != nil {
		return CRValidationResult{}, fmt.Errorf("failed to build config: %w", err)
	}

	clientset, err := kubernetes.NewForConfig(config)
	if err != nil {
		return CRValidationResult{}, fmt.Errorf("failed to create clientset: %w", err)
	}

	return checkRedundantAuthStaged(ctx, clientset, namespace, initialYAML, targets)
}

// checkRedundantAuthStaged is the inner orchestration used by
// CheckRedundantAuthStaged. Split from the method so unit tests can inject a
// fake clientset, matching waitForGatewayPods and waitForGatewayReady.
//
// For each target this confirms: the target streaming domain is declared with
// the target bootstrap server id; the route already carries a pre-staged
// security.cluster block for that domain; the route is not already bound
// there (the flip must be a real change); and — across every target together
// — every secret any part of the initial CR references exists. The whole CR
// is in scope for the secret check, not just the target blocks: unlike a
// route's CURRENTLY bound domain (already running, so its refs resolve by
// construction), the redundant target-domain block has never been exercised,
// so a typo'd secret name there would otherwise go unnoticed until cutover.
func checkRedundantAuthStaged(ctx context.Context, clientset kubernetes.Interface, namespace string, initialYAML []byte, targets []RouteSwitchoverTarget) (CRValidationResult, error) {
	var result CRValidationResult

	// Parsing is terminal rather than one problem among many: every later check
	// reads the parsed tree, so there is nothing useful left to report.
	initial, err := parseGatewayCR(roleInitial, initialYAML)
	if err != nil {
		return result, err
	}

	var f findings
	routes := routesByName(initial)
	domains := domainBootstrapIDs(initial)

	for _, target := range targets {
		route, ok := routes[target.RouteName]
		if !ok {
			f.problem("route %q not found in the initial gateway CR's spec.routes", target.RouteName)
			continue
		}

		if ids, declared := domains[target.StreamingDomainName]; !declared {
			f.problem("route %q switches to streaming domain %q, which the initial gateway CR does not declare (declared: %s)",
				target.RouteName, target.StreamingDomainName, describeSet(domains))
		} else if _, defined := ids[target.BootstrapServerId]; !defined {
			f.problem("route %q switches to bootstrapServerId %q, which streaming domain %q does not define (defined: %s)",
				target.RouteName, target.BootstrapServerId, target.StreamingDomainName, describeSet(ids))
		}

		staged, hasStaged := stagedAuthFor(route, target.StreamingDomainName)
		if !hasStaged {
			f.problem("route %q has no pre-staged security.cluster.%s block for its switchover target — the redundant auth this switch depends on is not configured",
				target.RouteName, target.StreamingDomainName)
		} else {
			if secretStore, _ := stringField(staged, "secretStore"); secretStore == "" {
				f.problem("route %q's security.cluster.%s has no secretStore", target.RouteName, target.StreamingDomainName)
			}
			if auth, ok := mapField(staged, "authentication"); !ok || len(auth) == 0 {
				f.problem("route %q's security.cluster.%s has no authentication block", target.RouteName, target.StreamingDomainName)
			}
		}

		if current, _ := mapField(route, "streamingDomain"); current != nil {
			if currentName, _ := stringField(current, "name"); currentName == target.StreamingDomainName {
				f.problem("route %q is already bound to streaming domain %q — the switch would be a no-op",
					target.RouteName, target.StreamingDomainName)
			}
		}
	}

	checkSecretRefsExist(ctx, clientset, namespace, []gatewayCR{initial}, &result, &f)

	// Findings reach the terminal through the caller's reporter (which mirrors
	// them into kcp.log), and the returned error is logged by main. Logging them
	// again here would double every line on the console, so these stay at Debug.
	result.Warnings = f.warnings

	if len(f.problems) > 0 {
		slog.Debug("redundant-auth staging check found problems",
			"namespace", namespace, "problems", f.problems, "warnings", f.warnings)
		return result, fmt.Errorf("%d problem(s) found:\n  - %s", len(f.problems), strings.Join(f.problems, "\n  - "))
	}

	slog.Debug("redundant auth staged",
		"namespace", namespace,
		"secretRefsChecked", result.SecretRefsChecked,
		"secretCheckSkipped", result.SecretCheckSkipped,
		"warnings", f.warnings)
	return result, nil
}

// routesByName indexes a CR's spec.routes by name for direct target lookup.
func routesByName(cr gatewayCR) map[string]map[string]any {
	byName := map[string]map[string]any{}
	spec, _ := mapField(cr.obj, "spec")
	routes, _ := sliceField(spec, "routes")
	for _, raw := range routes {
		route, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		if name, ok := stringField(route, "name"); ok && name != "" {
			byName[name] = route
		}
	}
	return byName
}

// domainBootstrapIDs maps each streaming domain a CR declares to the
// bootstrap server ids it defines.
func domainBootstrapIDs(cr gatewayCR) map[string]map[string]struct{} {
	domains := map[string]map[string]struct{}{}
	spec, _ := mapField(cr.obj, "spec")
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
	return domains
}

// stagedAuthFor returns route's security.cluster.<domainName> block — the
// pre-staged ("redundant") auth for that domain — or false if it is absent.
func stagedAuthFor(route map[string]any, domainName string) (map[string]any, bool) {
	security, _ := mapField(route, "security")
	cluster, _ := mapField(security, "cluster")
	return mapField(cluster, domainName)
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

// secretRefKeys are the CRD fields that name a Kubernetes Secret.
//
// These are collected by walking the whole CR rather than by fixed paths: across
// the worked examples in docs/assets/gateway-switchover they appear at six
// different depths (secret stores, per-bootstrap-server TLS, route client and
// cluster authentication, SCRAM admin credentials, JAAS pass-through), and the
// CRD keeps growing new ones. A path-based collector would silently stop
// covering the field that matters.
var secretRefKeys = map[string]struct{}{
	"secretRef":            {},
	"configSecretRef":      {},
	"clientCredentialsRef": {},
}

// unrecognisedRefKeySuffix is the naming convention every Secret-naming field in
// the Gateway CRD follows. A string field matching it that is not in
// secretRefKeys is a field this validator does not know about: report it rather
// than let the gap sit behind a tick claiming the secrets were checked.
//
// clientCredentialsRef was found this way — it names a Secret in four of the
// shipped switchover examples and was silently unchecked.
const unrecognisedRefKeySuffix = "Ref"

// checkSecretRefsExist confirms every Secret the given CRs reference already
// exists in the namespace. This is the check that would have caught the
// 2026-07-27 failure.
//
// checkRedundantAuthStaged passes the live initial CR here deliberately
// INCLUDING it, rather than excluding it: a route's currently-bound domain is
// already running, so its refs resolve by construction, but the pre-staged
// ("redundant") auth block for the switchover target has never been
// exercised by the cluster — a typo'd secret name there would otherwise go
// unnoticed until cutover.
//
// Reading Secrets is a privilege plenty of migration operators are not granted,
// and this validation is new — a denial must not block a migration that would
// otherwise succeed. So a denial downgrades to a recorded skip, and only a
// confirmed NotFound refuses.
func checkSecretRefsExist(ctx context.Context, clientset kubernetes.Interface, namespace string, crs []gatewayCR, result *CRValidationResult, f *findings) {
	refs := map[string]struct{}{}
	unrecognised := map[string]struct{}{}
	for _, cr := range crs {
		collectSecretRefs(cr.obj, refs, unrecognised)
	}

	// A Ref-suffixed field this validator does not know is a hole in the check,
	// not a detail: say so rather than let the reported count imply completeness.
	if len(unrecognised) > 0 {
		f.warn("the fenced/switchover gateway CRs use reference field(s) kcp does not know how to check: %s — any Kubernetes secret they name is not verified",
			describeSet(unrecognised))
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
// Secrets it references, plus any Ref-suffixed field it does not recognise.
//
// A known ref key whose value is not a string is descended into rather than
// recorded, so a future object-valued form still gets walked.
func collectSecretRefs(v any, into, unrecognised map[string]struct{}) {
	collectSecretRefsGuarded(v, into, unrecognised, walkGuard{})
}

func collectSecretRefsGuarded(v any, into, unrecognised map[string]struct{}, guard walkGuard) {
	if guard.seen(v) {
		return
	}
	switch node := v.(type) {
	case map[string]any:
		for key, child := range node {
			name, isString := child.(string)
			if _, known := secretRefKeys[key]; known {
				if isString && name != "" {
					into[name] = struct{}{}
					continue
				}
			} else if isString && strings.HasSuffix(key, unrecognisedRefKeySuffix) {
				unrecognised[key] = struct{}{}
			}
			collectSecretRefsGuarded(child, into, unrecognised, guard)
		}
	case []any:
		for _, child := range node {
			collectSecretRefsGuarded(child, into, unrecognised, guard)
		}
	}
}

// walkGuard records the composite nodes a tree walk has already visited.
//
// YAML aliases let a sub-kilobyte document describe an astronomically large
// logical tree (an "anchor bomb"): goccy/go-yaml resolves an alias by *sharing*
// the anchored node rather than copying it, so parsing stays cheap while a naive
// recursive walk traverses the fully expanded graph and effectively never
// returns — a ~700 byte CR file was measured at 12 seconds and a ~850 byte one
// at hours, on a flat 3 MB heap, with no way to interrupt it. Skipping nodes
// already visited collapses the walk back to the parsed document's real size: an
// alias pointing at a node we have already walked cannot contribute anything new.
type walkGuard map[uintptr]struct{}

// seen reports whether node has been walked before, recording it if not. Only
// non-empty maps and slices are tracked: scalars carry no identity worth
// deduplicating, and Go may hand distinct empty composites the same backing
// pointer (which would wrongly collapse them — harmless here, since an empty
// node has nothing to walk, but not worth relying on).
func (g walkGuard) seen(node any) bool {
	v := reflect.ValueOf(node)
	switch v.Kind() {
	case reflect.Map, reflect.Slice:
		if v.Len() == 0 {
			return false
		}
	default:
		return false
	}

	ptr := v.Pointer()
	if _, visited := g[ptr]; visited {
		return true
	}
	g[ptr] = struct{}{}
	return false
}

// treeContainsNonNilKey reports whether key appears anywhere in the subtree with
// a value that is not null. Null is excluded for the same reason fencedRoutes
// ignores `fence: null` — an explicitly nulled field is a field being removed,
// not a field being set. Guarded against alias amplification for the same reason
// as collectSecretRefs.
func treeContainsNonNilKey(v any, key string) bool {
	return treeContainsNonNilKeyGuarded(v, key, walkGuard{})
}

func treeContainsNonNilKeyGuarded(v any, key string, guard walkGuard) bool {
	if guard.seen(v) {
		return false
	}
	switch node := v.(type) {
	case map[string]any:
		if value, found := node[key]; found && value != nil {
			return true
		}
		for _, child := range node {
			if treeContainsNonNilKeyGuarded(child, key, guard) {
				return true
			}
		}
	case []any:
		for _, child := range node {
			if treeContainsNonNilKeyGuarded(child, key, guard) {
				return true
			}
		}
	}
	return false
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
// runtime.DeepCopyJSONValue, which panics on the uint64 goccy/go-yaml produces
// for a positive integer like nodeIdRanges.start (it tolerates int64 only).

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
