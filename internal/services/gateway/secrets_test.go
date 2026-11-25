package gateway

import (
	"context"
	"fmt"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestSecretChecker_MissingSecrets_AllPresent(t *testing.T) {
	clientset := newFakeClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "cc-sasl-secret", Namespace: "confluent"}},
	)
	checker := NewSecretChecker(newK8sServiceWithClients(nil, clientset), "confluent")

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

func TestSecretChecker_MissingSecrets_SomeAbsent(t *testing.T) {
	clientset := newFakeClientset(
		&corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: "present-secret", Namespace: "confluent"}},
	)
	checker := NewSecretChecker(newK8sServiceWithClients(nil, clientset), "confluent")

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

func TestSecretChecker_MissingSecrets_EmptyInput(t *testing.T) {
	checker := NewSecretChecker(newK8sServiceWithClients(nil, newFakeClientset()), "confluent")
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

// TestSecretChecker_MissingSecrets_PermissionDeniedIsSkippedNotFailed is a
// regression test for a real bug found via live e2e testing: the first
// version of this checker treated a Forbidden response as a hard error,
// which made every AAO migration fail outright in an RBAC-restricted
// namespace — a strictly worse guarantee than AAO's own retired
// checkSecretRefsExist, which explicitly tolerated exactly this case.
func TestSecretChecker_MissingSecrets_PermissionDeniedIsSkippedNotFailed(t *testing.T) {
	clientset := newFakeClientset()
	clientset.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewForbidden(
			corev1.Resource("secrets"), "plain-jaas",
			fmt.Errorf("User \"system:serviceaccount:confluent:kcp-runner\" cannot get resource \"secrets\""))
	})
	checker := NewSecretChecker(newK8sServiceWithClients(nil, clientset), "confluent")

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

// TestSecretChecker_MissingSecrets_UnexpectedErrorIsWrapped covers the third
// branch of the policy (not ported from migplan, which had no test for it).
func TestSecretChecker_MissingSecrets_UnexpectedErrorIsWrapped(t *testing.T) {
	clientset := newFakeClientset()
	clientset.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, fmt.Errorf("boom")
	})
	checker := NewSecretChecker(newK8sServiceWithClients(nil, clientset), "confluent")

	missing, skipReason, err := checker.MissingSecrets(context.Background(), []string{"s1"})

	require.Error(t, err)
	assert.Contains(t, err.Error(), `checking secret "s1" in namespace "confluent": boom`)
	assert.Empty(t, skipReason)
	assert.Empty(t, missing)
}

// TestSecretChecker_MissingSecrets_PermissionDeniedMessage pins the skip text.
func TestSecretChecker_MissingSecrets_PermissionDeniedMessage(t *testing.T) {
	clientset := newFakeClientset()
	clientset.(*kubernetesfake.Clientset).PrependReactor("get", "secrets", func(action k8stesting.Action) (bool, runtime.Object, error) {
		return true, nil, apierrors.NewUnauthorized("nope")
	})
	checker := NewSecretChecker(newK8sServiceWithClients(nil, clientset), "confluent")

	_, skipReason, err := checker.MissingSecrets(context.Background(), []string{"a", "b"})

	require.NoError(t, err)
	assert.Equal(t, "no permission to read secrets in namespace \"confluent\" \u2014 staged auth secret existence was not verified", skipReason)
}

// TestSecretChecker_MissingSecrets_BadKubeconfigIsAnErrorAtCallTime: the typed
// client is obtained lazily, so a bad kubeconfig surfaces from MissingSecrets,
// not from construction.
func TestSecretChecker_MissingSecrets_BadKubeconfigIsAnErrorAtCallTime(t *testing.T) {
	checker := NewSecretChecker(NewK8sService("/does/not/exist"), "confluent")

	_, _, err := checker.MissingSecrets(context.Background(), []string{"s1"})

	require.Error(t, err)
}

func TestK8sServiceWithClients_NilDynamicFailsClearly(t *testing.T) {
	svc := newK8sServiceWithClients(nil, newFakeClientset())

	_, err := svc.GetGatewayGeneration(context.Background(), "ns", "gw")

	require.Error(t, err)
	assert.Contains(t, err.Error(), "without a dynamic client")
	cs, err := svc.clientset()
	require.NoError(t, err, "a nil dynamic client must not break the typed path")
	assert.NotNil(t, cs)
}

func TestK8sServiceWithClients_NilClientsetFailsClearly(t *testing.T) {
	svc := newK8sServiceWithClients(newFakeDynamicClient(), nil)

	_, err := svc.clientset()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "without a clientset")
}
