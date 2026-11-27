package migplan

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type fakeLinkCopy struct {
	c   reconcile.LinkCopy
	err error
}

func (f *fakeLinkCopy) LinkCopy(context.Context) (reconcile.LinkCopy, error) { return f.c, f.err }

// revGatewayConfig is a static route bound to bound, with domains cp
// (cp-plain) and cc (cc-sasl) declared and auth staged for both.
func revGatewayConfig(bound string, fenced bool) *reconcile.GatewayConfig {
	ids := map[string]string{"cp": "cp-plain", "cc": "cc-sasl"}
	staged := func(secret string) map[string]any {
		return map[string]any{"secretStore": "vault", "authentication": map[string]any{"sasl": map[string]any{"secretRef": secret}}}
	}
	route := map[string]any{
		"name":            "migration-route",
		"streamingDomain": map[string]any{"name": bound, "bootstrapServerId": ids[bound]},
		"security":        map[string]any{"cluster": map[string]any{"cp": staged("cp-secret"), "cc": staged("cc-secret")}},
	}
	if fenced {
		route["fence"] = map[string]any{"scope": "ALL", "errorCode": "BROKER_NOT_AVAILABLE"}
	}
	domain := func(name string) map[string]any {
		return map[string]any{"name": name, "kafkaCluster": map[string]any{"bootstrapServers": []any{map[string]any{"id": ids[name]}}}}
	}
	return &reconcile.GatewayConfig{
		Route:      &reconcile.RouteConfig{Name: "migration-route", Mode: reconcile.RouteModeStatic, BoundDomains: []string{bound}, Raw: route},
		RawObj:     map[string]any{"spec": map[string]any{"streamingDomains": []any{domain("cp"), domain("cc")}, "routes": []any{route}}},
		RawYAML:    "kind: Gateway\n",
		Generation: 7,
	}
}

func revInput(writableOn string) reconcile.ReversibleInput {
	return reconcile.ReversibleInput{
		Topics: []string{"a"}, Route: "migration-route", LinkName: "rev-link",
		SourceDomain: "cp", TargetDomain: "cc", WritableOn: writableOn,
		SourceClusterID: "src-id", TargetClusterID: "tgt-id",
	}
}

type revFakes struct {
	providers                  ReversibleProviders
	sourceGroups, targetGroups *fakeGroupLister
	secrets                    *fakeSecretChecker
}

func newRevFakes(gw *reconcile.GatewayConfig) revFakes {
	f := revFakes{
		sourceGroups: &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "g1", State: "Empty"}}, tracked: map[string][]string{"g1": {"a"}}},
		targetGroups: &fakeGroupLister{groups: []types.ConsumerGroupListing{{GroupID: "g1", State: "Stable", Type: "consumer"}}, tracked: map[string][]string{"never": {"read"}}},
		secrets:      &fakeSecretChecker{},
	}
	f.providers = ReversibleProviders{
		Gateway:      &fakeGateway{gw: gw},
		Source:       &fakeLister{topics: []string{"a"}, clusterID: "src-id"},
		Target:       &fakeLister{topics: []string{"a", "z"}, clusterID: "tgt-id"},
		SourceLink:   &fakeLinkCopy{c: reconcile.LinkCopy{Found: true, LinkID: "link-1", RemoteClusterID: "tgt-id"}},
		TargetLink:   &fakeLinkCopy{c: reconcile.LinkCopy{Found: true, LinkID: "link-1", RemoteClusterID: "src-id"}},
		Secrets:      f.secrets,
		SourceGroups: f.sourceGroups,
		TargetGroups: f.targetGroups,
	}
	return f
}

func TestGatherReversibleFacts_ReadsBothSides(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", false))
	f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	assert.Equal(t, []string{"a"}, f.SourceTopics)
	assert.Equal(t, []string{"a", "z"}, f.TargetTopics)
	assert.Equal(t, "src-id", f.LiveSourceClusterID)
	assert.Equal(t, "tgt-id", f.LiveTargetClusterID)
	assert.Equal(t, "tgt-id", f.SourceLink.RemoteClusterID)
	assert.Equal(t, "src-id", f.TargetLink.RemoteClusterID)
	assert.Same(t, fk.providers.Gateway.(*fakeGateway).gw, f.Gateway)
}

func TestGatherReversibleFacts_GroupsFromBoundAndWritableSides(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", false))
	f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	require.NotNil(t, f.Groups)
	assert.Equal(t, map[string][]string{"g1": {"a"}}, f.Groups.Committed, "commits come from the bound (source) side")
	assert.Equal(t, []string{"g1"}, fk.sourceGroups.trackedFor)
	assert.Equal(t, map[string]string{"g1": "Stable"}, f.Groups.EnteredStates, "states come from the side being entered (target)")
	assert.Equal(t, map[string]string{"g1": "consumer"}, f.Groups.EnteredTypes)
	assert.Zero(t, fk.targetGroups.trackedN, "the side being entered has no commits to read")
	assert.Empty(t, f.Groups.ListingIncomplete)
	assert.Empty(t, f.Groups.EnteredListingIncomplete)
}

