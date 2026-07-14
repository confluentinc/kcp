package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/stretchr/testify/require"
)

// fakeClient is an in-memory CCClient: existing/created service accounts are
// keyed by display name, matching the real API's unique-display_name
// contract (decision §6).
type fakeClient struct {
	existing map[string]*ServiceAccount // by display name
	created  map[string]string          // display name -> generated id
	findErr  map[string]error
	// createErr, keyed by display name, makes Create fail outright for that
	// name (simulating a non-409 error from the real client).
	createErr map[string]error
	// createOverride, keyed by display name, makes Create return this exact
	// account instead of synthesizing one from name/description — used to
	// script the 409-fallback returning a DIFFERENT principal's existing
	// account (a same-run name collision Plan's FindByDisplayName never saw).
	createOverride map[string]*ServiceAccount
	nextID         int
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
	if sa, ok := f.createOverride[name]; ok {
		return sa, nil
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

// TestReconciler_Plan_PopulatesResolvedMap verifies that Plan (not just Apply)
// populates ResolvedMap: mapped and already-existing principals resolve to
// their real "User:sa-<id>", a to-be-created principal resolves to a
// placeholder, and skipped principals (User:* / User:ANONYMOUS) stay out. This
// is what lets the acls reconciler render a meaningful dry-run preview.
func TestReconciler_Plan_PopulatesResolvedMap(t *testing.T) {
	fc := newFakeClient()
	fc.existing["legacy"] = &ServiceAccount{ID: "sa-legacy", DisplayName: "legacy", Description: descriptionFor("User:legacy")}
	r := New(Config{
		AutoCreate: true,
		Mapping:    map[string]string{"User:mapped": "sa-mapped"},
		Principals: []string{"User:new-app", "User:mapped", "User:legacy", "User:*"},
		Client:     fc,
	})

	_, err := r.Plan(context.Background())
	require.NoError(t, err)

	m := r.ResolvedMap()
	require.Equal(t, "User:sa-mapped", m["User:mapped"])                                   // mapping, real id at Plan time
	require.Equal(t, "User:sa-legacy", m["User:legacy"])                                   // existing, real id at Plan time
	require.Equal(t, placeholderFor(DeriveDisplayName("User:new-app")), m["User:new-app"]) // to-be-created placeholder
	require.NotContains(t, m, "User:*")                                                    // skipped, absent
}

func TestReconciler_AutoCreateFalseUnmapped_Errors(t *testing.T) {
	r := New(Config{AutoCreate: false, Principals: []string{"User:app"}, Client: newFakeClient()})
	_, err := r.Plan(context.Background())
	require.ErrorContains(t, err, "no service-account mapping for principal \"User:app\"")
}

// TestReconciler_FindTimeCollision_HardErrors covers the collision backstop
// when a service account with the derived display name already exists at
// plan time but its description belongs to a different principal.
func TestReconciler_FindTimeCollision_HardErrors(t *testing.T) {
	fc := newFakeClient()
	fc.existing["app"] = &ServiceAccount{ID: "sa-existing", DisplayName: "app", Description: "kcp:source-principal=User:other"}
	r := New(Config{AutoCreate: true, Principals: []string{"User:app"}, Client: fc})

	_, err := r.Plan(context.Background())

	require.Error(t, err)
	require.ErrorContains(t, err, "naming collision")
	require.ErrorContains(t, err, `"app"`)
	require.ErrorContains(t, err, "kcp:source-principal=User:other")
}

// TestReconciler_FindLookupError_HardErrors covers a FindByDisplayName
// failure during resolution being surfaced as a hard error rather than
// silently skipped.
func TestReconciler_FindLookupError_HardErrors(t *testing.T) {
	fc := newFakeClient()
	fc.findErr = map[string]error{"app": errors.New("connection refused")}
	r := New(Config{AutoCreate: true, Principals: []string{"User:app"}, Client: fc})

	_, err := r.Plan(context.Background())

	require.Error(t, err)
	require.ErrorContains(t, err, "looking up service account")
	require.ErrorContains(t, err, "connection refused")
}

// TestReconciler_CreatePathCollision_HardErrors covers the create-path
// collision backstop: Create's 409-fallback resolves "app" to an account
// that already exists for a DIFFERENT principal (a same-run name collision
// that never existed at plan time, so Plan's FindByDisplayName check didn't
// catch it). Apply must hard-error rather than silently attribute this
// principal's ACLs to the other principal's account.
func TestReconciler_CreatePathCollision_HardErrors(t *testing.T) {
	fc := newFakeClient()
	fc.createOverride = map[string]*ServiceAccount{
		"app": {ID: "sa-other", DisplayName: "app", Description: "kcp:source-principal=User:other"},
	}
	r := New(Config{AutoCreate: true, Principals: []string{"User:app"}, Client: fc})
	plan := mustPlan(t, r)

	outcome, err := r.Apply(context.Background(), plan)

	require.Error(t, err)
	require.Len(t, outcome.Failed, 1)
	require.Contains(t, outcome.Failed[0].Detail, "name collision")
	require.Contains(t, outcome.Failed[0].Detail, "kcp:source-principal=User:other")
	require.NotContains(t, r.ResolvedMap(), "User:app")
}
