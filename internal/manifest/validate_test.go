package manifest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// validCCWithDestinationLink returns a valid CC migration with a minimal
// destination-mode cluster link set. It must pass Validate() with no errors.
func validCCWithDestinationLink(t *testing.T) *Migration {
	t.Helper()
	m := validCC()
	m.Spec.ClusterLink = &ClusterLink{
		Name:   "l",
		Source: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./s.yaml"},
	}
	return m
}

// joinErrs joins error strings with "; ".
func joinErrs(errs []error) string {
	parts := make([]string, len(errs))
	for i, e := range errs {
		parts[i] = e.Error()
	}
	return strings.Join(parts, "; ")
}

// validCC returns a minimal, fully-valid Confluent Cloud manifest.
func validCC() *Migration {
	return &Migration{
		APIVersion: SupportedAPIVersion,
		Kind:       KindMigration,
		Metadata:   Metadata{Name: "m"},
		Spec: Spec{
			Source: Source{Type: SourceApacheKafka, BootstrapServers: []string{"b:9092"}, Credentials: "./s.yaml"},
			Target: Target{Type: TargetConfluentCloud, ClusterID: "lkc-1", ClusterCredentials: "./t.yaml", Kafka: &TargetKafka{RestEndpoint: "https://pkc-x.confluent.cloud:443"}},
		},
	}
}

func errorContains(errs []error, substr string) bool {
	for _, e := range errs {
		if e != nil && strings.Contains(e.Error(), substr) {
			return true
		}
	}
	return false
}

func TestValidate_ValidCC(t *testing.T) {
	require.Empty(t, validCC().Validate())
}

func TestValidate_APIVersionAndKind(t *testing.T) {
	m := validCC()
	m.APIVersion = "wrong"
	m.Kind = "wrong"
	errs := m.Validate()
	require.True(t, errorContains(errs, "apiVersion"))
	require.True(t, errorContains(errs, "kind"))
}

func TestValidate_MetadataName(t *testing.T) {
	m := validCC()
	m.Metadata.Name = ""
	require.True(t, errorContains(m.Validate(), "metadata.name"))
}

func TestValidate_SourceType(t *testing.T) {
	m := validCC()
	m.Spec.Source.Type = "kinesis"
	require.True(t, errorContains(m.Validate(), "spec.source.type"))
}

func TestValidate_SourceCredentials(t *testing.T) {
	m := validCC()
	m.Spec.Source.Credentials = ""
	require.True(t, errorContains(m.Validate(), "spec.source.credentials"))
}

func TestValidate_SourceBootstrapServers_Missing(t *testing.T) {
	m := validCC()
	m.Spec.Source.BootstrapServers = nil
	require.True(t, errorContains(m.Validate(), "spec.source.bootstrapServers"))
}

func TestValidate_SourceBootstrapServers_InvalidFormat(t *testing.T) {
	m := validCC()
	m.Spec.Source.BootstrapServers = []string{"not-a-valid-host-port"}
	errs := m.Validate()
	require.True(t, errorContains(errs, "spec.source.bootstrapServers"))
}

func TestValidate_TargetCCRequiresCluster(t *testing.T) {
	m := validCC()
	m.Spec.Target.ClusterID = ""
	require.True(t, errorContains(m.Validate(), "spec.target.clusterId"))
}

func TestValidate_TargetCPRequiresRestEndpoint(t *testing.T) {
	m := validCC()
	m.Spec.Target = Target{Type: TargetConfluentPlatform, ClusterCredentials: "./t.yaml"}
	require.True(t, errorContains(m.Validate(), "spec.target.kafka.restEndpoint"))
}

func TestValidate_TargetTypeUnsupported(t *testing.T) {
	m := validCC()
	m.Spec.Target.Type = "self-managed"
	require.True(t, errorContains(m.Validate(), "spec.target.type"))
}

func TestValidate_SourceTypeEmpty(t *testing.T) {
	m := validCC()
	m.Spec.Source.Type = ""
	require.True(t, errorContains(m.Validate(), "spec.source.type"))
}

