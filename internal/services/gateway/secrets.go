package gateway

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// SecretChecker reports which Kubernetes Secrets don't exist in its namespace.
// It reads through the K8sService's typed client, obtained at call time, so a
// bad kubeconfig surfaces as an error from MissingSecrets, not at
// construction. Its method set satisfies migplan's SecretExistenceChecker.
type SecretChecker struct {
	svc       *K8sService
	namespace string
}

// NewSecretChecker builds a secret-existence checker bound to namespace.
func NewSecretChecker(svc *K8sService, namespace string) *SecretChecker {
	return &SecretChecker{svc: svc, namespace: namespace}
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
func (c *SecretChecker) MissingSecrets(ctx context.Context, names []string) (missing []string, skipReason string, err error) {
	clientset, err := c.svc.clientset()
	if err != nil {
		return nil, "", err
	}
	for _, name := range names {
		_, getErr := clientset.CoreV1().Secrets(c.namespace).Get(ctx, name, metav1.GetOptions{})
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
