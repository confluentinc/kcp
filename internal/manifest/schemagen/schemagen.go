// Package schemagen generates the migration.yaml and gateway-migration.yaml
// JSON Schemas from the manifest Go structs. It is used by `go generate` and
// the drift-guard tests only; no runtime or command package imports it.
package schemagen

import (
	"encoding/json"

	"github.com/confluentinc/kcp/internal/manifest"
	"github.com/google/jsonschema-go/jsonschema"
)

// durationPattern matches a Go duration string. time.Duration reflects as an
// integer, but goccy parses "10m", so without this override the parser and the
// schema would disagree and every editor honouring the yaml-language-server
// header would flag the documented example as invalid.
const durationPattern = `^[0-9]+(ns|us|ms|s|m|h)([0-9]+(ns|us|ms|s|m|h))*$`

// Generate reflects the Migration struct into a JSON Schema, injects the enums
// (from the manifest constants) and the intended required sets, and returns the
// indented JSON (newline-terminated). Output is deterministic.
func Generate() ([]byte, error) {
	s, err := jsonschema.For[manifest.Migration](nil)
	if err != nil {
		return nil, err
	}

	spec := s.Properties["spec"]
	source := spec.Properties["source"]
	target := spec.Properties["target"]
	topics := spec.Properties["topics"]
	clusterLink := spec.Properties["clusterLink"]

	source.Properties["type"].Enum = []any{manifest.SourceMSK, manifest.SourceApacheKafka, manifest.SourceConfluentPlatform}
	target.Properties["type"].Enum = []any{manifest.TargetConfluentCloud, manifest.TargetConfluentPlatform}
	topics.Properties["mode"].Enum = []any{manifest.TopicModeMirror, manifest.TopicModeNew}
	clusterLink.Properties["mode"].Enum = []any{manifest.ClusterLinkModeDestination, manifest.ClusterLinkModeSource}

	if cos, ok := clusterLink.Properties["consumerOffsetSync"]; ok && cos.Properties != nil {
		if gf, ok := cos.Properties["groupFilters"]; ok && gf.Items != nil && gf.Items.Properties != nil {
			gf.Items.Properties["patternType"].Enum = []any{manifest.PatternTypeLiteral, manifest.PatternTypePrefixed}
			gf.Items.Properties["filterType"].Enum = []any{manifest.FilterTypeInclude, manifest.FilterTypeExclude}
		}
	}

	// Every credentials slot is a CredentialsRef: a path to a credentials file.
	stringCredential(source.Properties["credentials"])
	stringCredential(target.Properties["clusterCredentials"])
	stringCredential(target.Properties["cloudCredentials"])
	for _, key := range []string{"source", "destination"} {
		if kc, ok := clusterLink.Properties[key]; ok && kc.Properties != nil {
			stringCredential(kc.Properties["credentials"])
		}
	}
	if sr, ok := clusterLink.Properties["sourceRest"]; ok && sr.Properties != nil {
		stringCredential(sr.Properties["credentials"])
	}

	// target.kafka.clusterCredentials lives on the shared TargetKafka for kind:
	// GatewayMigration only — kcp migrate authenticates the destination via
	// spec.target.clusterCredentials and never reads it. Validate() rejects it
	// here, so drop it from this schema too; otherwise an editor would offer a
	// field that does nothing in this kind.
	if kafka, ok := target.Properties["kafka"]; ok && kafka.Properties != nil {
		delete(kafka.Properties, "clusterCredentials")
	}

	return marshal(s)
}

