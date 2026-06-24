package manifest

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// linkConfigPrefixReadOnly is the derived link-config key users may reach for by
// mistake (the settable key is cluster.link.prefix via spec.clusterLink.prefix).
const linkConfigPrefixReadOnly = "link.prefix"

// blank reports whether s is empty or only whitespace.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

// validBootstrapServer reports whether s is a host:port (host + numeric 1-65535 port).
func validBootstrapServer(s string) bool {
	host, port, err := net.SplitHostPort(s)
	if err != nil || host == "" || port == "" {
		return false
	}
	n, err := strconv.Atoi(port)
	return err == nil && n > 0 && n <= 65535
}

func validateBootstrapServers(field string, servers []string) []error {
	if len(servers) == 0 {
		return []error{fmt.Errorf("%s: must not be empty", field)}
	}
	var errs []error
	for i, s := range servers {
		if !validBootstrapServer(s) {
			errs = append(errs, fmt.Errorf("%s[%d]: invalid bootstrap server %q (expected host:port)", field, i, s))
		}
	}
	return errs
}

// validateKafkaConn validates a {bootstrapServers, credentials} slot.
func validateKafkaConn(field string, c *KafkaConn) []error {
	if c == nil {
		return []error{fmt.Errorf("%s: required", field)}
	}
	errs := validateBootstrapServers(field+".bootstrapServers", c.BootstrapServers)
	if blank(c.Credentials) {
		errs = append(errs, fmt.Errorf("%s.credentials: must not be empty", field))
	}
	return errs
}

// validateEnum returns an error if value is empty or not one of allowed.
func validateEnum(field, value string, allowed ...string) error {
	if value == "" {
		return fmt.Errorf("%s: must not be empty", field)
	}
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("%s: unsupported value %q (supported: %s)", field, value, strings.Join(allowed, ", "))
}

// validateSelection checks an include list: it must be non-empty and contain no blank entries.
func validateSelection(field string, include []string) []error {
	if len(include) == 0 {
		return []error{fmt.Errorf("%s: must not be empty", field)}
	}
	var errs []error
	for i, p := range include {
		if blank(p) {
			errs = append(errs, fmt.Errorf("%s[%d]: must not be blank", field, i))
		}
	}
	return errs
}