// In a back-out the route stays where it is, so both roles are the writable
// side's and the other cluster's groups are never read.
func TestGatherReversibleFacts_BackOutReadsGroupsFromWritableSide(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", true))
	f, err := GatherReversibleFacts(context.Background(), revInput("cp"), fk.providers)
	require.NoError(t, err)
	require.NotNil(t, f.Groups)
	assert.Equal(t, map[string][]string{"g1": {"a"}}, f.Groups.Committed)
	assert.Equal(t, map[string]string{"g1": "Empty"}, f.Groups.EnteredStates)
	assert.Zero(t, fk.targetGroups.calls, "the mirror side's groups are not read in a back-out")
	assert.Zero(t, fk.targetGroups.trackedN)
}

func TestGatherReversibleFacts_NoGroupsWhenNotMoving(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cc", false))
	f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	assert.Nil(t, f.Groups)
	assert.Zero(t, fk.sourceGroups.calls+fk.targetGroups.calls+fk.sourceGroups.trackedN+fk.targetGroups.trackedN)
}

func TestGatherReversibleFacts_BoundListingGapSkipsCommittedTopics(t *testing.T) {
	for name, deny := range map[string]func(l *fakeGroupLister){
		"groups": func(l *fakeGroupLister) { l.denied = true },
		"topics": func(l *fakeGroupLister) { l.topicsDenied = true },
	} {
		t.Run(name, func(t *testing.T) {
			fk := newRevFakes(revGatewayConfig("cp", false))
			deny(fk.sourceGroups)
			f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
			require.NoError(t, err)
			assert.Contains(t, f.Groups.ListingIncomplete, "source credential cannot describe arbitrary")
			assert.Zero(t, fk.sourceGroups.trackedN, "a filtered credential's commits are not read")
		})
	}
}

func TestGatherReversibleFacts_EnteredListingGapReported(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", false))
	fk.targetGroups.denied = true
	f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	assert.Contains(t, f.Groups.EnteredListingIncomplete, "target credential cannot describe arbitrary consumer groups")
}

func TestGatherReversibleFacts_SecretsForBothDomains(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", false))
	fk.secrets.missing = []string{"cp-secret"}
	f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	assert.Equal(t, []string{"cc-secret", "cp-secret"}, fk.secrets.calledWith)
	assert.Equal(t, []string{"cp-secret"}, f.MissingSecrets)
}

func TestGatherReversibleFacts_NoSecretCheckerIsASkip(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", false))
	fk.providers.Secrets = nil
	f, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	assert.NotEmpty(t, f.SecretCheckSkipped, "an unchecked secret must not read as present")
}

func TestGatherReversibleFacts_ProviderErrorReturned(t *testing.T) {
	boom := errors.New("boom")
	for _, tc := range []struct {
		name, want string
		mutate     func(p *ReversibleProviders)
	}{
		{"gateway", "gateway", func(p *ReversibleProviders) { p.Gateway = &fakeGateway{err: boom} }},
		{"source topics", "source", func(p *ReversibleProviders) { p.Source = &fakeLister{err: boom} }},
		{"target topics", "target", func(p *ReversibleProviders) { p.Target = &fakeLister{err: boom} }},
		{"source copy", "source copy", func(p *ReversibleProviders) { p.SourceLink = &fakeLinkCopy{err: boom} }},
		{"target copy", "target copy", func(p *ReversibleProviders) { p.TargetLink = &fakeLinkCopy{err: boom} }},
		{"bound groups", "source", func(p *ReversibleProviders) { p.SourceGroups = &fakeGroupLister{err: boom} }},
		{"bound commits", "source", func(p *ReversibleProviders) { p.SourceGroups = &fakeGroupLister{trackedErr: boom} }},
		{"entered groups", "target", func(p *ReversibleProviders) { p.TargetGroups = &fakeGroupLister{err: boom} }},
		{"entered access", "target", func(p *ReversibleProviders) { p.TargetGroups = &fakeGroupLister{accessErr: boom} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fk := newRevFakes(revGatewayConfig("cp", false))
			tc.mutate(&fk.providers)
			_, err := GatherReversibleFacts(context.Background(), revInput("cc"), fk.providers)
			require.ErrorIs(t, err, boom)
			assert.Contains(t, err.Error(), tc.want)
		})
	}
}

func TestRunReversible_AttachesGatewayYAMLAndGeneration(t *testing.T) {
	fk := newRevFakes(revGatewayConfig("cp", false))
	plan, err := RunReversible(context.Background(), revInput("cc"), fk.providers)
	require.NoError(t, err)
	assert.Equal(t, reconcile.ModeReversibleStatic, plan.Mode)
	assert.Equal(t, "kind: Gateway\n", plan.GatewayYAML)
	assert.Equal(t, int64(7), plan.GatewayGeneration)
}