// GenerateGateway reflects the GatewayMigration struct into its own JSON
// Schema. A second schema rather than a merged one: the two kinds share an
// apiVersion and sub-types but not a shape, and one schema covering both would
// validate every gateway field as unknown in a kcp migrate file, and vice versa.
func GenerateGateway() ([]byte, error) {
	s, err := jsonschema.For[manifest.GatewayMigration](nil)
	if err != nil {
		return nil, err
	}

	spec := s.Properties["spec"]
	source := spec.Properties["source"]
	target := spec.Properties["target"]
	gateway := spec.Properties["gateway"]
	policy := spec.Properties["defaultPolicies"]
	clusterLink := spec.Properties["clusterLink"]

	source.Properties["type"].Enum = []any{manifest.SourceMSK, manifest.SourceApacheKafka}
	target.Properties["type"].Enum = []any{manifest.TargetConfluentCloud, manifest.TargetConfluentPlatform}

	kafka := target.Properties["kafka"]
	stringCredential(source.Properties["credentials"])
	stringCredential(kafka.Properties["clusterCredentials"])
	stringCredential(clusterLink.Properties["linkCredentials"])
	// The reflected schema requires only restEndpoint (the one field without
	// omitempty), but Validate() also requires bootstrapServers and
	// clusterCredentials. Patch the schema to match so an editor/CI lint cannot
	// pass a manifest that init will then reject.
	kafka.Required = []string{"restEndpoint", "bootstrapServers", "clusterCredentials"}

	// lagThreshold's zero value is a legitimate, fail-safe setting (strictest:
	// zero lag before proceeding), so it is indistinguishable from "omitted" —
	// requiring the key forces an operator to choose it explicitly rather than
	// silently inherit that default by leaving it out.
	policy.Required = []string{"lagThreshold"}

	// gateway carries only namespace + cr-name; the retired crs/routes shape is
	// gone from the struct, so reflection no longer emits it.
	gateway.Required = []string{"namespace", "cr-name"}

	// spec.topicGroup: each entry pairs a topic selection with its route and
	// target streaming domain. The item's required set (route,
	// targetStreamingDomain) reflects from the non-omitempty struct tags; the "at
	// least one of topics/topicPatterns" rule is not expressible on the struct, so
	// hand-patch it as an anyOf on the item.
	topicGroup := spec.Properties["topicGroup"]
	tgItem := topicGroup.Items
	tgItem.AnyOf = []*jsonschema.Schema{
		{Required: []string{"topics"}},
		{Required: []string{"topicPatterns"}},
	}

	// Durations parse as "10m", not as an integer count.
	for _, k := range []string{"rolloutTimeout", "detectUnroutedProducersDuration", "consumerOffsetSyncDrainDuration", "hotReloadTimeout"} {
		p := policy.Properties[k]
		// time.Duration reflects as {"type":"integer"}; Type and Types are
		// mutually exclusive, so the reflected one must be replaced, not added to.
		p.Type = "string"
		p.Types = nil
		p.Pattern = durationPattern
	}

	// Retiring ~56 flags retires ~56 pieces of --help guidance, and --help is
	// the primary discovery surface for a CLI run from a bastion. Each ported
	// field carries its flag's usage text, reworded away from the
	// Confluent-Cloud-specific phrasing where "the destination" is meant.
	describe(map[*jsonschema.Schema]string{
		source.Properties["type"]:             "Source Kafka flavour. Gates authentication: iam is msk-only.",
		source.Properties["bootstrapServers"]: "Bootstrap server(s) of the source Kafka cluster (e.g. broker1:9092, broker2:9092).",
		source.Properties["credentials"]:      "Path to the source cluster credentials file. Exactly one authentication block must be present in that file.",

		target.Properties["type"]:      "Destination flavour: a Confluent Cloud or Confluent Platform cluster.",
		target.Properties["clusterId"]: "Destination cluster ID (e.g. lkc-abc123). Required for both destination types.",

		kafka.Properties["bootstrapServers"]:   "Destination Kafka bootstrap endpoint (e.g. pkc-abc123.us-east-1.aws.confluent.cloud:9092).",
		kafka.Properties["restEndpoint"]:       "REST endpoint of the destination cluster.",
		kafka.Properties["clusterCredentials"]: "Path to the destination Kafka credentials file, dialled directly to read destination-side offsets. Accepts sasl_plain, sasl_scram, mtls, unauthenticated_tls, or unauthenticated_plaintext — iam is rejected (the destination is Confluent Cloud/Platform, never MSK).",

		clusterLink.Properties["name"]:                    "Name of the cluster link on the destination cluster. The link must ALREADY EXIST.",
		clusterLink.Properties["bootstrapServers"]:        "Repeats spec.target.kafka.bootstrapServers for manifest self-documentation. Not validated against it.",
		clusterLink.Properties["linkCredentials"]:         "Path to the cluster-link REST credentials file (api_key/api_secret, basic, bearer, or mtls) that calls the destination Admin REST API to drive the link (status, list/promote mirror topics). Always required; it is never derived from the Kafka leg.",
		clusterLink.Properties["pauseConsumerOffsetSync"]: "Disable the cluster link's consumer.offset.sync.enable during execute and restore it after switchover. Requires the cluster link to currently have consumer.offset.sync.enable=true.",

		gateway.Properties["namespace"]:  "Kubernetes namespace where the gateway is deployed.",
		gateway.Properties["kubeconfig"]: "Path to the Kubernetes config file to use for the migration. A leading ~/ is expanded.",

		gateway.Properties["cr-name"]: "NAME of the initial gateway custom resource in Kubernetes. Read live from the cluster at init — this is an object name, not a file path.",

		topicGroup:                                 "Pairs the topic selection with the route it migrates and the streaming domain that route switches to. Exactly one entry today — one route, one migration per file. kcp reads the live initial CR, injects fence: {scope: ALL, errorCode: BROKER_NOT_AVAILABLE} onto the route, and applies the patched CR — there is no separate fenced-CR file. At cutover it derives the switch the same way, flipping the route's streamingDomain to its target. The route's migration mode (all-at-once vs topic-based) is read from the live CR's route binding, not declared here.",
		tgItem.Properties["topics"]:                "Topics to cut over, as a flat list of LITERAL names exact-matched against the cluster link's active mirror topics — not globs. At least one of topics or topicPatterns is required. If topics is set, it is authoritative and topicPatterns is ignored.",
		tgItem.Properties["topicPatterns"]:         "Topic selection as a list of anchored full-match regular expressions (RE2). Use ['.*'] to cut over every active mirror topic. At least one of topics or topicPatterns is required. Only consulted when topics is absent; only the match-all pattern is currently supported (union with topics is not yet implemented).",
		tgItem.Properties["route"]:                 "The spec.routes[].name of the route to fence and switch over. Must exist in the initial CR and must not already be fenced.",
		tgItem.Properties["targetStreamingDomain"]: "Name of a streaming domain already declared in the initial CR's spec.streamingDomains. kcp derives the bootstrap server id to bind the route to from that declaration in the live CR — it is not written here. Safe with no secret or auth change at cutover only because the route's security.cluster already carries pre-staged (\"redundant\") auth for this domain, which kcp proves at init.",

		policy.Properties["lagThreshold"]:                    "Total topic replication lag threshold (sum of all partition lags) before proceeding with the migration.",
		policy.Properties["promoteBatchSize"]:                "Maximum number of mirror topics to promote per batch. 0 (the default) promotes all topics at once. When set (>0), each batch is promoted and confirmed STOPPED before the next batch is submitted.",
		policy.Properties["rolloutTimeout"]:                  "Maximum time to wait for the Confluent operator to report the gateway as Ready during fence and switchover, as a duration (e.g. 10m). 0 (the default) means no deadline — the wait runs until the operator converges or the user cancels.",
		policy.Properties["detectUnroutedProducersDuration"]: "Time to monitor source offsets after fencing to detect producers still writing directly to the source cluster (bypassing the gateway); a detected increase aborts the migration before switchover. 0 (the default) skips the check; minimum 10s if set.",
		policy.Properties["consumerOffsetSyncDrainDuration"]: "How long to wait after fencing before disabling the cluster link's consumer.offset.sync.enable. The fence freezes source consumer offsets, so this drain lets the link propagate the final offsets to the destination, reducing (best-effort, not guaranteed) messages reprocessed after switchover. Has no effect unless pauseConsumerOffsetSync is set. 0 (the default) disables the wait.",
		policy.Properties["hotReloadTimeout"]:                "Maximum time to wait for every gateway pod to report the new config revision when the gateway supports hot-reload, as a duration (e.g. 90s). Unlike rolloutTimeout this is never unbounded: a hot-reload moves no Kubernetes signal, so 0 (the default) uses the built-in 90s budget rather than waiting forever.",
		policy.Properties["gatewayConfigPort"]:               "Port serving the gateway's /config endpoint, polled per pod to confirm a config revision was applied. 0 (the default) uses the persisted value, falling back to the gateway default (9180).",
	})

	return marshal(s)
}

// stringCredential rewrites a credentials property into a plain file-path
// string, preserving any existing description. Inline credential blocks are no
// longer accepted, so no credentials property is polymorphic and no $defs are
// emitted.
func stringCredential(p *jsonschema.Schema) {
	if p == nil {
		return
	}
	*p = jsonschema.Schema{Type: "string", Description: p.Description}
}

// describe applies descriptions after the credential rewrites, so a rewritten
// property keeps its text.
func describe(m map[*jsonschema.Schema]string) {
	for p, text := range m {
		if p != nil {
			p.Description = text
		}
	}
}

func marshal(s *jsonschema.Schema) ([]byte, error) {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}
