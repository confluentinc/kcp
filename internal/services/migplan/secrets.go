package migplan

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// SecretExistenceChecker reports which of a list of Kubernetes Secret names
// don't exist in its namespace. Namespace is bound at construction (mirrors
// GatewayLive/ClusterLinkStatus's shape), not passed per-call — the static
// route strategy's one live-I/O precondition (see the migplan
// static-route-strategy design doc, decision 4): does every secret the
// switchover target's own staged auth block references actually exist. Not
// a CR-scanning interface — the caller (engine.go) resolves which names to
// check via reconcile.ResolveStagedSecretNames and passes exactly those.
//
// skipReason is non-empty when the check could not run at all (a permission
// denial, not a missing secret) — see MissingSecrets' own doc comment for
// why this must never be conflated with err.
type SecretExistenceChecker interface {
	MissingSecrets(ctx context.Context, names []string) (missing []string, skipReason string, err error)
}

var _ SecretExistenceChecker = (*K8sSecretChecker)(nil)

// K8sSecretChecker checks Secret existence via a live Kubernetes clientset.
// It implements SecretExistenceChecker.
//
// It depends on kubernetes.Interface directly rather than a further-narrowed
// interface: unlike gatewayCRReader/linkReader — which narrow away large,
// multi-purpose services (gateway.K8sService, clusterlink.Service) down to
// the one or two methods this package needs — kubernetes.Interface is
// already the standard client-go seam, and internal/services/gateway's own
// checkSecretRefsExist (validate.go) depends on it directly for this exact
// Secrets(namespace).Get call. An extra narrowing interface here would only
// rename kubernetes.Interface's Secrets-get path without shrinking it.
type K8sSecretChecker struct {
	clientset kubernetes.Interface
	namespace string
}

// NewK8sSecretChecker builds a live secret-existence checker bound to namespace.
func NewK8sSecretChecker(clientset kubernetes.Interface, namespace string) *K8sSecretChecker {
	return &K8sSecretChecker{clientset: clientset, namespace: namespace}
}

// MissingSecrets checks each name and returns those that don't exist, in the
// order given. A "not found" from the API server is not an error — it's the
// exact fact this method reports.
//
// A permission denial (Forbidden/Unauthorized) is reported via a non-empty
// skipReason, not err, and short-circuits the sweep without reporting any
// names as missing: many migration operators are not granted secrets-read
// RBAC, and a check this new must not block a migration that would
// otherwise succeed. This mirrors AAO's own historical checkSecretRefsExist
// tolerance (removed by this integration, but the reasoning survives here):
// a denial downgrades to a recorded skip, and only a confirmed NotFound
// refuses. Found live, the hard way: this exact gap made every AAO e2e
// migration fail outright the first time this check ran against an
// RBAC-restricted namespace.
//
// Only an unexpected API failure — neither NotFound nor a permission
// denial — is returned as err.
func (c *K8sSecretChecker) MissingSecrets(ctx context.Context, names []string) (missing []string, skipReason string, err error) {
	for _, name := range names {
		_, getErr := c.clientset.CoreV1().Secrets(c.namespace).Get(ctx, name, metav1.GetOptions{})
		if getErr == nil {
			continue
		}
		if apierrors.IsForbidden(getErr) || apierrors.IsUnauthorized(getErr) {
			return nil, fmt.Sprintf("no permission to read secrets in namespace %q — staged auth secret existence was not verified", c.namespace), nil
		}
		if apierrors.IsNotFound(getErr) {
			missing = append(missing, name)
			continue
		}
		return nil, "", fmt.Errorf("checking secret %q in namespace %q: %w", name, c.namespace, getErr)
	}
	return missing, "", nil
}
