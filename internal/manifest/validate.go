package manifest

import (
	"fmt"
	"strings"
)

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
	if strings.TrimSpace(m.Metadata.Name) == "" {
		add("metadata.name: must not be empty")
	}

	switch m.Spec.Source.Type {
	case SourceApacheKafka:
		// ok
	case "":
		add("spec.source.type: must not be empty")
	default:
		add("spec.source.type: unsupported value %q (supported: %s)", m.Spec.Source.Type, SourceApacheKafka)
	}
	if strings.TrimSpace(m.Spec.Source.Credentials) == "" {
		add("spec.source.credentials: must not be empty")
	}

	switch m.Spec.Target.Type {
	case TargetConfluentCloud:
		if strings.TrimSpace(m.Spec.Target.Cluster) == "" {
			add("spec.target.cluster: required for target type %q", TargetConfluentCloud)
		}
	case TargetConfluentPlatform:
		if m.Spec.Target.Kafka == nil || strings.TrimSpace(m.Spec.Target.Kafka.RestEndpoint) == "" {
			add("spec.target.kafka.restEndpoint: required for target type %q", TargetConfluentPlatform)
		}
	case "":
		add("spec.target.type: must not be empty")
	default:
		add("spec.target.type: unsupported value %q (supported: %s, %s)", m.Spec.Target.Type, TargetConfluentCloud, TargetConfluentPlatform)
	}
	if strings.TrimSpace(m.Spec.Target.Credentials) == "" {
		add("spec.target.credentials: must not be empty")
	}

	return errs
}
