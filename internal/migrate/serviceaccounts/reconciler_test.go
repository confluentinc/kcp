package serviceaccounts

import (
	"context"
	"errors"
	"fmt"
	"strings"
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
	// users scripts UserExists: an id present with true exists, present with
	// false is a 404, absent defaults to not-found. userErr forces a lookup
	// failure for an id.
	users   map[string]bool
	userErr map[string]error
}

func newFakeClient() *fakeClient {
	return &fakeClient{
		existing: map[string]*ServiceAccount{},
		created:  map[string]string{},
	}
}

func (f *fakeClient) UserExists(ctx context.Context, id string) (bool, error) {
	if err := f.userErr[id]; err != nil {
		return false, err
	}
	return f.users[id], nil
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
	require.ErrorContains(t, err, "could not be resolved to a Confluent Cloud identity")
	require.ErrorContains(t, err, "serviceAccounts.autoCreate")
	require.ErrorContains(t, err, "unmapped (1):")
	require.ErrorContains(t, err, "User:app")
}

// TestReconciler_AutoCreateFalse_MappedAndGap_ErrorListsBoth proves the
// resolution error is a single structured block: it lists every unmapped
// principal AND, separately, the principals that DID resolve via mapping — so an
// operator sees what worked (their one mapping) alongside what still needs a
// home, instead of a flat list of only the failures. It must NOT list the
// mapped principal as unmapped.
func TestReconciler_AutoCreateFalse_MappedAndGap_ErrorListsBoth(t *testing.T) {
	r := New(Config{
		AutoCreate: false,
		Mapping:    map[string]string{"User:mapped": "sa-1"},
		Principals: []string{"User:mapped", "User:gap-b", "User:gap-a"},
		Client:     newFakeClient(),
	})
	_, err := r.Plan(context.Background())
	require.Error(t, err)
	msg := err.Error()

	// Summary counts the problems (2 gaps), and both gaps are listed under
	// "unmapped".
	require.Contains(t, msg, "2 ACL principal(s) could not be resolved")
	require.Contains(t, msg, "unmapped (2):")
	require.Contains(t, msg, "User:gap-a")
	require.Contains(t, msg, "User:gap-b")

	// The mapped principal is reported as resolved, with its id, and never as a gap.
	require.Contains(t, msg, "already resolved (1):")
	require.Contains(t, msg, "User:mapped -> sa-1")

	// Regression: the gaps must be sorted so the mapped principal isn't mistaken
	// for the first gap, and the mapped principal must not appear under "unmapped".
	unmappedBlock := msg[strings.Index(msg, "unmapped (2):"):strings.Index(msg, "already resolved")]
	require.NotContains(t, unmappedBlock, "User:mapped")
}

// TestReconciler_MappedSAMissing_HardErrors proves that when the CC
// service-account set is known (ExistingSAIDs non-nil, i.e. a Confluent Cloud
// target), a mapping to an "sa-" id that does not exist is a hard error listed
// under "mapping target not found" — a real "sa-" mapping still resolves.
func TestReconciler_MappedSAMissing_HardErrors(t *testing.T) {
	r := New(Config{
		AutoCreate:    false,
		Mapping:       map[string]string{"User:good": "sa-real", "User:bad": "sa-ghost"},
		Principals:    []string{"User:good", "User:bad"},
		Client:        newFakeClient(),
		ExistingSAIDs: map[string]bool{"sa-real": true},
	})
	_, err := r.Plan(context.Background())
	require.Error(t, err)
	msg := err.Error()
	require.Contains(t, msg, "mapping target not found in Confluent Cloud (1):")
	require.Contains(t, msg, "User:bad -> sa-ghost")
	require.Contains(t, msg, "already resolved (1):")
	require.Contains(t, msg, "User:good -> sa-real")
}

