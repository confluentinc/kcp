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
// validate every gateway field as unknown in a kcp migration resources file, and vice versa.
func GenerateGateway() ([]byte, error) {
	s, err := jsonschema.For[manifest.GatewayMigration](nil)
	if err != nil {
		return nil, err
	}

	spec := s.Properties["spec"]
	source := spec.Properties["source"]
	target := spec.Properties["target"]
	gateway := spec.Properties["gateway"]
	clusterLink := spec.Properties["clusterLink"]
	static := spec.Properties[manifest.SectionStaticMigration]
	dynamic := spec.Properties[manifest.SectionDynamicMigration]
	conv := spec.Properties[manifest.SectionDynamicToStaticConversion]

	source.Properties["type"].Enum = []any{manifest.SourceMSK, manifest.SourceApacheKafka}
	target.Properties["type"].Enum = []any{manifest.TargetConfluentCloud, manifest.TargetConfluentPlatform}

	kafka := target.Properties["kafka"]
	stringCredential(source.Properties["credentials"])
	stringCredential(kafka.Properties["clusterCredentials"])
	stringCredential(clusterLink.Properties["linkCredentials"])
	// The reflected schema requires only restEndpoint (the one field without
	// omitempty), but Validate() also requires bootstrapServers and
	// clusterCredentials. Patch the schema to match so an editor/CI lint cannot
	// pass a manifest that execute will then reject.
	kafka.Required = []string{"restEndpoint", "bootstrapServers", "clusterCredentials"}

	// gateway requires namespace and cr-name; kubeconfig is optional.
	gateway.Required = []string{"namespace", "cr-name"}

	// The migration type is the section present: exactly one of the three.
	spec.OneOf = []*jsonschema.Schema{
		{Required: []string{manifest.SectionStaticMigration}},
		{Required: []string{manifest.SectionDynamicMigration}},
		{Required: []string{manifest.SectionDynamicToStaticConversion}},
	}

	// Pointer fields reflect as nullable; a section is an object (absent, not
	// null, is how the other two are "not declared").
	for _, sec := range []*jsonschema.Schema{static, dynamic, conv} {
		sec.Type = "object"
		sec.Types = nil
		route := sec.Properties["route"]
		route.Required = []string{"name", "targetStreamingDomain"}
	}
	static.Required = []string{"route", "topicGroup"}
	dynamic.Required = []string{"route", "topicGroup"}
	conv.Required = []string{"route"}

	// The item's required set is empty by reflection (both fields are
	// omitempty); the "at least one of topics/topicPatterns" rule is not
	// expressible on the struct, so hand-patch it as an anyOf on the item.
	var tgItems []*jsonschema.Schema
	for _, sec := range []*jsonschema.Schema{static, dynamic} {
		item := sec.Properties["topicGroup"].Items
		item.AnyOf = []*jsonschema.Schema{
			{Required: []string{"topics"}},
			{Required: []string{"topicPatterns"}},
		}
		tgItems = append(tgItems, item)
	}

	cos := static.Properties["consumerOffsetSync"]
	cos.Properties["baseline"].Enum = []any{manifest.OffsetSyncBaselineEnabled, manifest.OffsetSyncBaselineDisabled}

	// lagThreshold's zero value is a legitimate, fail-safe setting (strictest:
	// zero lag before proceeding), so it is indistinguishable from "omitted" —
	// requiring the key forces an operator to choose it explicitly rather than
	// silently inherit that default by leaving it out.
	staticPolicy := static.Properties["policies"]
	dynamicPolicy := dynamic.Properties["policies"]
	convPolicy := conv.Properties["policies"]
	staticPolicy.Required = []string{"lagThreshold"}
	dynamicPolicy.Required = []string{"lagThreshold"}

	// Durations parse as "10m", not as an integer count.
	for _, policy := range []*jsonschema.Schema{staticPolicy, dynamicPolicy, convPolicy} {
		for _, k := range []string{"rolloutTimeout", "detectUnroutedProducersDuration", "consumerOffsetSyncDrainDuration", "hotReloadTimeout", "detectUnroutedCommitsDuration"} {
			p, ok := policy.Properties[k]
			if !ok {
				continue
			}
			// time.Duration reflects as {"type":"integer"}; Type and Types are
			// mutually exclusive, so the reflected one must be replaced, not added to.
			p.Type = "string"
			p.Types = nil
			p.Pattern = durationPattern
		}
	}

	d := map[*jsonschema.Schema]string{
		source.Properties["type"]:             "Source Kafka flavour. Gates authentication: iam is msk-only.",
		source.Properties["bootstrapServers"]: "Bootstrap server(s) of the source Kafka cluster (e.g. broker1:9092, broker2:9092).",
		source.Properties["credentials"]:      "Path to the source cluster credentials file. Exactly one authentication block must be present in that file.",

		target.Properties["type"]:      "Destination flavour: a Confluent Cloud or Confluent Platform cluster.",
		target.Properties["clusterId"]: "Destination cluster ID (e.g. lkc-abc123). Required for both destination types.",

		kafka.Properties["bootstrapServers"]:   "Destination Kafka bootstrap endpoint (e.g. pkc-abc123.us-east-1.aws.confluent.cloud:9092).",
		kafka.Properties["restEndpoint"]:       "REST endpoint of the destination cluster.",
		kafka.Properties["clusterCredentials"]: "Path to the destination Kafka credentials file, dialled directly to read destination-side offsets. Accepts sasl_plain, sasl_scram, mtls, unauthenticated_tls, or unauthenticated_plaintext — iam is rejected (the destination is Confluent Cloud/Platform, never MSK).",

		clusterLink.Properties["name"]:             "Name of the cluster link on the destination cluster. The link must ALREADY EXIST.",
		clusterLink.Properties["bootstrapServers"]: "Repeats spec.target.kafka.bootstrapServers for manifest self-documentation. Not validated against it.",
		clusterLink.Properties["linkCredentials"]:  "Path to the cluster-link REST credentials file (api_key/api_secret, basic, bearer, or mtls) that calls the destination Admin REST API to drive the link (status, list/promote mirror topics). Always required; it is never derived from the Kafka leg.",

		gateway.Properties["namespace"]:  "Kubernetes namespace where the gateway is deployed.",
		gateway.Properties["kubeconfig"]: "Path to the Kubernetes config file to use for the migration. A leading ~/ is expanded.",
		gateway.Properties["cr-name"]:    "NAME of the initial gateway custom resource in Kubernetes. Read live from the cluster on every run — this is an object name, not a file path.",

		static:  "Declares a static (all-at-once) migration: every selected topic cuts over together across a static route. The live route must be static. Exactly one of staticMigration, dynamicMigration or dynamicToStaticConversion is required.",
		dynamic: "Declares a dynamic (topic-based) migration: topics cut over incrementally across a dynamic route, promoting mirror topics at zero lag. The live route must be dynamic. Exactly one of staticMigration, dynamicMigration or dynamicToStaticConversion is required.",
		conv:    "Declares a dynamic-to-static route conversion: after every topic on the cluster link has been migrated, convert the dynamic route to a static route bound to route.targetStreamingDomain, carrying consumer-group offsets across. The cluster link is the scope, with no topic selection: every mirror topic on the link must be promoted and routed to the target, and every source consumer group must have committed only on link topics, or the run is refused. Exactly one of staticMigration, dynamicMigration or dynamicToStaticConversion is required.",
	}
	for _, sec := range []*jsonschema.Schema{static, dynamic, conv} {
		route := sec.Properties["route"]
		d[route] = "Names the route on the live Gateway CR to act on and the streaming domain it ends up bound to."
		d[route.Properties["name"]] = "The spec.routes[].name of the route to fence and switch over. Must exist in the initial CR. On a static route it must carry no fence other than kcp's own."
		d[route.Properties["targetStreamingDomain"]] = "Name of a streaming domain already declared in the initial CR's spec.streamingDomains. kcp derives the bootstrap server id to bind the route to from that declaration in the live CR — it is not written here. Safe with no secret or auth change at cutover only because the route's security.cluster already carries pre-staged (\"redundant\") auth for this domain, which kcp checks on a static route on every run."
	}
	for _, sec := range []*jsonschema.Schema{static, dynamic} {
		topicGroup := sec.Properties["topicGroup"]
		d[topicGroup] = "The topic selection(s) that migrate on this route. Exactly one entry today — one route, one migration per file. kcp reads the live initial CR, injects fence: {scope: ALL, errorCode: BROKER_NOT_AVAILABLE} onto the route, and applies the patched CR — there is no separate fenced-CR file. At cutover it derives the switch the same way, flipping the route's streamingDomain to its target."
	}
	for _, item := range tgItems {
		d[item.Properties["topics"]] = "Topics to cut over, as a flat list of LITERAL names — not globs. Combined with topicPatterns: the union of the two is migrated. At least one of topics or topicPatterns is required. Each selected topic must exist on the source and be a mirror topic on the cluster link, or the run is refused."
		d[item.Properties["topicPatterns"]] = "Topic selection as a list of anchored full-match regular expressions (RE2), matched against the topic names on the source cluster. Combined with topics: the union of the two is migrated. Use ['.*'] to select every source topic. At least one of topics or topicPatterns is required. Each selected topic must be a mirror topic on the cluster link, or the run is refused."
	}
	d[cos] = "Pause the cluster link's consumer.offset.sync.enable around the cutover: disabled right after the fence, restored after the switch. Requires the link to currently have consumer.offset.sync.enable=true unless baseline says otherwise."
	d[cos.Properties["pause"]] = "Disable the cluster link's consumer.offset.sync.enable during execute and restore it after switchover."
	d[cos.Properties["baseline"]] = "The state of the cluster link's consumer.offset.sync.enable BEFORE this migration started (\"enabled\" or \"disabled\"). Required when pause is set; kcp restores sync to this value after the migration completes or fails."

	for _, sec := range []*jsonschema.Schema{static, dynamic, conv} {
		policy := sec.Properties["policies"]
		d[policy] = "Execute-time defaults, read fresh on every execute. Each can be overridden for a single run by the matching `kcp migration cutover execute` flag."
		set := func(name, text string) {
			if p, ok := policy.Properties[name]; ok {
				d[p] = text
			}
		}
		set("lagThreshold", "Total topic replication lag threshold (sum of all partition lags) before proceeding with the migration.")
		set("promoteBatchSize", "Maximum number of mirror topics to promote per batch. 0 (the default) promotes all topics at once. When set (>0), each batch is promoted and confirmed STOPPED before the next batch is submitted.")
		set("rolloutTimeout", "Maximum time to wait for the Confluent operator to report the gateway as Ready during fence and switchover, as a duration (e.g. 10m). 0 (the default) means no deadline — the wait runs until the operator converges or the user cancels.")
		set("detectUnroutedProducersDuration", "Time to monitor source offsets after fencing to detect producers still writing directly to the source cluster (bypassing the gateway); a detected increase aborts the migration before switchover. 0 (the default) skips the check; minimum 10s if set.")
		set("consumerOffsetSyncDrainDuration", "How long to wait after fencing before disabling the cluster link's consumer.offset.sync.enable. The fence freezes source consumer offsets, so this drain lets the link propagate the final offsets to the destination, reducing (best-effort, not guaranteed) messages reprocessed after switchover. Has no effect unless consumerOffsetSync.pause is set. 0 (the default) disables the wait.")
		set("hotReloadTimeout", "Maximum time to wait for every gateway pod to report the new config revision when the gateway supports hot-reload, as a duration (e.g. 90s). Unlike rolloutTimeout this is never unbounded: a hot-reload moves no Kubernetes signal, so 0 (the default) uses the built-in 90s budget rather than waiting forever.")
		set("gatewayConfigPort", "Port serving the gateway's /config endpoint, polled per pod to confirm a config revision was applied. 0 (the default) uses the gateway default (9180).")
		set("detectUnroutedCommitsDuration", "Time between the two committed-offset snapshots taken after fencing, to detect a consumer committing to the source directly (bypassing the gateway), as a duration (e.g. 30s). 0 (the default) uses 30s; this check cannot be skipped. When set the minimum is 10s.")
		set("offsetSyncConcurrency", "Number of workers, each with its own broker connection, reading and writing consumer-group offsets. 0 (the default) uses 8.")
	}
	describe(d)

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