func TestValidate_TargetTypeEmpty(t *testing.T) {
	m := validCC()
	m.Spec.Target.Type = ""
	require.True(t, errorContains(m.Validate(), "spec.target.type"))
}

func TestValidate_TargetCredentials(t *testing.T) {
	m := validCC()
	m.Spec.Target.ClusterCredentials = ""
	require.True(t, errorContains(m.Validate(), "spec.target.clusterCredentials"))
}

// TestValidate_CloudCredentialsCConly rejects cloudCredentials on a
// confluent-platform target (it is the CC IAM v2 Cloud/Global key).
func TestValidate_CloudCredentialsCConly(t *testing.T) {
	m := baseCPTargetManifest(t)
	m.Spec.Target.CloudCredentials = "./cloud.yaml"
	require.True(t, errorContains(m.Validate(), `spec.target.cloudCredentials: only valid when spec.target.type is "confluent-cloud"`))
}

// TestValidate_CloudCredentialsRequiredForAutoCreate requires cloudCredentials
// when serviceAccounts.autoCreate is true — a subset of the broader "spec.acls
// on a confluent-cloud target requires cloudCredentials" rule (auto-create also
// calls IAM v2).
func TestValidate_CloudCredentialsRequiredForAutoCreate(t *testing.T) {
	m := baseCCTargetManifest(t)
	m.Spec.ServiceAccounts = &ServiceAccounts{AutoCreate: true}
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	require.True(t, errorContains(m.Validate(), "spec.target.cloudCredentials: required for spec.acls on a confluent-cloud target"))
}

// TestValidate_CloudCredentialsForAutoCreate_Valid confirms a CC target with
// autoCreate and cloudCredentials set validates clean.
func TestValidate_CloudCredentialsForAutoCreate_Valid(t *testing.T) {
	m := baseCCTargetManifest(t)
	m.Spec.Target.CloudCredentials = "./cloud.yaml"
	m.Spec.ServiceAccounts = &ServiceAccounts{AutoCreate: true}
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	require.Empty(t, m.Validate())
}

// TestValidate_CloudCredentialsRequiredForMappingOnlyACLs confirms a mapping-only
// serviceAccounts block (autoCreate:false) STILL requires cloudCredentials once
// spec.acls is present on a confluent-cloud target: Confluent Cloud returns
// numeric ACL principals for mapped accounts too, so the acls reconciler needs
// the Cloud/Global key to build its numeric map and stay idempotent.
func TestValidate_CloudCredentialsRequiredForMappingOnlyACLs(t *testing.T) {
	m := baseCCTargetManifest(t)
	m.Spec.ServiceAccounts = &ServiceAccounts{AutoCreate: false, Mapping: map[string]string{"User:x": "sa-1"}}
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	require.True(t, errorContains(m.Validate(), "spec.target.cloudCredentials: required for spec.acls on a confluent-cloud target"))
}

// TestValidate_CloudCredentialsForMappingOnlyACLs_Valid confirms the same
// mapping-only acls manifest validates clean once cloudCredentials is set.
func TestValidate_CloudCredentialsForMappingOnlyACLs_Valid(t *testing.T) {
	m := baseCCTargetManifest(t)
	m.Spec.Target.CloudCredentials = "./cloud.yaml"
	m.Spec.ServiceAccounts = &ServiceAccounts{AutoCreate: false, Mapping: map[string]string{"User:x": "sa-1"}}
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	require.Empty(t, m.Validate())
}

func TestValidate_TopicsModeUnsupported(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: "copy", Include: []string{"*"}}
	require.True(t, errorContains(m.Validate(), "spec.topics.mode"))
}

func TestValidate_TopicsIncludeRequired(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: TopicModeNew, Include: nil}
	require.True(t, errorContains(m.Validate(), "spec.topics.include"))
}

func TestValidate_MirrorRequiresClusterLink(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: TopicModeMirror, Include: []string{"*"}}
	m.Spec.ClusterLink = nil
	require.True(t, errorContains(m.Validate(), "spec.clusterLink.name"))
}

