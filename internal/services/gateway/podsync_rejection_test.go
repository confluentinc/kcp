package gateway

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// withManagedFields sets the CR's metadata.managedFields.
func withManagedFields(entries ...metav1.ManagedFieldsEntry) gatewayCROption {
	return func(obj *unstructured.Unstructured) {
		obj.SetManagedFields(entries)
	}
}

// managedWrite is a write by manager that owns a spec field.
func managedWrite(manager, subresource string, at time.Time) metav1.ManagedFieldsEntry {
	return managedWriteOf(manager, subresource, at, `{"f:spec":{"f:configId":{}}}`)
}

// metadataWrite is a write by manager that owns only metadata fields, as an
// annotation or label edit, or CFK's own non-status writes, record.
func metadataWrite(manager string, at time.Time) metav1.ManagedFieldsEntry {
	return managedWriteOf(manager, "", at, `{"f:metadata":{"f:annotations":{"f:note":{}}}}`)
}

func managedWriteOf(manager, subresource string, at time.Time, fields string) metav1.ManagedFieldsEntry {
	return metav1.ManagedFieldsEntry{
		Manager:     manager,
		Operation:   metav1.ManagedFieldsOperationUpdate,
		Subresource: subresource,
		Time:        &metav1.Time{Time: at},
		FieldsType:  "FieldsV1",
		FieldsV1:    &metav1.FieldsV1{Raw: []byte(fields)},
	}
}

func TestCurrentFatalCondition(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)
	const msg = "secretRef not found"

	tests := []struct {
		name          string
		opts          []gatewayCROption
		wantRejected  bool
		wantUndecided bool
	}{
		{
			name:         "condition observedGeneration equals generation",
			opts:         []gatewayCROption{withGatewayConditionAt("ready", "False", "ApplyFailed", msg, 3, t0)},
			wantRejected: true,
		},
		{
			name: "condition observedGeneration older",
			opts: []gatewayCROption{withGatewayConditionAt("ready", "False", "ApplyFailed", msg, 2, t0)},
		},
		{
			name: "transition long before the last spec write",
			opts: []gatewayCROption{
				withGatewayConditionAtTime("ready", "False", "ApplyFailed", msg, t0.Add(-time.Hour)),
				withManagedFields(managedWrite("kcp", "", t0)),
			},
		},
		{
			name: "transition after the last spec write",
			opts: []gatewayCROption{
				withGatewayConditionAtTime("ready", "False", "ApplyFailed", msg, t0.Add(time.Minute)),
				withManagedFields(managedWrite("kcp", "", t0)),
			},
			wantRejected: true,
		},
		{
			name: "status writes are not spec writes",
			opts: []gatewayCROption{
				withGatewayConditionAtTime("ready", "False", "ApplyFailed", msg, t0.Add(time.Hour)),
				withManagedFields(managedWrite("kcp", "", t0), managedWrite("cfk", "status", t0.Add(2*time.Hour))),
			},
			wantRejected: true,
		},
		{
			name: "metadata-only writes are not spec writes",
			opts: []gatewayCROption{
				withGatewayConditionAtTime("ready", "False", "ApplyFailed", msg, t0.Add(time.Hour)),
				withManagedFields(managedWrite("kcp", "", t0), metadataWrite("cfk", t0.Add(2*time.Hour))),
			},
			wantRejected: true,
		},
		{
			name:          "no evidence at all",
			opts:          []gatewayCROption{withGatewayCondition("ready", "False", "ApplyFailed", msg)},
			wantRejected:  true,
			wantUndecided: true,
		},
		{
			name:          "transition time but no spec write recorded",
			opts:          []gatewayCROption{withGatewayConditionAtTime("ready", "False", "ApplyFailed", msg, t0)},
			wantRejected:  true,
			wantUndecided: true,
		},
		{
			name: "non-fatal reason ignored",
			opts: []gatewayCROption{withGatewayConditionAt("ready", "False", "Reconciling", msg, 3, t0)},
		},
		{
			name: "True condition ignored",
			opts: []gatewayCROption{withGatewayConditionAt("ready", "True", "ApplyFailed", msg, 3, t0)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cr := newGatewayCR("gw", "ns", 3, 3, true, tt.opts...)
			rej, undecided, err := currentFatalCondition(cr, "gw")
			require.NoError(t, err)
			assert.Equal(t, tt.wantRejected, rej != nil)
			assert.Equal(t, tt.wantUndecided, undecided)
			if rej != nil {
				assert.Equal(t, "gw", rej.Gateway)
				assert.Equal(t, "ApplyFailed", rej.Reason)
				assert.Equal(t, msg, rej.Message)
				assert.Equal(t, int64(3), rej.Generation)
				assert.Equal(t, int64(3), rej.ObservedGeneration)
			}
		})
	}
}

func TestLastSpecWriteTime(t *testing.T) {
	t0 := time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC)

	t.Run("picks the newest non-status entry", func(t *testing.T) {
		cr := newGatewayCR("gw", "ns", 1, 1, false, withManagedFields(
			managedWrite("a", "", t0.Add(-time.Hour)),
			managedWrite("b", "", t0),
			managedWrite("c", "status", t0.Add(time.Hour)),
		))
		got, ok := lastSpecWriteTime(cr)
		require.True(t, ok)
		assert.True(t, got.Equal(t0))
	})

	t.Run("false for an empty list", func(t *testing.T) {
		_, ok := lastSpecWriteTime(newGatewayCR("gw", "ns", 1, 1, false))
		assert.False(t, ok)
	})

	t.Run("false for only status entries", func(t *testing.T) {
		cr := newGatewayCR("gw", "ns", 1, 1, false, withManagedFields(managedWrite("c", "status", t0)))
		_, ok := lastSpecWriteTime(cr)
		assert.False(t, ok)
	})

	t.Run("skips entries that own no spec field", func(t *testing.T) {
		cr := newGatewayCR("gw", "ns", 1, 1, false, withManagedFields(
			managedWrite("a", "", t0),
			metadataWrite("b", t0.Add(time.Hour)),
		))
		got, ok := lastSpecWriteTime(cr)
		require.True(t, ok)
		assert.True(t, got.Equal(t0))
	})

	t.Run("false for only metadata entries", func(t *testing.T) {
		cr := newGatewayCR("gw", "ns", 1, 1, false, withManagedFields(metadataWrite("b", t0)))
		_, ok := lastSpecWriteTime(cr)
		assert.False(t, ok)
	})
}