// Validate performs structural (no-I/O) validation and returns ALL problems
// found, each tagged with its field path. An empty slice means valid.
func (m *Migration) Validate() []error {
	var errs []error
	add := func(format string, args ...any) {
		errs = append(errs, fmt.Errorf(format, args...))
	}

	if m.APIVersion != SupportedAPIVersion {
		add("apiVersion: must be %q, got %q", SupportedAPIVersion, m.APIVersion)
	}
	if m.Kind != KindMigration {
		add("kind: must be %q, got %q", KindMigration, m.Kind)
	}
	if blank(m.Metadata.Name) {
		add("metadata.name: must not be empty")
	}

	if err := validateEnum("spec.source.type", m.Spec.Source.Type, SourceApacheKafka, SourceConfluentPlatform); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateBootstrapServers("spec.source.bootstrapServers", m.Spec.Source.BootstrapServers)...)
	if blank(m.Spec.Source.Credentials) {
		add("spec.source.credentials: must not be empty")
	}

	switch m.Spec.Target.Type {
	case TargetConfluentCloud:
		if blank(m.Spec.Target.Cluster) {
			add("spec.target.cluster: required for target type %q", TargetConfluentCloud)
		}
		if m.Spec.Target.Kafka != nil {
			add("spec.target.kafka: not valid for target type %q", TargetConfluentCloud)
		}
		if m.Spec.Target.SchemaRegistry != nil {
			add("spec.target.schemaRegistry: not valid for target type %q", TargetConfluentCloud)
		}
		if m.Spec.Target.Connect != nil {
			add("spec.target.connect: not valid for target type %q", TargetConfluentCloud)
		}
	case TargetConfluentPlatform:
		if m.Spec.Target.Kafka == nil || blank(m.Spec.Target.Kafka.RestEndpoint) {
			add("spec.target.kafka.restEndpoint: required for target type %q", TargetConfluentPlatform)
		}
		if !blank(m.Spec.Target.Cluster) {
			add("spec.target.cluster: not valid for target type %q", TargetConfluentPlatform)
		}
	case "":
		add("spec.target.type: must not be empty")
	default:
		add("spec.target.type: unsupported value %q (supported: %s, %s)", m.Spec.Target.Type, TargetConfluentCloud, TargetConfluentPlatform)
	}
	if blank(m.Spec.Target.Credentials) {
		add("spec.target.credentials: must not be empty")
	}

	if t := m.Spec.Topics; t != nil {
		if err := validateEnum("spec.topics.mode", t.Mode, TopicModeMirror, TopicModeNew); err != nil {
			errs = append(errs, err)
		}
		if t.Mode == TopicModeMirror {
			if m.Spec.ClusterLink == nil || blank(m.Spec.ClusterLink.Name) {
				add("spec.clusterLink.name: required when spec.topics.mode is %q", TopicModeMirror)
			}
		}
		errs = append(errs, validateSelection("spec.topics.include", t.Include)...)
	}

	if cl := m.Spec.ClusterLink; cl != nil {
		if blank(cl.Name) {
			add("spec.clusterLink.name: must not be empty")
		}
		mode := cl.Mode
		if mode == "" {
			mode = ClusterLinkModeDestination
		}
		switch mode {
		case ClusterLinkModeDestination:
			errs = append(errs, validateKafkaConn("spec.clusterLink.source", cl.Source)...)
			if cl.SourceRest != nil {
				add("spec.clusterLink.sourceRest: not valid for mode %q", ClusterLinkModeDestination)
			}
			if cl.Destination != nil {
				add("spec.clusterLink.destination: not valid for mode %q", ClusterLinkModeDestination)
			}
		case ClusterLinkModeSource:
			if cl.SourceRest == nil {
				add("spec.clusterLink.sourceRest: required for mode %q", ClusterLinkModeSource)
			} else {
				if blank(cl.SourceRest.Endpoint) {
					add("spec.clusterLink.sourceRest.endpoint: must not be empty")
				}
				if blank(cl.SourceRest.Credentials) {
					add("spec.clusterLink.sourceRest.credentials: must not be empty")
				}
			}
			errs = append(errs, validateKafkaConn("spec.clusterLink.destination", cl.Destination)...)
			if cl.Source != nil {
				add("spec.clusterLink.source: not valid for mode %q", ClusterLinkModeSource)
			}
			if m.Spec.Source.Type == SourceApacheKafka {
				add("spec.clusterLink.mode: %q is not supported when spec.source.type is %q (only confluent-platform/confluent-cloud can initiate a link)", ClusterLinkModeSource, SourceApacheKafka)
			}
		case "bidirectional":
			add(`spec.clusterLink.mode: "bidirectional" is not supported (DR/active-active, not migration); use two unidirectional links`)
		default:
			add("spec.clusterLink.mode: unsupported value %q (supported: %s, %s)", cl.Mode, ClusterLinkModeDestination, ClusterLinkModeSource)
		}

		if cos := cl.ConsumerOffsetSync; cos != nil {
			if cos.IntervalMs < 0 {
				add("spec.clusterLink.consumerOffsetSync.intervalMs: must be >= 0 (0 = use server default)")
			}
			for i, f := range cos.GroupFilters {
				if blank(f.Name) {
					add("spec.clusterLink.consumerOffsetSync.groupFilters[%d].name: must not be blank", i)
				}
				if err := validateEnum(fmt.Sprintf("spec.clusterLink.consumerOffsetSync.groupFilters[%d].patternType", i), f.PatternType, PatternTypeLiteral, PatternTypePrefixed); err != nil {
					errs = append(errs, err)
				}
				if err := validateEnum(fmt.Sprintf("spec.clusterLink.consumerOffsetSync.groupFilters[%d].filterType", i), f.FilterType, FilterTypeInclude, FilterTypeExclude); err != nil {
					errs = append(errs, err)
				}
			}
		}
		if tcs := cl.TopicConfigSync; tcs != nil && tcs.IntervalMs < 0 {
			add("spec.clusterLink.topicConfigSync.intervalMs: must be >= 0 (0 = use server default)")
		}
		for k := range cl.Configs {
			if k == linkConfigPrefixReadOnly {
				add("spec.clusterLink.configs[%q]: read-only/derived key — set spec.clusterLink.prefix (cluster.link.prefix) instead, not configs[%q]", k, k)
				continue
			}
			for _, managed := range ManagedLinkConfigKeys {
				if k == managed {
					add("spec.clusterLink.configs[%q]: managed by a typed field — set the typed spec.clusterLink field, not configs[%q]", k, k)
					break
				}
			}
		}
	}

	if a := m.Spec.ACLs; a != nil {
		errs = append(errs, validateSelection("spec.acls.include", a.Include)...)
	}
	if s := m.Spec.Schemas; s != nil {
		errs = append(errs, validateSelection("spec.schemas.include", s.Include)...)
	}
	if c := m.Spec.Connectors; c != nil {
		errs = append(errs, validateSelection("spec.connectors.include", c.Include)...)
	}

	return errs
}
