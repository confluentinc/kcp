package idempotent_fsm_e2e

import (
	"bytes"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The fixtures below are trimmed from testdata/manifests/templates/gateway-*.yaml,
// with the fence/switchover edits in the shapes reconcile renders them
// (rules.go PrependFence/PrependCondition, staticartifacts.go).

const (
	testRoute = "migration-route"
	srcDomain = "source-domain"
	dstDomain = "destination-domain"
)

// crdDefaultedStreamingDomain is the empty singular streamingDomain CFK's Gateway
// CRD defaults onto every route when a CR is applied, dynamic routes included,
// so a dynamic route read back from the cluster always carries it.
const crdDefaultedStreamingDomain = `      streamingDomain:
        name: ""
        bootstrapServerId: ""
`

// dynamicCR wraps a dynamic route's `rules` body in a CR alongside a decoy route
// of the same shape, so a lookup that ignored the route name would be caught.
// The route is in the shape the cluster returns it: with the CRD-defaulted
// singular streamingDomain.
func dynamicCR(rules string) []byte {
	return []byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: tbm-gateway
spec:
  routes:
    - name: other-route
      mode: dynamic
      rules:
        routing:
          conditions:
            - topics: [t1]
              streamingDomain: destination-domain
        fencing:
          - topics: [t1]
            blocked: true
    - name: migration-route
      mode: dynamic
      streamingDomains:
        - name: source-domain
          bootstrapServerId: SOURCE
        - name: destination-domain
          bootstrapServerId: DESTINATION
` + crdDefaultedStreamingDomain + `      rules:
` + rules)
}

// withoutCRDDefault drops the CRD-defaulted singular streamingDomain from a
// dynamicCR, for a route as authored rather than as read back.
func withoutCRDDefault(cr []byte) []byte {
	return bytes.Replace(cr, []byte(crdDefaultedStreamingDomain), nil, 1)
}

// staticCR builds a CR whose static route is bound to domain, with extra
// route-level YAML (e.g. a fence block) appended.
func staticCR(domain, extra string) []byte {
	return []byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: static-gateway
spec:
  routes:
    - name: migration-route
      endpoint: static-gateway.confluent.svc.cluster.local:9595
      brokerIdentificationStrategy:
        type: port
      streamingDomain:
        name: ` + domain + `
        bootstrapServerId: UNAUTHED
` + extra)
}

const pristineRules = `        routing:
          coordination:
            group: source-domain
          conditions:
            - topicPatterns: [".*"]
              streamingDomain: source-domain
        fencing: []
`

func TestRouteFences(t *testing.T) {
	cases := []struct {
		name  string
		cr    []byte
		topic string
		want  bool
	}{
		{"dynamic pristine", dynamicCR(pristineRules), "t1", false},
		{"dynamic kcp fence names topic", dynamicCR(`        fencing:
          - topics: [t1, t2]
            blocked: true
`), "t2", true},
		{"dynamic fence names only other topics", dynamicCR(`        fencing:
          - topics: [t2, t3]
            blocked: true
`), "t1", false},
		{"dynamic unblocked entry is not a fence", dynamicCR(`        fencing:
          - topics: [t1]
            blocked: false
`), "t1", false},
		{"dynamic entry without blocked is not a fence", dynamicCR(`        fencing:
          - topics: [t1]
`), "t1", false},
		{"dynamic topic in a later fencing entry", dynamicCR(`        fencing:
          - topics: [operator-topic]
            blocked: true
          - topics: [t1]
            blocked: true
`), "t1", true},
		{"dynamic route with no rules", dynamicCR(""), "t1", false},
		{"dynamic fence, route without the CRD-defaulted streamingDomain", withoutCRDDefault(dynamicCR(`        fencing:
          - topics: [t1]
            blocked: true
`)), "t1", true},

		{"static pristine", staticCR(srcDomain, ""), "t1", false},
		{"static fenced (whole route)", staticCR(srcDomain, `      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
`), "any-topic", true},
		{"static switched, fence dropped", staticCR(dstDomain, ""), "t1", false},

		{"route absent", nil, "t1", false},
		{"unparseable CR", []byte("spec: [unterminated"), "t1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, routeFences(findRoute(tc.cr, testRoute), tc.topic))
		})
	}
}

// operatorProduceFence is an operator's produce-only fence over [t1, t2]: kcp's
// topics and blocked: true, plus a trafficType kcp never writes.
const operatorProduceFence = `          - trafficType: PRODUCE
            topics: [t1, t2]
            blocked: true
`

func TestHasKcpFence(t *testing.T) {
	cases := []struct {
		name string
		cr   []byte
		want bool
	}{
		{"kcp fence", dynamicCR("        fencing:\n          - topics: [t1, t2]\n            blocked: true\n"), true},
		{"kcp fence, topics in another order", dynamicCR("        fencing:\n          - topics: [t2, t1]\n            blocked: true\n"), true},
		{"kcp fence after the operator's", dynamicCR("        fencing:\n" + operatorProduceFence + "          - topics: [t1, t2]\n            blocked: true\n"), true},
		{"operator's produce-only fence on the same topics", dynamicCR("        fencing:\n" + operatorProduceFence), false},
		{"fence over a wider topic set", dynamicCR("        fencing:\n          - topics: [t1, t2, t3]\n            blocked: true\n"), false},
		{"fence over a narrower topic set", dynamicCR("        fencing:\n          - topics: [t1]\n            blocked: true\n"), false},
		{"unblocked entry", dynamicCR("        fencing:\n          - topics: [t1, t2]\n            blocked: false\n"), false},
		{"pristine", dynamicCR(pristineRules), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, hasKcpFence(findRoute(tc.cr, testRoute), []string{"t1", "t2"}))
		})
	}
}

