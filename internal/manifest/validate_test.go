package manifest

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// validCC returns a minimal, fully-valid Confluent Cloud manifest.
func validCC() *Migration {
	return &Migration{
		APIVersion: SupportedAPIVersion,
		Kind:       KindMigration,
		Metadata:   Metadata{Name: "m"},
		Spec: Spec{
			Source: Source{Type: SourceApacheKafka, Credentials: "./s.yaml"},
			Target: Target{Type: TargetConfluentCloud, Cluster: "lkc-1", Credentials: "./t.yaml"},
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

func TestValidate_TargetCCRequiresCluster(t *testing.T) {
	m := validCC()
	m.Spec.Target.Cluster = ""
	require.True(t, errorContains(m.Validate(), "spec.target.cluster"))
}

func TestValidate_TargetCPRequiresRestEndpoint(t *testing.T) {
	m := validCC()
	m.Spec.Target = Target{Type: TargetConfluentPlatform, Credentials: "./t.yaml"}
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
	m.Spec.Target.Credentials = ""
	require.True(t, errorContains(m.Validate(), "spec.target.credentials"))
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

func TestValidate_CCTargetRejectsKafkaBlock(t *testing.T) {
	m := validCC()
	m.Spec.Target.Kafka = &TargetKafka{RestEndpoint: "https://broker:8090"}
	require.True(t, errorContains(m.Validate(), "spec.target.kafka"))
}

func TestValidate_CPTargetRejectsCluster(t *testing.T) {
	m := validCC()
	m.Spec.Target = Target{
		Type:        TargetConfluentPlatform,
		Credentials: "./t.yaml",
		Kafka:       &TargetKafka{RestEndpoint: "https://broker:8090"},
		Cluster:     "lkc-1",
	}
	require.True(t, errorContains(m.Validate(), "spec.target.cluster"))
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
					Name:              "cl",
					Mode:              "destination",
					SourceCredentials: "./src.yaml",
				}
			},
		},
		{
			name: "default empty mode treated as destination",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:              "cl",
					SourceCredentials: "./src.yaml",
				}
			},
		},
		{
			name: "destination missing sourceCredentials",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{Name: "cl", Mode: "destination"}
			},
			wantErr: "spec.clusterLink.sourceCredentials",
		},
		{
			name: "destination with sourceRest set rejected",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:              "cl",
					Mode:              "destination",
					SourceCredentials: "./src.yaml",
					SourceRest:        &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
				}
			},
			wantErr: "spec.clusterLink.sourceRest",
		},
		{
			name: "destination with destinationCredentials set rejected",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "destination",
					SourceCredentials:      "./src.yaml",
					DestinationCredentials: "./dst.yaml",
				}
			},
			wantErr: "spec.clusterLink.destinationCredentials",
		},
		{
			name: "valid source mode (confluent-platform source)",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = SourceConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "source",
					SourceRest:             &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					DestinationCredentials: "./dst.yaml",
				}
			},
		},
		{
			name: "source missing sourceRest",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = SourceConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "source",
					DestinationCredentials: "./dst.yaml",
				}
			},
			wantErr: "spec.clusterLink.sourceRest",
		},
		{
			name: "source sourceRest missing endpoint",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "source",
					SourceRest:             &RestRef{Credentials: "./rest.yaml"},
					DestinationCredentials: "./dst.yaml",
				}
			},
			wantErr: "spec.clusterLink.sourceRest.endpoint",
		},
		{
			name: "source sourceRest missing credentials",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "source",
					SourceRest:             &RestRef{Endpoint: "https://src:8090"},
					DestinationCredentials: "./dst.yaml",
				}
			},
			wantErr: "spec.clusterLink.sourceRest.credentials",
		},
		{
			name: "source missing destinationCredentials",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:       "cl",
					Mode:       "source",
					SourceRest: &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
				}
			},
			wantErr: "spec.clusterLink.destinationCredentials",
		},
		{
			name: "source with sourceCredentials set rejected",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = TargetConfluentPlatform
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "source",
					SourceCredentials:      "./src.yaml",
					SourceRest:             &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					DestinationCredentials: "./dst.yaml",
				}
			},
			wantErr: "spec.clusterLink.sourceCredentials",
		},
		{
			name: "source mode rejected for apache-kafka source",
			mutate: func(m *Migration) {
				m.Spec.Source.Type = SourceApacheKafka
				m.Spec.ClusterLink = &ClusterLink{
					Name:                   "cl",
					Mode:                   "source",
					SourceRest:             &RestRef{Endpoint: "https://src:8090", Credentials: "./rest.yaml"},
					DestinationCredentials: "./dst.yaml",
				}
			},
			wantErr: "spec.clusterLink.mode",
		},
		{
			name: "bidirectional mode rejected with clear message",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:              "cl",
					Mode:              "bidirectional",
					SourceCredentials: "./src.yaml",
				}
			},
			wantErr: "not supported",
		},
		{
			name: "unknown mode rejected",
			mutate: func(m *Migration) {
				m.Spec.ClusterLink = &ClusterLink{
					Name:              "cl",
					Mode:              "sideways",
					SourceCredentials: "./src.yaml",
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
