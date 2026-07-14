package acls

import (
	"context"
	"testing"

	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeACLClient is a minimal in-memory ACLClient double. It deliberately has
// no delete method at all — reconciling against it can only ever grow
// existing, proving structurally that the reconciler cannot delete.
type fakeACLClient struct {
	existing  []types.Acls
	created   []types.Acls
	listErr   error
	createErr error
}

func (f *fakeACLClient) List(ctx context.Context) ([]types.Acls, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.existing, nil
}

func (f *fakeACLClient) Create(ctx context.Context, a types.Acls) error {
	if f.createErr != nil {
		return f.createErr
	}
	f.created = append(f.created, a)
	f.existing = append(f.existing, a)
	return nil
}

// acl builds a canonical Allow-Read-on-Topic-"orders" ACL for the given
// principal — the one varying field every test needs.
func acl(principal string) types.Acls {
	return types.Acls{
		ResourceType:        "Topic",
		ResourceName:        "orders",
		ResourcePatternType: "Literal",
		Principal:           principal,
		Host:                "*",
		Operation:           "Read",
		PermissionType:      "Allow",
	}
}

func TestReconciler_Plan_CreatesWhenAbsent(t *testing.T) {
	client := &fakeACLClient{}
	r := New(Config{
		Desired:            []types.Acls{acl("User:app1")},
		ResolvedPrincipals: func() map[string]string { return map[string]string{"User:app1": "User:sa-abc123"} },
		Client:             client,
	})

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.False(t, plan.Empty())
	changes := plan.Changes()
	require.Len(t, changes, 1)
	require.Equal(t, reconcile.ActionCreate, changes[0].Action)

	outcome, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Len(t, outcome.Created, 1)
	require.Len(t, client.created, 1)
	require.Equal(t, "User:sa-abc123", client.created[0].Principal)
}

func TestReconciler_Plan_PresentWhenAlreadyExists(t *testing.T) {
	existing := acl("User:sa-abc123")
	client := &fakeACLClient{existing: []types.Acls{existing}}
	r := New(Config{
		Desired:            []types.Acls{acl("User:app1")},
		ResolvedPrincipals: func() map[string]string { return map[string]string{"User:app1": "User:sa-abc123"} },
		Client:             client,
	})

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, plan.Empty())
	changes := plan.Changes()
	require.Len(t, changes, 1)
	require.Equal(t, reconcile.ActionPresent, changes[0].Action)

	outcome, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Empty(t, outcome.Created)
	require.Empty(t, client.created)
}

func TestReconciler_Reapply_SecondPlanIsEmpty(t *testing.T) {
	client := &fakeACLClient{}
	cfg := Config{
		Desired:            []types.Acls{acl("User:app1")},
		ResolvedPrincipals: func() map[string]string { return map[string]string{"User:app1": "User:sa-abc123"} },
		Client:             client,
	}
	r := New(cfg)

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	_, err = r.Apply(context.Background(), plan)
	require.NoError(t, err)

	// A fresh reconciler over the same (now-mutated) client, as a second real
	// run would see it.
	r2 := New(cfg)
	plan2, err := r2.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, plan2.Empty())
	for _, c := range plan2.Changes() {
		require.Equal(t, reconcile.ActionPresent, c.Action)
	}
}

func TestReconciler_Plan_SkipsPrincipalNotInMap(t *testing.T) {
	client := &fakeACLClient{}
	r := New(Config{
		Desired:            []types.Acls{acl("User:*")}, // warn-skipped upstream: absent from PrincipalMap
		ResolvedPrincipals: func() map[string]string { return map[string]string{} },
		Client:             client,
	})

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.True(t, plan.Empty())
	require.Empty(t, plan.Changes())

	outcome, err := r.Apply(context.Background(), plan)
	require.NoError(t, err)
	require.Empty(t, outcome.Created)
	require.Empty(t, client.created)
}

func TestReconciler_ExtraTargetACL_IsNeverDeleted(t *testing.T) {
	extra := acl("User:sa-other")
	client := &fakeACLClient{existing: []types.Acls{extra}}
	r := New(Config{
		Desired:            []types.Acls{acl("User:app1")},
		ResolvedPrincipals: func() map[string]string { return map[string]string{"User:app1": "User:sa-abc123"} },
		Client:             client,
	})

	plan, err := r.Plan(context.Background())
	require.NoError(t, err)
	require.False(t, plan.Empty())
	// Only the desired ACL is represented in the plan; the extra target ACL is
	// not reported as removable at all (additive).
	changes := plan.Changes()
	require.Len(t, changes, 1)
	require.Equal(t, reconcile.ActionCreate, changes[0].Action)

	_, err = r.Apply(context.Background(), plan)
	require.NoError(t, err)

	found := false
	for _, e := range client.existing {
		if e == extra {
			found = true
		}
	}
	require.True(t, found, "pre-existing target ACL must never be removed")
}

func TestReconciler_Name(t *testing.T) {
	r := New(Config{})
	require.Equal(t, "acls", r.Name())
}