func TestCountFencingEntry(t *testing.T) {
	op := map[string]any{"trafficType": "PRODUCE", "topics": []any{"t1", "t2"}, "blocked": true}
	cases := []struct {
		name string
		cr   []byte
		want int
	}{
		{"present once, beside kcp's fence", dynamicCR("        fencing:\n          - topics: [t1, t2]\n            blocked: true\n" + operatorProduceFence), 1},
		{"present twice", dynamicCR("        fencing:\n" + operatorProduceFence + operatorProduceFence), 2},
		{"only kcp's fence", dynamicCR("        fencing:\n          - topics: [t1, t2]\n            blocked: true\n"), 0},
		{"same topics, other traffic type", dynamicCR("        fencing:\n          - trafficType: CONSUME\n            topics: [t1, t2]\n            blocked: true\n"), 0},
		{"pristine", dynamicCR(pristineRules), 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, countFencingEntry(findRoute(tc.cr, testRoute), op))
		})
	}
}

func TestRouteTargets(t *testing.T) {
	cases := []struct {
		name  string
		cr    []byte
		topic string
		want  bool
	}{
		// The catch-all is a topicPatterns condition on the source domain.
		{"dynamic pristine catch-all", dynamicCR(pristineRules), "t1", false},
		{"dynamic switchover condition prepended", dynamicCR(`        routing:
          conditions:
            - topics: [t1, t2]
              streamingDomain: destination-domain
            - topicPatterns: [".*"]
              streamingDomain: source-domain
`), "t2", true},
		{"dynamic target condition for other topics", dynamicCR(`        routing:
          conditions:
            - topics: [t2]
              streamingDomain: destination-domain
            - topicPatterns: [".*"]
              streamingDomain: source-domain
`), "t1", false},
		{"dynamic topic bound to source domain", dynamicCR(`        routing:
          conditions:
            - topics: [t1]
              streamingDomain: source-domain
`), "t1", false},
		{"dynamic fenced but not switched", dynamicCR(`        routing:
          conditions:
            - topicPatterns: [".*"]
              streamingDomain: source-domain
        fencing:
          - topics: [t1]
            blocked: true
`), "t1", false},
		{"dynamic route with no rules", dynamicCR(""), "t1", false},
		{"dynamic switchover, route without the CRD-defaulted streamingDomain", withoutCRDDefault(dynamicCR(`        routing:
          conditions:
            - topics: [t1]
              streamingDomain: destination-domain
`)), "t1", true},

		{"static pristine on source", staticCR(srcDomain, ""), "t1", false},
		{"static fenced, still on source", staticCR(srcDomain, `      fence:
        scope: ALL
        errorCode: BROKER_NOT_AVAILABLE
`), "t1", false},
		{"static switched (whole route)", staticCR(dstDomain, ""), "any-topic", true},

		{"route absent", nil, "t1", false},
		{"unparseable CR", []byte("spec: [unterminated"), "t1", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, routeTargets(findRoute(tc.cr, testRoute), dstDomain, tc.topic))
		})
	}
}

func TestFindRoute(t *testing.T) {
	r := findRoute(dynamicCR(pristineRules), testRoute)
	require.NotNil(t, r)
	assert.Equal(t, testRoute, r["name"])

	assert.Nil(t, findRoute(dynamicCR(pristineRules), "no-such-route"))
	assert.Nil(t, findRoute([]byte("kind: Gateway\n"), testRoute), "CR without spec.routes")
}

func TestStripServerFields(t *testing.T) {
	raw := []byte(`apiVersion: platform.confluent.io/v1beta1
kind: Gateway
metadata:
  name: tbm-gateway
  namespace: confluent
  labels:
    app: gw
  managedFields:
    - manager: kubectl
  resourceVersion: "12345"
  uid: 0b0c-uid
  creationTimestamp: "2026-09-23T10:00:00Z"
  generation: 7
  selfLink: /apis/platform.confluent.io/v1beta1/gateways/tbm-gateway
spec:
  configId: tbm-initial
status:
  phase: RUNNING
`)
	var got map[string]any
	require.NoError(t, yaml.Unmarshal(stripServerFields(t, raw), &got))

	assert.NotContains(t, got, "status")
	md, _ := got["metadata"].(map[string]any)
	for _, k := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "selfLink"} {
		assert.NotContains(t, md, k)
	}
	// Everything the operator authored survives.
	assert.Equal(t, "tbm-gateway", md["name"])
	assert.Equal(t, "confluent", md["namespace"])
	assert.Equal(t, map[string]any{"app": "gw"}, md["labels"])
	assert.Equal(t, map[string]any{"configId": "tbm-initial"}, got["spec"])
	assert.Equal(t, "Gateway", got["kind"])
}
