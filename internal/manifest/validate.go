package manifest

import (
	"fmt"
	"strings"
)

// blank reports whether s is empty or only whitespace.
func blank(s string) bool { return strings.TrimSpace(s) == "" }

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
			if blank(cl.SourceCredentials) {
				add("spec.clusterLink.sourceCredentials: required for mode %q", ClusterLinkModeDestination)
			}
			if cl.SourceRest != nil {
				add("spec.clusterLink.sourceRest: not valid for mode %q", ClusterLinkModeDestination)
			}
			if !blank(cl.DestinationCredentials) {
				add("spec.clusterLink.destinationCredentials: not valid for mode %q", ClusterLinkModeDestination)
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
			if blank(cl.DestinationCredentials) {
				add("spec.clusterLink.destinationCredentials: required for mode %q", ClusterLinkModeSource)
			}
			if !blank(cl.SourceCredentials) {
				add("spec.clusterLink.sourceCredentials: not valid for mode %q", ClusterLinkModeSource)
			}
			if m.Spec.Source.Type == SourceApacheKafka {
				add("spec.clusterLink.mode: %q is not supported when spec.source.type is %q (only confluent-platform/confluent-cloud can initiate a link)", ClusterLinkModeSource, SourceApacheKafka)
			}
		case "bidirectional":
			add(`spec.clusterLink.mode: "bidirectional" is not supported (DR/active-active, not migration); use two unidirectional links`)
		default:
			add("spec.clusterLink.mode: unsupported value %q (supported: %s, %s)", cl.Mode, ClusterLinkModeDestination, ClusterLinkModeSource)
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
