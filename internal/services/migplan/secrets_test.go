package migplan

import (
	"context"
	"fmt"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

	missing, skipReason, err := checker.MissingSecrets(context.Background(), []string{"cc-sasl-secret"})
	if err != nil {
		t.Fatalf("MissingSecrets: %v", err)
	}
	if skipReason != "" {
		t.Fatalf("skipReason = %q, want empty", skipReason)
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

	missing, skipReason, err := checker.MissingSecrets(context.Background(), []string{"present-secret", "absent-secret"})
	if err != nil {
		t.Fatalf("MissingSecrets: %v", err)
	}
	if skipReason != "" {
		t.Fatalf("skipReason = %q, want empty", skipReason)
	}
	if len(missing) != 1 || missing[0] != "absent-secret" {
		t.Fatalf("missing = %v, want [absent-secret]", missing)
	}
}

func TestK8sSecretChecker_MissingSecrets_EmptyInput(t *testing.T) {
	checker := NewK8sSecretChecker(newFakeClientset(), "confluent")
	missing, skipReason, err := checker.MissingSecrets(context.Background(), nil)
	if err != nil {
		t.Fatalf("MissingSecrets: %v", err)
	}
	if skipReason != "" {
		t.Fatalf("skipReason = %q, want empty", skipReason)
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none for empty input", missing)
	}
}

// TestK8sSecretChecker_MissingSecrets_PermissionDeniedIsSkippedNotFailed is a
// regression test for a real bug found via live e2e testing: the first
// version of this checker treated a Forbidden response as a hard error,
// which made every AAO migration fail outright in an RBAC-restricted
// namespace — a strictly worse guarantee than AAO's own retired
// checkSecretRefsExist, which explicitly tolerated exactly this case.
func TestK8sSecretChecker_MissingSecrets_PermissionDeniedIsSkippedNotFailed(t *testing.T) {
	clientset := newFakeClientset()
	clientset.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			corev1.Resource("secrets"), "plain-jaas",
			fmt.Errorf("User \"system:serviceaccount:confluent:kcp-runner\" cannot get resource \"secrets\""))
	})
	checker := NewK8sSecretChecker(clientset, "confluent")

	missing, skipReason, err := checker.MissingSecrets(context.Background(), []string{"plain-jaas"})
	if err != nil {
		t.Fatalf("MissingSecrets: %v, want no error (a denial is a skip, not a failure)", err)
	}
	if skipReason == "" {
		t.Fatal("skipReason = \"\", want a non-empty reason describing the permission denial")
	}
	if len(missing) != 0 {
		t.Fatalf("missing = %v, want none — a denial must not be reported as a confirmed-missing secret", missing)
	}
}
