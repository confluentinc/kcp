package manifest

import (
	"fmt"
	"net"
	"path"
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
	if blankRef(c.Credentials) {
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

// isMSKClusterArn reports whether s is a well-formed MSK cluster ARN
// (arn:aws:kafka:<region>:<account>:cluster/<name>/<uuid>) with every field —
// region, account, name, uuid — actually populated.
//
// This mirrors the parsing arnClusterIdentity
// (internal/migrate/acls/iam_translate.go) relies on to scope IAM-derived ACL
// grants to a cluster: a plain prefix+substring check (the previous
// implementation) accepts shapes like "arn:aws:kafka::cluster/x" — missing
// region/account — that superficially look right but that arnClusterIdentity
// can't parse a real cluster identity out of either. Validating such an ARN
// as OK would let a manifest reach execute time with a clusterArn that silently
// scopes nothing (Finding 2(a) / task-7).
func isMSKClusterArn(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "aws" || parts[2] != "kafka" {
		return false
	}
	region, account, resource := parts[3], parts[4], parts[5]
	if region == "" || account == "" {
		return false
	}
	restype, rest, ok := strings.Cut(resource, "/")
	if !ok || restype != "cluster" {
		return false
	}
	name, uuid, ok := strings.Cut(rest, "/")
	return ok && name != "" && uuid != ""
}

// isIAMPrincipalArn reports whether s is a well-formed IAM role or user ARN
// (arn:aws:iam::<account>:role/<name> or arn:aws:iam::<account>:user/<name>)
// with a non-empty account and a non-empty trailing name.
//
// Tightened for the same reason as isMSKClusterArn (Finding 2(a) / task-7): a
// plain prefix+substring check accepts shapes like "arn:aws:iam:::role/" —
// empty account, empty name — from which principalFromArn
// (internal/migrate/acls/iam_translate.go) would derive a bogus "User:"
// (empty) Kafka principal. name may itself contain further "/"-segments (a
// path-bearing role ARN, e.g. "role/team/AppRole") — only the account and the
// immediate restype/name split are checked.
func isIAMPrincipalArn(s string) bool {
	parts := strings.Split(s, ":")
	if len(parts) != 6 || parts[0] != "arn" || parts[1] != "aws" || parts[2] != "iam" || parts[3] != "" {
		return false
	}
	account, resource := parts[4], parts[5]
	if account == "" {
		return false
	}
	restype, name, ok := strings.Cut(resource, "/")
	if !ok || name == "" {
		return false
	}
	return restype == "role" || restype == "user"
}

// validateGlobs checks each pattern compiles as a path.Match glob.
func validateGlobs(field string, patterns []string) []error {
	var errs []error
	for i, p := range patterns {
		if _, err := path.Match(p, ""); err != nil {
			errs = append(errs, fmt.Errorf("%s[%d]: invalid pattern %q: %v", field, i, p, err))
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

	if err := validateEnum("spec.source.type", m.Spec.Source.Type, SourceMSK, SourceApacheKafka, SourceConfluentPlatform); err != nil {
		errs = append(errs, err)
	}
	errs = append(errs, validateBootstrapServers("spec.source.bootstrapServers", m.Spec.Source.BootstrapServers)...)
	if blankRef(m.Spec.Source.Credentials) {
		add("spec.source.credentials: must not be empty")
	}

	switch m.Spec.Target.Type {
	case TargetConfluentCloud:
		if blank(m.Spec.Target.ClusterID) {
			add("spec.target.clusterId: required for target type %q", TargetConfluentCloud)
		}
		if m.Spec.Target.Kafka == nil || blank(m.Spec.Target.Kafka.RestEndpoint) {
			add("spec.target.kafka.restEndpoint: required for target type %q", TargetConfluentCloud)
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
		if !blank(m.Spec.Target.ClusterID) {
			add("spec.target.clusterId: not valid for target type %q", TargetConfluentPlatform)
		}
	case "":
		add("spec.target.type: must not be empty")
	default:
		add("spec.target.type: unsupported value %q (supported: %s, %s)", m.Spec.Target.Type, TargetConfluentCloud, TargetConfluentPlatform)
	}
	if blankRef(m.Spec.Target.ClusterCredentials) {
		add("spec.target.clusterCredentials: must not be empty")
	}
	// cloudCredentials is the CC Cloud/Global API key (distinct from the Kafka
	// cluster key). It is confluent-cloud-only, and required whenever spec.acls
	// is present on a confluent-cloud target: the acls reconciler uses it to map
	// Confluent Cloud's numeric read-back principals back to "sa-" ids so
	// re-apply stays idempotent — needed whether service accounts are
	// auto-created or mapped to pre-existing ids (serviceAccounts.autoCreate is a
	// subset of this, as auto-create also calls the IAM v2 API).
	if !blankRef(m.Spec.Target.CloudCredentials) && m.Spec.Target.Type != TargetConfluentCloud {
		add("spec.target.cloudCredentials: only valid when spec.target.type is %q", TargetConfluentCloud)
	}
	if m.Spec.ACLs != nil && m.Spec.Target.Type == TargetConfluentCloud && blankRef(m.Spec.Target.CloudCredentials) {
		add("spec.target.cloudCredentials: required for spec.acls on a confluent-cloud target (used to reconcile Confluent Cloud's numeric ACL principals for idempotency)")
	}
	// serviceAccounts.autoCreate provisions accounts via the IAM v2 API, which
	// also needs the Cloud/Global key — required even with no spec.acls (the
	// acls-based rule above only covers the case where acls happens to be
	// present too).
	if m.Spec.ServiceAccounts != nil && m.Spec.ServiceAccounts.AutoCreate && m.Spec.Target.Type == TargetConfluentCloud && blankRef(m.Spec.Target.CloudCredentials) {
		add("spec.target.cloudCredentials: required for spec.serviceAccounts.autoCreate on a confluent-cloud target (used to provision accounts via IAM v2)")
	}
	// spec.target.kafka.credentials / restCredentials exist on the shared
	// TargetKafka for kind: GatewayMigration only. kcp migrate authenticates the
	// destination via spec.target.clusterCredentials and never reads them, so a
	// value here would be silently ignored at apply — reject it rather than let an
	// author believe they configured destination auth (the same stance the gateway
	// kind takes on credentials it would otherwise drop).
	if k := m.Spec.Target.Kafka; k != nil {
		if !blankRef(k.Credentials) {
			add("spec.target.kafka.credentials: not supported by kind: %s — the destination is authenticated via spec.target.clusterCredentials (this field applies only to kind: %s)", KindMigration, KindGatewayMigration)
		}
		if k.RestCredentials != nil {
			add("spec.target.kafka.restCredentials: not supported by kind: %s — the destination is authenticated via spec.target.clusterCredentials (this field applies only to kind: %s)", KindMigration, KindGatewayMigration)
		}
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
		errs = append(errs, validateGlobs("spec.topics.include", t.Include)...)
		errs = append(errs, validateGlobs("spec.topics.exclude", t.Exclude)...)
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
				if blankRef(cl.SourceRest.Credentials) {
					add("spec.clusterLink.sourceRest.credentials: must not be empty")
				}
			}
			errs = append(errs, validateKafkaConn("spec.clusterLink.destination", cl.Destination)...)
			if cl.Source != nil {
				add("spec.clusterLink.source: not valid for mode %q", ClusterLinkModeSource)
			}
			if m.Spec.Source.Type == SourceApacheKafka || m.Spec.Source.Type == SourceMSK {
				add("spec.clusterLink.mode: %q is not supported when spec.source.type is %q (only confluent-platform/confluent-cloud can initiate a link)", ClusterLinkModeSource, m.Spec.Source.Type)
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

	if sa := m.Spec.ServiceAccounts; sa != nil {
		if m.Spec.Target.Type != TargetConfluentCloud {
			add("spec.serviceAccounts: only valid when spec.target.type is %q", TargetConfluentCloud)
		}
		for src, id := range sa.Mapping {
			v := strings.TrimPrefix(id, "User:")
			if !strings.HasPrefix(v, "sa-") && !strings.HasPrefix(v, "u-") && !strings.HasPrefix(v, "pool-") {
				add("spec.serviceAccounts.mapping value %q must be a User:sa-/u-/pool- id (source %q)", id, src)
			}
		}
	}
	if a := m.Spec.ACLs; a != nil {
		if m.Spec.Target.Type != TargetConfluentCloud {
			add("spec.acls: only valid when spec.target.type is %q", TargetConfluentCloud)
		}
		errs = append(errs, validateSelection("spec.acls.include", a.Include)...)
		errs = append(errs, validateGlobs("spec.acls.include", a.Include)...)
		errs = append(errs, validateGlobs("spec.acls.exclude", a.Exclude)...)
		if a.UnprotectedTopicPolicy != "" {
			if err := validateEnum("spec.acls.unprotectedTopicPolicy", a.UnprotectedTopicPolicy, UnprotectedTopicPolicyWarn, UnprotectedTopicPolicyFail); err != nil {
				errs = append(errs, err)
			}
		}
		if iam := a.IAM; iam != nil {
			if m.Spec.Source.Type != SourceMSK {
				add("spec.acls.iam: only valid when spec.source.type is %q", SourceMSK)
			}
			if blank(iam.ClusterArn) {
				add("spec.acls.iam.clusterArn: required when spec.acls.iam is set")
			} else if !isMSKClusterArn(iam.ClusterArn) {
				add("spec.acls.iam.clusterArn: %q is not a valid MSK cluster ARN (arn:aws:kafka:<region>:<account>:cluster/<name>/<uuid>)", iam.ClusterArn)
			}
			hasExplicit := len(iam.PrincipalArns) > 0
			if hasExplicit && iam.DiscoverAllRoles {
				add("spec.acls.iam: principalArns and discoverAllRoles are mutually exclusive")
			}
			if !hasExplicit && !iam.DiscoverAllRoles {
				add("spec.acls.iam: requires principalArns or discoverAllRoles")
			}
			for _, p := range iam.PrincipalArns {
				if !isIAMPrincipalArn(p) {
					add("spec.acls.iam.principalArns: %q is not a valid IAM role/user ARN", p)
				}
			}
		}
	}
	if s := m.Spec.Schemas; s != nil {
		errs = append(errs, validateSelection("spec.schemas.include", s.Include)...)
	}
	if c := m.Spec.Connectors; c != nil {
		errs = append(errs, validateSelection("spec.connectors.include", c.Include)...)
	}

	return errs
}
