// Hermetic (no live cluster) integration test for the ACL-migration pipeline:
// the REAL serviceaccounts.Reconciler and acls.Reconciler wired together
// through the REAL reconcile.Engine, against in-memory fake Confluent Cloud
// clients. This complements — and deliberately does not duplicate — the
// per-reconciler unit tests (reconciler_test.go here and in
// internal/migrate/serviceaccounts) and the cmd-level apply test: its value
// is only in behaviours that emerge from the two reconcilers running through
// the engine together.
package acls

import (
	"bytes"
	"context"
	"fmt"
	"testing"

	"github.com/confluentinc/kcp/internal/migrate/serviceaccounts"
	"github.com/confluentinc/kcp/internal/services/reconcile"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// fakeSAClient is a minimal in-memory serviceaccounts.CCClient double, keyed
// by display name like the real API's unique-display_name contract.
type fakeSAClient struct {
	existing map[string]*serviceaccounts.ServiceAccount
	nextID   int
}

func newFakeSAClient() *fakeSAClient {
	return &fakeSAClient{existing: map[string]*serviceaccounts.ServiceAccount{}}
}

func (f *fakeSAClient) FindByDisplayName(ctx context.Context, name string) (*serviceaccounts.ServiceAccount, error) {
	if sa, ok := f.existing[name]; ok {
		return sa, nil
	}
	return nil, nil
}

func (f *fakeSAClient) Create(ctx context.Context, name, description string) (*serviceaccounts.ServiceAccount, error) {
	f.nextID++
	sa := &serviceaccounts.ServiceAccount{ID: fmt.Sprintf("sa-%d", f.nextID), DisplayName: name, Description: description}
	f.existing[name] = sa
	return sa, nil
}

// buildPipeline wires a fresh serviceAccounts + acls reconciler pair over the
// given (possibly already-mutated, for a second run) fake clients — mirroring
// exactly how cmd/migrate/apply builds the same two reconcilers.
func buildPipeline(saClient *fakeSAClient, aclClient *fakeACLClient, desired []types.Acls, principals []string) (*serviceaccounts.Reconciler, *Reconciler) {
	saRec := serviceaccounts.New(serviceaccounts.Config{
		AutoCreate: true,
		Principals: principals,
		Client:     saClient,
	})
	aclRec := New(Config{
		Desired:            desired,
		ResolvedPrincipals: saRec.ResolvedMap,
		Client:             aclClient,
	})
	return saRec, aclRec
}

// TestIntegration_Ordering_ResolvedPrincipalFlowsIntoCreatedACL proves the
// engine runs serviceAccounts to completion (Plan+Apply) before acls' Plan
// ever runs: the ACL created on the target carries the real "User:sa-…" id
// that serviceAccounts' Apply produced, not the source principal.
func TestIntegration_Ordering_ResolvedPrincipalFlowsIntoCreatedACL(t *testing.T) {
	saClient := newFakeSAClient()
	aclClient := &fakeACLClient{}
	saRec, aclRec := buildPipeline(saClient, aclClient, []types.Acls{acl("User:app1")}, []string{"User:app1"})

	engine := reconcile.NewEngine(&bytes.Buffer{})
	_, err := engine.Run(context.Background(), []reconcile.Reconciler{saRec, aclRec}, false)
	require.NoError(t, err)

	require.Len(t, aclClient.created, 1)
	require.Equal(t, "User:sa-1", aclClient.created[0].Principal)
	require.Len(t, saClient.existing, 1)
}

// TestIntegration_Idempotent_SecondRunCreatesNothing proves a second engine
// run over the now-mutated clients creates nothing at either stage: every
// change in both outcomes is ActionPresent.
func TestIntegration_Idempotent_SecondRunCreatesNothing(t *testing.T) {
	saClient := newFakeSAClient()
	aclClient := &fakeACLClient{}
	desired := []types.Acls{acl("User:app1")}
	principals := []string{"User:app1"}

	engine := reconcile.NewEngine(&bytes.Buffer{})
	saRec, aclRec := buildPipeline(saClient, aclClient, desired, principals)
	_, err := engine.Run(context.Background(), []reconcile.Reconciler{saRec, aclRec}, false)
	require.NoError(t, err)

	// A fresh pair of reconcilers over the same, now-mutated fake clients, as
	// a second real invocation of `kcp migrate apply` would see them.
	saRec2, aclRec2 := buildPipeline(saClient, aclClient, desired, principals)
	report, err := engine.Run(context.Background(), []reconcile.Reconciler{saRec2, aclRec2}, false)
	require.NoError(t, err)

	for name, outcome := range report.Outcomes {
		require.Empty(t, outcome.Created, "reconciler %q created something on second run", name)
		require.Empty(t, outcome.Failed, "reconciler %q failed on second run", name)
	}
	require.Len(t, saClient.existing, 1, "no additional service account created")
	require.Len(t, aclClient.created, 1, "no additional ACL created")
}

// TestIntegration_Additive_PreSeededExtraACLNeverTouched proves an ACL that
// pre-exists on the target but is not in the desired set survives the run
// untouched — the pipeline is strictly additive end to end.
func TestIntegration_Additive_PreSeededExtraACLNeverTouched(t *testing.T) {
	extra := acl("User:sa-other")
	saClient := newFakeSAClient()
	aclClient := &fakeACLClient{existing: []types.Acls{extra}}
	saRec, aclRec := buildPipeline(saClient, aclClient, []types.Acls{acl("User:app1")}, []string{"User:app1"})

	engine := reconcile.NewEngine(&bytes.Buffer{})
	_, err := engine.Run(context.Background(), []reconcile.Reconciler{saRec, aclRec}, false)
	require.NoError(t, err)

	found := false
	for _, e := range aclClient.existing {
		if e == extra {
			found = true
		}
	}
	require.True(t, found, "pre-seeded target ACL not in the desired set must never be removed")
	// The one desired ACL was created in addition to (not instead of) extra.
	require.Len(t, aclClient.existing, 2)
}

// TestIntegration_DryRun_MutatesNothingButRendersMeaningfulPlan proves
// dryRun=true never calls either reconciler's Apply — no service account and
// no ACL is created on either fake client — while still rendering a plan
// with the ACL's principal already resolved (to serviceAccounts' pending
// placeholder, since Apply never ran to mint a real id).
func TestIntegration_DryRun_MutatesNothingButRendersMeaningfulPlan(t *testing.T) {
	saClient := newFakeSAClient()
	aclClient := &fakeACLClient{}
	saRec, aclRec := buildPipeline(saClient, aclClient, []types.Acls{acl("User:app1")}, []string{"User:app1"})

	var out bytes.Buffer
	engine := reconcile.NewEngine(&out)
	report, err := engine.Run(context.Background(), []reconcile.Reconciler{saRec, aclRec}, true)
	require.NoError(t, err)
	require.True(t, report.DryRun)

	require.Empty(t, saClient.existing, "dry-run must not create a service account")
	require.Empty(t, aclClient.created, "dry-run must not create an acl")
	require.Empty(t, aclClient.existing, "dry-run must not mutate the target's existing acls")

	rendered := out.String()
	require.Contains(t, rendered, "service account for principal \"User:app1\"")
	require.Contains(t, rendered, "sa-(pending:app1)", "acl plan should preview the pending-create identity, proving cross-reconciler dry-run resolution")
}
