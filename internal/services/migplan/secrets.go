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
type SecretExistenceChecker interface {
	MissingSecrets(ctx context.Context, names []string) ([]string, error)
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
// exact fact this method reports; only an unexpected API failure is returned
// as err.
func (c *K8sSecretChecker) MissingSecrets(ctx context.Context, names []string) ([]string, error) {
	var missing []string
	for _, name := range names {
		_, err := c.clientset.CoreV1().Secrets(c.namespace).Get(ctx, name, metav1.GetOptions{})
		if err == nil {
			continue
		}
		if apierrors.IsNotFound(err) {
			missing = append(missing, name)
			continue
		}
		return nil, fmt.Errorf("checking secret %q in namespace %q: %w", name, c.namespace, err)
	}
	return missing, nil
}
