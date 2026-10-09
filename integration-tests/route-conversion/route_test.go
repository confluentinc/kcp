package routeconversion

import (
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/require"
)

var testDomains = Domains{Source: "source-domain", SourceID: "SOURCE", Dest: "destination-domain", DestID: "DESTINATION"}

var testLinkTopics = []string{"rc-topic-002", "rc-topic-001"}

// liveCR is the gateway CR as setup.sh leaves it (route trimmed to what the
// checks read), read back the way the harness reads it: YAML.
const liveCR = `
apiVersion: platform.confluent.io/v1beta1
kind: Gateway
spec:
  routes:
    - name: other-route
      mode: static
    - name: rc-route
      endpoint: bootstrap.gw.local:9595
      mode: dynamic
      streamingDomain: {name: "", bootstrapServerId: ""}
      streamingDomains:
        - {name: source-domain, bootstrapServerId: SOURCE}
        - {name: destination-domain, bootstrapServerId: DESTINATION}
      rules:
        routing:
          coordination: {group: source-domain}
          conditions:
            - {streamingDomain: destination-domain, topics: [rc-topic-001, rc-topic-002]}
            - {streamingDomain: source-domain, topicPatterns: [".*"]}
        fencing: []
      security:
        client: {authentication: {type: none}, tls: {secretRef: client-tls}}
        cluster:
          source-domain: {auth: passthrough}
          destination-domain: {auth: swap, secretStore: file-store}
`

func liveRoute(t *testing.T) map[string]any {
	t.Helper()
	r := FindRoute([]byte(liveCR), "rc-route")
	require.NotNil(t, r)
	return r
}

func TestFindRoute_PicksTheNamedRoute(t *testing.T) {
	require.Equal(t, "bootstrap.gw.local:9595", liveRoute(t)["endpoint"])
	require.Nil(t, FindRoute([]byte(liveCR), "missing"))
	require.Nil(t, FindRoute([]byte("{not yaml"), "rc-route"))
}

func TestDynamicProblems_PostTBMRouteFromTheCRPasses(t *testing.T) {
	require.Empty(t, DynamicProblems(liveRoute(t), testDomains, testLinkTopics, false))
	require.NotEmpty(t, DynamicProblems(liveRoute(t), testDomains, testLinkTopics, true), "an unfenced route is not the fenced shape")
}

func TestDynamicProblems_FlagsAnExtraOrMissingLinkTopic(t *testing.T) {
	require.NotEmpty(t, DynamicProblems(liveRoute(t), testDomains, []string{"rc-topic-001"}, false))
	require.NotEmpty(t, DynamicProblems(liveRoute(t), testDomains, []string{"rc-topic-001", "rc-topic-002", "rc-topic-003"}, false))
}

func TestPostTBMRoute_RoundTripsThroughYAMLAndKeepsSecurity(t *testing.T) {
	live := liveRoute(t)
	built := PostTBMRoute(live, testDomains, []string{"rc-topic-002", "rc-topic-001", "rc-topic-new-1"})
	raw, err := yaml.Marshal(map[string]any{"spec": map[string]any{"routes": []any{built}}})
	require.NoError(t, err)
	back := FindRoute(raw, "rc-route")
	require.Empty(t, DynamicProblems(back, testDomains, []string{"rc-topic-001", "rc-topic-002", "rc-topic-new-1"}, false))
	require.True(t, jsonEqual(live["security"], back["security"]))
	require.Equal(t, "bootstrap.gw.local:9595", back["endpoint"])
	_, stillFenced := back["fence"]
	require.False(t, stillFenced)
}

func TestPostTBMRoute_RebuildsAConvertedRoute(t *testing.T) {
	converted := map[string]any{
		"name": "rc-route", "endpoint": "bootstrap.gw.local:9595", "mode": "static",
		"streamingDomain": map[string]any{"name": "destination-domain", "bootstrapServerId": "DESTINATION"},
		"security":        liveRoute(t)["security"],
	}
	require.Empty(t, ConvertedProblems(converted, testDomains, liveRoute(t)["security"]))
	rebuilt := PostTBMRoute(converted, testDomains, testLinkTopics)
	require.Empty(t, DynamicProblems(rebuilt, testDomains, testLinkTopics, false))
	require.False(t, StaticOn(rebuilt, "destination-domain", "DESTINATION"))
	require.NotEmpty(t, ConvertedProblems(rebuilt, testDomains, liveRoute(t)["security"]))
}

func TestWithConvertFence_IsTheFencedShapeKcpRecognises(t *testing.T) {
	fenced := WithConvertFence(liveRoute(t))
	require.True(t, HasConvertFence(fenced))
	require.Empty(t, DynamicProblems(fenced, testDomains, testLinkTopics, true))
	require.False(t, HasConvertFence(liveRoute(t)), "WithConvertFence must not mutate its input")
}

func TestHasConvertFence_IgnoresLookalikes(t *testing.T) {
	for name, entry := range map[string]map[string]any{
		"not blocked":    {"topicPatterns": []any{".*"}, "blocked": false},
		"other pattern":  {"topicPatterns": []any{"rc-.*"}, "blocked": true},
		"extra key":      {"topicPatterns": []any{".*"}, "blocked": true, "trafficType": "PRODUCE"},
		"literal topics": {"topics": []any{".*"}, "blocked": true},
	} {
		r := liveRoute(t)
		r["rules"].(map[string]any)["fencing"] = []any{entry}
		require.Falsef(t, HasConvertFence(r), "%s must not count as kcp's conversion fence", name)
		require.NotEmptyf(t, DynamicProblems(r, testDomains, testLinkTopics, false), "%s is a fence, so the route is not unfenced", name)
	}
}

func TestConvertedProblems_FlagsLeftoversAndChangedSecurity(t *testing.T) {
	sec := liveRoute(t)["security"]
	base := func() map[string]any {
		return map[string]any{
			"mode":            "static",
			"streamingDomain": map[string]any{"name": "destination-domain", "bootstrapServerId": "DESTINATION"},
			"security":        DeepCopy(map[string]any{"s": sec})["s"],
		}
	}
	require.Empty(t, ConvertedProblems(base(), testDomains, sec))

	withRules := base()
	withRules["rules"] = map[string]any{"fencing": []any{ConvertFence()}}
	require.NotEmpty(t, ConvertedProblems(withRules, testDomains, sec))

	onSource := base()
	onSource["streamingDomain"] = map[string]any{"name": "source-domain", "bootstrapServerId": "SOURCE"}
	require.NotEmpty(t, ConvertedProblems(onSource, testDomains, sec))

	require.NotEmpty(t, ConvertedProblems(base(), testDomains, map[string]any{"client": "changed"}))
	require.NotEmpty(t, ConvertedProblems(nil, testDomains, sec))
}
