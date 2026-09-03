package gateway

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// crWithDomains is a minimal initial gateway CR: one declared streaming domain
// (single-homed) and one static route (singular streamingDomain binding).
const crWithDomains = `apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: gateway-initial
spec:
  streamingDomains:
    - name: confluent-cloud
      kafkaCluster:
        bootstrapServers:
          - id: SASL_PLAIN
  routes:
    - name: migration-route
      streamingDomain:
        name: source-domain
        bootstrapServerId: SOURCE_ID
`

func TestDeriveBootstrapServerID_SingleHomed(t *testing.T) {
	id, err := DeriveBootstrapServerID([]byte(crWithDomains), "confluent-cloud")
	require.NoError(t, err)
	assert.Equal(t, "SASL_PLAIN", id)
}

// TestDeriveBootstrapServerID_MultiHomed is D1a: a domain declaring more than
// one bootstrap server id is a hard error naming the domain and its ids, since
// this manifest shape has no disambiguator.
func TestDeriveBootstrapServerID_MultiHomed(t *testing.T) {
	cr := strings.Replace(crWithDomains,
		"        bootstrapServers:\n          - id: SASL_PLAIN\n",
		"        bootstrapServers:\n          - id: SASL_PLAIN\n          - id: SASL_SCRAM\n", 1)
	_, err := DeriveBootstrapServerID([]byte(cr), "confluent-cloud")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confluent-cloud")
	assert.Contains(t, err.Error(), "SASL_PLAIN")
	assert.Contains(t, err.Error(), "SASL_SCRAM")
}

// TestDeriveBootstrapServerID_DomainAbsent is the D1 zero case: a
// targetStreamingDomain the CR does not declare (a typo) is a hard error.
func TestDeriveBootstrapServerID_DomainAbsent(t *testing.T) {
	_, err := DeriveBootstrapServerID([]byte(crWithDomains), "typo-domain")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "typo-domain")
}

// TestDeriveBootstrapServerID_DomainDeclaredWithNoIDs — declared but with an
// empty bootstrapServers set collapses to the same zero case (D1).
func TestDeriveBootstrapServerID_DomainDeclaredWithNoIDs(t *testing.T) {
	cr := strings.Replace(crWithDomains,
		"      kafkaCluster:\n        bootstrapServers:\n          - id: SASL_PLAIN\n",
		"      kafkaCluster:\n        bootstrapServers: []\n", 1)
	_, err := DeriveBootstrapServerID([]byte(cr), "confluent-cloud")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "confluent-cloud")
}

func TestResolveRouteMode_Singular(t *testing.T) {
	mode, err := ResolveRouteMode([]byte(crWithDomains), "migration-route")
	require.NoError(t, err)
	assert.Equal(t, RouteModeStatic, mode)
}

func TestResolveRouteMode_Plural(t *testing.T) {
	cr := strings.Replace(crWithDomains,
		"      streamingDomain:\n        name: source-domain\n        bootstrapServerId: SOURCE_ID\n",
		"      streamingDomains:\n        - name: source-domain\n          bootstrapServerId: SOURCE_ID\n", 1)
	mode, err := ResolveRouteMode([]byte(cr), "migration-route")
	require.NoError(t, err)
	assert.Equal(t, RouteModeDynamic, mode)
}

// TestResolveRouteMode_RouteAbsent — a manifest route missing from the CR is an
// error, never a silent fall-through to static.
func TestResolveRouteMode_RouteAbsent(t *testing.T) {
	_, err := ResolveRouteMode([]byte(crWithDomains), "no-such-route")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "no-such-route")
}

// TestResolveRouteMode_Neither — a hand-edited CR with a route binding neither
// singular nor plural cannot resolve a mode (D5).
func TestResolveRouteMode_Neither(t *testing.T) {
	cr := strings.Replace(crWithDomains,
		"    - name: migration-route\n      streamingDomain:\n        name: source-domain\n        bootstrapServerId: SOURCE_ID\n",
		"    - name: migration-route\n", 1)
	_, err := ResolveRouteMode([]byte(cr), "migration-route")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-route")
}

// TestResolveRouteMode_Both — a partial/hand-edited CR declaring both bindings
// is ambiguous (the CRD's CEL XOR would normally forbid it) and is an error.
func TestResolveRouteMode_Both(t *testing.T) {
	cr := strings.Replace(crWithDomains,
		"      streamingDomain:\n        name: source-domain\n        bootstrapServerId: SOURCE_ID\n",
		"      streamingDomain:\n        name: source-domain\n        bootstrapServerId: SOURCE_ID\n      streamingDomains:\n        - name: other\n          bootstrapServerId: X\n", 1)
	_, err := ResolveRouteMode([]byte(cr), "migration-route")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "migration-route")
}