func TestValidate_StubSectionsRequireInclude(t *testing.T) {
	m := validCC()
	m.Spec.ACLs = &ACLs{Include: nil}
	m.Spec.Schemas = &Schemas{Include: nil}
	m.Spec.Connectors = &Connectors{Include: nil}
	errs := m.Validate()
	require.True(t, errorContains(errs, "spec.acls.include"))
	require.True(t, errorContains(errs, "spec.schemas.include"))
	require.True(t, errorContains(errs, "spec.connectors.include"))
}

func TestValidate_ReportsAllErrorsAtOnce(t *testing.T) {
	m := &Migration{} // everything wrong
	errs := m.Validate()
	require.GreaterOrEqual(t, len(errs), 5)
}

func TestValidate_TopicsModeEmpty(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: "", Include: []string{"*"}}
	require.True(t, errorContains(m.Validate(), "spec.topics.mode"))
}

func TestValidate_MirrorRequiresClusterLinkName(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: TopicModeMirror, Include: []string{"*"}}
	m.Spec.ClusterLink = &ClusterLink{Name: ""}
	require.True(t, errorContains(m.Validate(), "spec.clusterLink.name"))
}

func TestValidate_TopicsIncludeBlankEntry(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: TopicModeNew, Include: []string{""}}
	require.True(t, errorContains(m.Validate(), "spec.topics.include"))
}

func TestValidate_TopicsMirrorWithGlobs_Valid(t *testing.T) {
	m := validCCWithDestinationLink(t)
	m.Spec.Topics = &Topics{
		Mode:    TopicModeMirror,
		Include: []string{"orders.*", "events"},
		Exclude: []string{"_*"},
	}
	require.Empty(t, m.Validate())
}

func TestValidate_TopicsMalformedGlob(t *testing.T) {
	m := validCCWithDestinationLink(t)
	m.Spec.Topics = &Topics{
		Mode:    TopicModeMirror,
		Include: []string{"["},
	}
	require.Contains(t, joinErrs(m.Validate()), "invalid pattern")
}

func TestValidate_TopicsNewModeNoClusterLink_Valid(t *testing.T) {
	m := validCC()
	m.Spec.Topics = &Topics{Mode: TopicModeNew, Include: []string{"*"}}
	require.Empty(t, m.Validate())
}

func TestValidate_CCTargetRequiresRestEndpoint(t *testing.T) {
	m := validCC()
	m.Spec.Target.Kafka = nil
	require.True(t, errorContains(m.Validate(), "spec.target.kafka.restEndpoint"))
}

func TestValidate_CPTargetRejectsCluster(t *testing.T) {
	m := validCC()
	m.Spec.Target = Target{
		Type:               TargetConfluentPlatform,
		ClusterCredentials: "./t.yaml",
		Kafka:              &TargetKafka{RestEndpoint: "https://broker:8090"},
		ClusterID:          "lkc-1",
	}
	require.True(t, errorContains(m.Validate(), "spec.target.clusterId"))
}

func TestValidate_ClusterLinkConfigFields(t *testing.T) {
	cases := []struct {
		name    string
		mutate  func(cl *ClusterLink)
		wantSub string
	}{
		{"negative offset interval",
			func(cl *ClusterLink) { cl.ConsumerOffsetSync = &ConsumerOffsetSync{IntervalMs: -1} },
			"consumerOffsetSync.intervalMs"},
		{"negative topic interval",
			func(cl *ClusterLink) { cl.TopicConfigSync = &TopicConfigSync{IntervalMs: -5} },
			"topicConfigSync.intervalMs"},
		{"bad patternType",
			func(cl *ClusterLink) {
				cl.ConsumerOffsetSync = &ConsumerOffsetSync{GroupFilters: []GroupFilter{{Name: "x", PatternType: "REGEX", FilterType: "INCLUDE"}}}
			},
			"patternType"},
		{"bad filterType",
			func(cl *ClusterLink) {
				cl.ConsumerOffsetSync = &ConsumerOffsetSync{GroupFilters: []GroupFilter{{Name: "x", PatternType: "LITERAL", FilterType: "MAYBE"}}}
			},
			"filterType"},
		{"blank filter name",
			func(cl *ClusterLink) {
				cl.ConsumerOffsetSync = &ConsumerOffsetSync{GroupFilters: []GroupFilter{{Name: " ", PatternType: "LITERAL", FilterType: "INCLUDE"}}}
			},
			"name"},
		{"escape-hatch overlap (managed key)",
			func(cl *ClusterLink) { cl.Configs = map[string]string{"cluster.link.prefix": "x"} },
			"not configs"},
		{"escape-hatch read-only link.prefix",
			func(cl *ClusterLink) { cl.Configs = map[string]string{"link.prefix": "x"} },
			"not configs"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := validCCWithDestinationLink(t)
			tc.mutate(m.Spec.ClusterLink)
			errs := m.Validate()
			require.NotEmpty(t, errs)
			require.Contains(t, joinErrs(errs), tc.wantSub)
		})
	}
}