// TestReconciler_MappedUser_ExistenceChecked proves a "u-" mapping is verified
// via a point lookup: an existing user resolves, a missing one is a hard error.
func TestReconciler_MappedUser_ExistenceChecked(t *testing.T) {
	fc := newFakeClient()
	fc.users = map[string]bool{"u-live": true, "u-gone": false}
	r := New(Config{
		Mapping:       map[string]string{"User:a": "u-live", "User:b": "u-gone"},
		Principals:    []string{"User:a", "User:b"},
		Client:        fc,
		ExistingSAIDs: map[string]bool{}, // non-nil → validation on (CC target)
	})
	_, err := r.Plan(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "mapping target not found in Confluent Cloud (1):")
	require.ErrorContains(t, err, "User:b -> u-gone")
	require.ErrorContains(t, err, "User:a -> u-live") // the live user resolved
}

// TestReconciler_MappedUserLookupError_Propagates proves a non-404 user lookup
// failure surfaces as a genuine error, not a silent "not found".
func TestReconciler_MappedUserLookupError_Propagates(t *testing.T) {
	fc := newFakeClient()
	fc.userErr = map[string]error{"u-x": errors.New("boom")}
	r := New(Config{
		Mapping:       map[string]string{"User:a": "u-x"},
		Principals:    []string{"User:a"},
		Client:        fc,
		ExistingSAIDs: map[string]bool{},
	})
	_, err := r.Plan(context.Background())
	require.ErrorContains(t, err, "validating mapping")
	require.ErrorContains(t, err, "boom")
}

// TestReconciler_MappedPool_NotVerified_Resolves proves a "pool-" mapping is
// accepted (resolved) without a hard existence check — pools can't be
// point-looked-up — even when validation is on.
func TestReconciler_MappedPool_NotVerified_Resolves(t *testing.T) {
	r := New(Config{
		Mapping:       map[string]string{"User:a": "pool-xyz"},
		Principals:    []string{"User:a"},
		Client:        newFakeClient(),
		ExistingSAIDs: map[string]bool{},
	})
	_, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, "User:pool-xyz", r.ResolvedMap()["User:a"])
}

// TestReconciler_MappedValueUserPrefixed_NormalizedAndValidated proves a
// "User:"-prefixed mapping value (a form manifest validation explicitly
// accepts) resolves to a SINGLE-prefixed principal and is existence-checked —
// not double-prefixed ("User:User:sa-1") with the check silently skipped.
func TestReconciler_MappedValueUserPrefixed_NormalizedAndValidated(t *testing.T) {
	// exists → resolves to exactly "User:sa-1"
	r := New(Config{
		Mapping:       map[string]string{"User:a": "User:sa-1"},
		Principals:    []string{"User:a"},
		Client:        newFakeClient(),
		ExistingSAIDs: map[string]bool{"sa-1": true},
	})
	_, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, "User:sa-1", r.ResolvedMap()["User:a"]) // not "User:User:sa-1"

	// missing → the existence check now runs on the User:-prefixed form (bare id
	// extracted) and fails, instead of being skipped as an unrecognized prefix.
	r2 := New(Config{
		Mapping:       map[string]string{"User:a": "User:sa-ghost"},
		Principals:    []string{"User:a"},
		Client:        newFakeClient(),
		ExistingSAIDs: map[string]bool{"sa-1": true},
	})
	_, err = r2.Plan(context.Background())
	require.ErrorContains(t, err, "mapping target not found in Confluent Cloud")
	require.ErrorContains(t, err, "User:a -> sa-ghost")
}

// TestReconciler_NilExistingSAIDs_SkipsValidation proves the historical
// trust-verbatim behaviour is preserved for a non-Confluent-Cloud target: with
// a nil ExistingSAIDs set, a mapping to a non-existent "sa-" id is used as-is.
func TestReconciler_NilExistingSAIDs_SkipsValidation(t *testing.T) {
	r := New(Config{
		Mapping:    map[string]string{"User:a": "sa-ghost"},
		Principals: []string{"User:a"},
		Client:     newFakeClient(),
		// ExistingSAIDs left nil → no validation.
	})
	_, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.Equal(t, "User:sa-ghost", r.ResolvedMap()["User:a"])
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
