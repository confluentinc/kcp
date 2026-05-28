package plan

import (
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

// authInputs builds a PlanInputsResolved with the default target_auth
// unset — DecideAuth falls back to the per-source default from
// auth_mapping. Tests that want an override-everywhere behavior set
// inputs.TargetAuthMethod explicitly.
func authInputs() types.PlanInputsResolved {
	return types.PlanInputsResolved{}
}

func TestDecideAuth_NoSourceAuthsDetected(t *testing.T) {
	c := types.ProcessedCluster{Name: "blank"}
	d := DecideAuth(c, defaultCfg(t), authInputs())
	assert.Equal(t, "blank", d.ClusterID)
	assert.Empty(t, d.SourceAuths, "no auth flags enabled → empty SourceAuths")
	assert.Empty(t, d.TargetMappings, "empty SourceAuths → no mapping rows")
}

func TestDecideAuth_SCRAMMapsToAPIKeysAndIsGatewayCompatible(t *testing.T) {
	c := withSourceAuth("scram-cluster", SourceAuthSCRAM)
	d := DecideAuth(c, defaultCfg(t), authInputs())
	assert.Equal(t, []string{SourceAuthSCRAM}, d.SourceAuths)
	require := requireRow(t, d, SourceAuthSCRAM)
	assert.Equal(t, TargetAuthAPIKeys, require.EffectiveTarget)
	assert.True(t, require.GatewayCompatible, "SCRAM is gateway-compatible (credential swap)")
	assert.True(t, require.TransparentSwap, "SCRAM → API Keys is a transparent swap")
}

func TestDecideAuth_IAMTargetsAPIKeysButGatewayIncompatible(t *testing.T) {
	c := withSourceAuth("iam-cluster", SourceAuthIAM)
	d := DecideAuth(c, defaultCfg(t), authInputs())
	row := requireRow(t, d, SourceAuthIAM)
	assert.Equal(t, TargetAuthAPIKeys, row.EffectiveTarget)
	assert.False(t, row.GatewayCompatible, "IAM clients cannot connect to the CC Gateway")
}

func TestDecideAuth_MTLSStaysMTLS(t *testing.T) {
	c := withSourceAuth("mtls-cluster", SourceAuthMTLS)
	d := DecideAuth(c, defaultCfg(t), authInputs())
	row := requireRow(t, d, SourceAuthMTLS)
	assert.Equal(t, TargetAuthMTLS, row.EffectiveTarget)
	assert.True(t, row.GatewayCompatible, "mTLS gateway path uses auth-swap mode")
	assert.False(t, row.TransparentSwap, "mTLS swap is not transparent — gateway re-issues certs")
}

// Customer override replaces the auth_mapping default. Verified end-to-end
// through PlanService.Build's per-cluster resolution in
// plan_service_test.go; here we exercise DecideAuth directly.
func TestDecideAuth_TargetAuthMethodOverrideWins(t *testing.T) {
	c := withSourceAuth("scram-cluster", SourceAuthSCRAM)
	inputs := authInputs()
	inputs.TargetAuthMethod = TargetAuthOAuth
	d := DecideAuth(c, defaultCfg(t), inputs)
	row := requireRow(t, d, SourceAuthSCRAM)
	assert.Equal(t, TargetAuthOAuth, row.EffectiveTarget, "customer override must beat the auth_mapping default")
}

// Multiple source auths on one cluster (e.g. SCRAM + mTLS both on) is
// a real MSK shape; the plan must surface ALL options rather than
// picking one.
func TestDecideAuth_MultipleSourceAuthsAllRendered(t *testing.T) {
	c := withSourceAuth("multi-cluster", SourceAuthSCRAM)
	enabled := true
	c.AWSClientInformation.MskClusterConfig.Provisioned.ClientAuthentication.Tls = &kafkatypes.Tls{Enabled: &enabled}
	d := DecideAuth(c, defaultCfg(t), authInputs())
	assert.Len(t, d.SourceAuths, 2, "both SCRAM and mTLS should appear")
	assert.Len(t, d.TargetMappings, 2, "one row per source auth — plan never picks one")
}

func requireRow(t *testing.T, d types.AuthDecision, sourceAuth string) types.AuthMappingRow {
	t.Helper()
	for _, row := range d.TargetMappings {
		if row.SourceAuth == sourceAuth {
			return row
		}
	}
	t.Fatalf("AuthDecision missing row for source auth %q; got: %+v", sourceAuth, d.TargetMappings)
	return types.AuthMappingRow{}
}