func TestValidate_ClusterLinkConfigFields_Valid(t *testing.T) {
	m := validCCWithDestinationLink(t)
	no := false
	m.Spec.ClusterLink.Prefix = "a."
	m.Spec.ClusterLink.ConsumerOffsetSync = &ConsumerOffsetSync{
		Enable: &no, IntervalMs: 1000,
		GroupFilters: []GroupFilter{{Name: "*", PatternType: "LITERAL", FilterType: "INCLUDE"}},
	}
	m.Spec.ClusterLink.TopicConfigSync = &TopicConfigSync{IntervalMs: 5000}
	require.Empty(t, m.Validate())
}

func TestValidate_ClusterLinkModes(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(m *Migration)
		wantErr string // substring; "" means expect valid
	}{
		{
			name: "valid destination mode",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:   "cl",
					Mode:   "destination",
					Source: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
				}
			},
		},
		{
			name: "default empty mode treated as destination",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:   "cl",
					Source: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
				}
			},
		},
		{
			name: "destination missing source",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{Name: "cl", Mode: "destination"}
			},
			wantErr: "spec.clusterLink.source",
		},
		{
			name: "destination source missing bootstrapServers",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:   "cl",
					Mode:   "destination",
					Source: &KafkaConn{Credentials: "./src.yaml"},
				}
			},
			wantErr: "spec.clusterLink.source.bootstrapServers",
		},
		{
			name: "destination source bootstrapServers invalid format",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:   "cl",
					Mode:   "destination",
					Source: &KafkaConn{BootstrapServers: []string{"not-valid"}, Credentials: "./src.yaml"},
				}
			},
			wantErr: "spec.clusterLink.source.bootstrapServers",
		},
		{
			name: "destination with sourceRest set rejected",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:       "cl",
					Mode:       "destination",
					Source:     &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
					SourceRest: &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
				}
			},
			wantErr: "spec.clusterLink.sourceRest",
		},
		{
			name: "destination with destination set rejected",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "destination",
					Source:      &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.destination",
		},
		{
			name: "valid source mode (confluent-platform source)",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = SourceConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					SourceRest:  &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
		},
		{
			name: "source missing sourceRest",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = SourceConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.sourceRest",
		},
		{
			name: "source sourceRest missing endpoint",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					SourceRest:  &RestRef{Credentials: "./rest.yaml"},
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.sourceRest.endpoint",
		},
		{
			name: "source sourceRest missing credentials",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					SourceRest:  &RestRef{Endpoint: "https://src:8090"},
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.sourceRest.credentials",
		},
		{
			name: "source missing destination",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:       "cl",
					Mode:       "source",
					SourceRest: &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
				}
			},
			wantErr: "spec.clusterLink.destination",
		},
		{
			name: "source destination missing bootstrapServers",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					SourceRest:  &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					Destination: &KafkaConn{Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.destination.bootstrapServers",
		},
		{
			name: "source with source set rejected",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					Source:      &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
					SourceRest:  &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.source",
		},
		{
			name: "source mode rejected for apache-kafka source",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = SourceApacheKafka
				m.Spec.ClusterLink = &ClusterLink{
					Name:        "cl",
					Mode:        "source",
					SourceRest:  &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./dst.yaml"},
				}
			},
			wantErr: "spec.clusterLink.mode",
		},
		{
			name: "bidirectional mode rejected with clear message",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:   "cl",
					Mode:   "bidirectional",
					Source: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
				}
			},
			wantErr: "not supported",
		},
		{
			name: "unknown mode rejected",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:   "cl",
					Mode:   "sideways",
					Source: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./src.yaml"},
				}
			},
			wantErr: "spec.clusterLink.mode",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			m := validCC()
			tc.mutate(m)
			errs := m.Validate()
			if tc.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.True(t, errorContains(errs, tc.wantErr),
				"expected error containing %q, got %v", tc.wantErr, errs)
		})
	}
}

