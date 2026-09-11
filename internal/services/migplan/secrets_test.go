package migplan

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
)

// newFakeClientset constructs a fake kubernetes clientset seeded with the given
// objects. NewSimpleClientset is deprecated in favour of NewClientset, but the
// latter requires apply-configuration generation that this repo does not
// produce. Centralising the call here keeps the staticcheck suppression in one
// place (mirrors internal/services/gateway/gateway_test.go's own helper).
func newFakeClientset(objects ...runtime.Object) kubernetes.Interface {
	return kubernetesfake.NewSimpleClientset(objects...) //nolint:staticcheck // SA1019: see comment
}

func TestK8sSecretChecker_MissingSecrets_AllPresent(t *testing.T) {
	clientset := newFakeClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cc-sasl-secret", Namespace: "confluent"}},
	)
	checker := NewK8sSecretChecker(clientset, "confluent")

	missing, err := checker.MissingSecrets(context.Background(), []string{"cc-sasl-secret"})
	if err != nil {
		t.Fatalf("MissingSecrets: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none", missing)
	}
}

func TestK8sSecretChecker_MissingSecrets_SomeAbsent(t *testing.T) {
	clientset := newFakeClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "present-secret", Namespace: "confluent"}},
	)
	checker := NewK8sSecretChecker(clientset, "confluent")

	missing, err := checker.MissingSecrets(context.Background(), []string{"present-secret", "absent-secret"})
	if err != nil {
		t.Fatalf("MissingSecrets: %v", err)
	}
	if len(missing) != 1 || missing[0] != "absent-secret" {
		t.Fatalf("missing = %v, want [absent-secret]", missing)
	}
}

func TestK8sSecretChecker_MissingSecrets_EmptyInput(t *testing.T) {
	checker := NewK8sSecretChecker(newFakeClientset(), "confluent")
	missing, err := checker.MissingSecrets(context.Background(), nil)
	if err != nil {
		t.Fatalf("MissingSecrets: %v", err)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none for empty input", missing)
	}
}
