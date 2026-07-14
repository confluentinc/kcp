package serviceaccounts

import (
	"context"
	"fmt"
	"testing"

	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/stretchr/testify/require"
)

// fakeClient is an in-memory CCClient: existing/created service accounts are
// keyed by display name, matching the real API's unique-display_name
// contract (decision §6).
type fakeClient struct {
	existing  map[string]*ServiceAccount // by display name
	created   map[string]string          // display name -> generated id
	findErr   map[string]error
	createErr map[string]error
	nextID    int
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		existing: map[string]*ServiceAccount{},
		created:  map[string]string{},
	}
}

func (f *fakeClient) FindByDisplayName(ctx context.Context, name string) (*ServiceAccount, error) {
	if err := f.findErr[name]; err != nil {
		return nil, err
	}
	if sa, ok := f.existing[name]; ok {
		return sa, nil
	}
	return nil, nil
}

func (f *fakeClient) Create(ctx context.Context, name, description string) (*ServiceAccount, error) {
	if err := f.createErr[name]; err != nil {
		return nil, err
	}
	f.nextID++
	id := fmt.Sprintf("sa-%d", f.nextID)
	sa := &ServiceAccount{ID: id, DisplayName: name, Description: description}
	f.existing[name] = sa
	f.created[name] = id
	return sa, nil
}

func mustPlan(t *testing.T, r *Reconciler) reconcile.Plan {
	t.Helper()
	p, err := r.Plan(context.Background())
	require.NoError(t, err)
	return p
}

func TestReconciler_AutoCreateAndMappingAndSkip(t *testing.T) {
	fc := newFakeClient()
	fc.existing["legacy"] = &ServiceAccount{ID: "sa-legacy", DisplayName: "legacy"}
	r := New(Config{
		AutoCreate: true,
		Mapping:    map[string]string{"User:legacy": "sa-legacy", "User:ANONYMOUS": ""}, // ANONYMOUS unmapped
		Principals: []string{"User:app-consumer", "User:legacy", "User:*", "User:ANONYMOUS"},
		Client:     fc,
	})
	_, err := r.Plan(context.Background())
	require.NoError(t, err)
	_, err = r.Apply(context.Background(), mustPlan(t, r))
	require.NoError(t, err)
	m := r.ResolvedMap()
	require.Equal(t, "User:sa-legacy", m["User:legacy"])                         // mapping
	require.Equal(t, "User:"+fc.created["app-consumer"], m["User:app-consumer"]) // auto-created
	require.NotContains(t, m, "User:*")                                          // skipped
	require.NotContains(t, m, "User:ANONYMOUS")                                  // skipped (unmapped)
}

func TestReconciler_AutoCreateFalseUnmapped_Errors(t *testing.T) {
	r := New(Config{AutoCreate: false, Principals: []string{"User:app"}, Client: newFakeClient()})
	_, err := r.Plan(context.Background())
	require.ErrorContains(t, err, "no service-account mapping for principal \"User:app\"")
}