func TestValidate_SourceMSK_Valid(t *testing.T) {
	m := validCC()
	m.Spec.Source.Type = SourceMSK
	require.Empty(t, m.Validate())
}

// baseCCTargetManifest returns a valid Confluent Cloud target manifest with a
// mirror-mode cluster link and topics set, so callers can add/remove
// sections (e.g. to test acls-only manifests) while the rest stays valid.
func baseCCTargetManifest(t *testing.T) *Migration {
	t.Helper()
	m := validCCWithDestinationLink(t)
	m.Spec.Topics = &Topics{Mode: TopicModeMirror, Include: []string{"*"}}
	return m
}

// baseCPTargetManifest mirrors baseCCTargetManifest but for target type
// confluent-platform.
func baseCPTargetManifest(t *testing.T) *Migration {
	t.Helper()
	m := baseCCTargetManifest(t)
	m.Spec.Target = Target{
		Type:               TargetConfluentPlatform,
		ClusterCredentials: "./t.yaml",
		Kafka:              &TargetKafka{RestEndpoint: "https://broker:8090"},
	}
	return m
}

func TestValidate_ServiceAccountsCCOnly(t *testing.T) {
	m := baseCPTargetManifest(t)
	m.Spec.ServiceAccounts = &ServiceAccounts{AutoCreate: true}
	require.True(t, errorContains(m.Validate(), `spec.serviceAccounts: only valid when spec.target.type is "confluent-cloud"`))
}

func TestValidate_ACLsCCOnly(t *testing.T) {
	m := baseCPTargetManifest(t)
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	require.True(t, errorContains(m.Validate(), `spec.acls: only valid when spec.target.type is "confluent-cloud"`))
}

func TestValidate_MappingPrefix(t *testing.T) {
	m := baseCCTargetManifest(t)
	m.Spec.ServiceAccounts = &ServiceAccounts{Mapping: map[string]string{"User:x": "svc-1"}}
	require.True(t, errorContains(m.Validate(), `mapping value "svc-1" must be a User:sa-/u-/pool- id`))
}

// TestValidate_MappingPrefix_AcceptedValues confirms every documented
// accepted mapping-value shape (bare sa-/u-/pool- ids and the User:sa- form)
// passes validation with no error — the positive counterpart to
// TestValidate_MappingPrefix's negative (rejected) case.
func TestValidate_MappingPrefix_AcceptedValues(t *testing.T) {
	for _, v := range []string{"sa-1", "u-1", "pool-1", "User:sa-1"} {
		m := baseCCTargetManifest(t)
		m.Spec.ServiceAccounts = &ServiceAccounts{Mapping: map[string]string{"User:x": v}}
		require.Empty(t, m.Validate(), "mapping value %q should be accepted", v)
	}
}

func TestValidate_ACLsOnlyManifestAccepted(t *testing.T) {
	m := baseCCTargetManifest(t)
	m.Spec.ClusterLink, m.Spec.Topics = nil, nil
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	// spec.acls on a confluent-cloud target requires the Cloud/Global key (for
	// the acls reconciler's numeric-principal map).
	m.Spec.Target.CloudCredentials = "./cloud.yaml"
	require.Empty(t, m.Validate())
}

// mskArn is a well-formed MSK cluster ARN used across the acls.iam tests.
const mskArn = "arn:aws:kafka:us-east-1:1:cluster/m/abc-5"

// validCCMigrationWithACLs returns a valid msk->confluent-cloud migration
// manifest with spec.acls set (Include + the cloudCredentials it requires on
// a CC target) — the base fixture for spec.acls.iam validation tests.
func validCCMigrationWithACLs(t *testing.T) *Migration {
	t.Helper()
	m := baseCCTargetManifest(t)
	m.Spec.ClusterLink, m.Spec.Topics = nil, nil
	m.Spec.Source.Type = SourceMSK
	m.Spec.ACLs = &ACLs{Include: []string{"*"}}
	m.Spec.Target.CloudCredentials = "./cloud.yaml"
	return m
}

// requireHasErr asserts errs contains, across all messages, every substring
// in substrs (each substr must appear somewhere in the joined error text).
func requireHasErr(t *testing.T, errs []error, substrs ...string) {
	t.Helper()
	joined := joinErrs(errs)
	for _, s := range substrs {
		require.Contains(t, joined, s, "expected errors to contain %q; got: %v", s, errs)
	}
}

// TestValidate_ACLsIAM covers spec.acls.iam: msk-only, clusterArn required and
// well-formed, principalArns/discoverAllRoles mutual exclusivity, and
// well-formed principal ARNs.
func TestValidate_ACLsIAM(t *testing.T) {
	base := func(mut func(m *Migration)) *Migration {
		m := validCCMigrationWithACLs(t)
		mut(m)
		return m
	}
	// (a) non-msk source
	m := base(func(m *Migration) {
		m.Spec.Source.Type = SourceApacheKafka
		m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{"arn:aws:iam::1:role/R"}}
	})
	requireHasErr(t, m.Validate(), "spec.acls.iam", "msk")
	// (b) missing clusterArn
	m = base(func(m *Migration) {
		m.Spec.ACLs.IAM = &ACLsIAM{PrincipalArns: []string{"arn:aws:iam::1:role/R"}}
	})
	requireHasErr(t, m.Validate(), "clusterArn", "required")
	// (c) both modes set
	m = base(func(m *Migration) {
		m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{"arn:aws:iam::1:role/R"}, DiscoverAllRoles: true}
	})
	requireHasErr(t, m.Validate(), "mutually exclusive")
	// (d) neither mode set
	m = base(func(m *Migration) {
		m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn}
	})
	requireHasErr(t, m.Validate(), "principalArns", "discoverAllRoles")
	// (e) malformed clusterArn
	m = base(func(m *Migration) {
		m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: "not-an-arn", PrincipalArns: []string{"arn:aws:iam::1:role/R"}}
	})
	requireHasErr(t, m.Validate(), "clusterArn", "not a valid MSK cluster ARN")
	// (f) malformed principal ARN
	m = base(func(m *Migration) {
		m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{"not-an-arn"}}
	})
	requireHasErr(t, m.Validate(), "principalArns", "not a valid IAM role/user ARN")
	// (g) valid explicit config
	m = base(func(m *Migration) {
		m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{"arn:aws:iam::1:role/R"}}
	})
	require.Empty(t, m.Validate())
}

// TestValidate_ACLsIAM_DiscoverAllRolesValid confirms the discoverAllRoles
// mode (no explicit principalArns) validates clean on its own.
func TestValidate_ACLsIAM_DiscoverAllRolesValid(t *testing.T) {
	m := validCCMigrationWithACLs(t)
	m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, DiscoverAllRoles: true}
	require.Empty(t, m.Validate())
}

// TestValidate_ACLsIAM_MultiplePrincipalArns_OneMalformed confirms each
// principalArns entry is validated independently, not just the first.
func TestValidate_ACLsIAM_MultiplePrincipalArns_OneMalformed(t *testing.T) {
	m := validCCMigrationWithACLs(t)
	m.Spec.ACLs.IAM = &ACLsIAM{
		ClusterArn:    mskArn,
		PrincipalArns: []string{"arn:aws:iam::1:role/Good", "bad-arn", "arn:aws:iam::1:user/AlsoGood"},
	}
	errs := m.Validate()
	require.True(t, errorContains(errs, `"bad-arn" is not a valid IAM role/user ARN`))
	require.False(t, errorContains(errs, `"arn:aws:iam::1:role/Good" is not a valid IAM role/user ARN`))
}

// TestValidate_ACLsIAM_ClusterArnMalformed_EmptyRegionAccount rejects an ARN
// that merely LOOKS like an MSK cluster ARN by substring (has "arn:aws:kafka:"
// prefix and ":cluster/" somewhere in it) but has empty region/account
// segments — this shape does not actually scope anything (arnClusterIdentity
// in internal/migrate/acls/iam_translate.go cannot parse a cluster identity
// out of it any better than validation can), so a manifest naming it would
// silently match zero grants at apply time. See Finding 2(a) / task-7.
func TestValidate_ACLsIAM_ClusterArnMalformed_EmptyRegionAccount(t *testing.T) {
	m := validCCMigrationWithACLs(t)
	m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: "arn:aws:kafka::cluster/x", PrincipalArns: []string{"arn:aws:iam::1:role/R"}}
	requireHasErr(t, m.Validate(), "clusterArn", "not a valid MSK cluster ARN")
}

// TestValidate_ACLsIAM_ClusterArnWellFormed_Valid is the positive counterpart:
// a fully-populated ARN (region, account, name, uuid all non-empty) must
// validate clean.
func TestValidate_ACLsIAM_ClusterArnWellFormed_Valid(t *testing.T) {
	m := validCCMigrationWithACLs(t)
	m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: "arn:aws:kafka:us-east-1:111122223333:cluster/mymsk/abc-5", PrincipalArns: []string{"arn:aws:iam::1:role/R"}}
	require.Empty(t, m.Validate())
}

// TestValidate_ACLsIAM_PrincipalArnMalformed_EmptyAccountOrName mirrors the
// clusterArn tightening for principal ARNs: an ARN that superficially matches
// (prefix + ":role/"/":user/" substring) but has an empty account or an empty
// trailing name segment must be rejected — principalFromArn
// (internal/migrate/acls/iam_translate.go) would otherwise derive a bogus
// "User:" (empty name) Kafka principal from it.
func TestValidate_ACLsIAM_PrincipalArnMalformed_EmptyAccountOrName(t *testing.T) {
	m := validCCMigrationWithACLs(t)
	m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{"arn:aws:iam:::role/R"}}
	requireHasErr(t, m.Validate(), "principalArns", "not a valid IAM role/user ARN")

	m2 := validCCMigrationWithACLs(t)
	m2.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{"arn:aws:iam::111122223333:role/"}}
	requireHasErr(t, m2.Validate(), "principalArns", "not a valid IAM role/user ARN")
}

// TestValidate_ACLsIAM_PrincipalArnWellFormed_Valid confirms well-formed role
// and user ARNs (including a path-bearing role name) still validate clean
// after the tightening.
func TestValidate_ACLsIAM_PrincipalArnWellFormed_Valid(t *testing.T) {
	m := validCCMigrationWithACLs(t)
	m.Spec.ACLs.IAM = &ACLsIAM{ClusterArn: mskArn, PrincipalArns: []string{
		"arn:aws:iam::111122223333:role/AppRole",
		"arn:aws:iam::111122223333:user/alice",
		"arn:aws:iam::111122223333:role/team/AppRole",
	}}
	require.Empty(t, m.Validate())
}

func TestValidate_SourceMSK_CannotSourceInitiate(t *testing.T) {
	m := validCC()
	m.Spec.Source.Type = SourceMSK
	m.Spec.ClusterLink = &ClusterLink{
		Name:        "l",
		Mode:        ClusterLinkModeSource,
		SourceRest:  &RestRef{Endpoint: "https://s", Credentials: "./s.yaml"},
		Destination: &KafkaConn{BootstrapServers: []string{"b:9092"}, Credentials: "./d.yaml"},
	}
	require.True(t, errorContains(m.Validate(), "is not supported when spec.source.type"))
}
