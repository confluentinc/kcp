// TEMPORARY — added as a tripwire for the internal/types reorg.
// Delete this file (and the four golden fixtures under testdata/) once the
// reorg has landed and the team is satisfied with the result.
//
// What it does: builds a populated value inline for each on-disk format
// internal/types owns (State, Credentials, OSKCredentials, MigrationState),
// marshals it via the same path the public WriteToFile uses, and diffs the
// bytes against a checked-in golden fixture. Also confirms unmarshal-then-
// remarshal of the golden is byte-stable.
//
// The reorg PR must NOT regenerate these goldens — that is exactly the
// drift the test exists to detect. The API snapshot test sorts struct
// fields alphabetically so it cannot catch declaration-order changes
// within a struct; this test does, via JSON / YAML byte output.
//
// To (re)capture goldens after an intentional public format change, run:
//
//   UPDATE_ROUNDTRIP_GOLDENS=1 go test ./internal/types/ -run TestReorgNoop_RoundTrip
//

package types_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/goccy/go-yaml"
)

// fixedTime is a stable timestamp used in all fixtures so the goldens are
// reproducible across machines and runs.
var fixedTime = time.Date(2025, 1, 15, 12, 0, 0, 0, time.UTC)

func TestReorgNoop_RoundTrip(t *testing.T) {
	cases := []struct {
		name       string
		goldenPath string
		marshal    func(t *testing.T) []byte
		unmarshal  func(t *testing.T, b []byte) []byte
	}{
		{
			name:       "state.json",
			goldenPath: "testdata/sample_state.golden.json",
			marshal: func(t *testing.T) []byte {
				b, err := json.Marshal(buildSampleState())
				if err != nil {
					t.Fatalf("marshal state: %v", err)
				}
				return b
			},
			unmarshal: func(t *testing.T, b []byte) []byte {
				s, err := types.NewStateFromBytes(b)
				if err != nil {
					t.Fatalf("NewStateFromBytes: %v", err)
				}
				out, err := json.Marshal(s)
				if err != nil {
					t.Fatalf("re-marshal state: %v", err)
				}
				return out
			},
		},
		{
			name:       "msk_credentials.yaml",
			goldenPath: "testdata/sample_msk_credentials.golden.yaml",
			marshal: func(t *testing.T) []byte {
				c := buildSampleMSKCredentials()
				b, err := c.ToYaml()
				if err != nil {
					t.Fatalf("ToYaml: %v", err)
				}
				return b
			},
			unmarshal: func(t *testing.T, b []byte) []byte {
				var c types.Credentials
				if err := yaml.Unmarshal(b, &c); err != nil {
					t.Fatalf("unmarshal MSK creds: %v", err)
				}
				out, err := c.ToYaml()
				if err != nil {
					t.Fatalf("re-marshal MSK creds: %v", err)
				}
				return out
			},
		},
		{
			name:       "osk_credentials.yaml",
			goldenPath: "testdata/sample_osk_credentials.golden.yaml",
			marshal: func(t *testing.T) []byte {
				b, err := yaml.Marshal(buildSampleOSKCredentials())
				if err != nil {
					t.Fatalf("marshal OSK creds: %v", err)
				}
				return b
			},
			unmarshal: func(t *testing.T, b []byte) []byte {
				var c types.OSKCredentials
				if err := yaml.Unmarshal(b, &c); err != nil {
					t.Fatalf("unmarshal OSK creds: %v", err)
				}
				out, err := yaml.Marshal(&c)
				if err != nil {
					t.Fatalf("re-marshal OSK creds: %v", err)
				}
				return out
			},
		},
		{
			name:       "migration_state.json",
			goldenPath: "testdata/sample_migration_state.golden.json",
			marshal: func(t *testing.T) []byte {
				b, err := json.MarshalIndent(buildSampleMigrationState(), "", "  ")
				if err != nil {
					t.Fatalf("marshal migration: %v", err)
				}
				return b
			},
			unmarshal: func(t *testing.T, b []byte) []byte {
				var ms types.MigrationState
				if err := json.Unmarshal(b, &ms); err != nil {
					t.Fatalf("unmarshal migration: %v", err)
				}
				out, err := json.MarshalIndent(&ms, "", "  ")
				if err != nil {
					t.Fatalf("re-marshal migration: %v", err)
				}
				return out
			},
		},
	}

	updateGoldens := os.Getenv("UPDATE_ROUNDTRIP_GOLDENS") != ""

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.marshal(t)

			if updateGoldens {
				if err := os.MkdirAll(filepath.Dir(tc.goldenPath), 0o755); err != nil {
					t.Fatalf("mkdir testdata: %v", err)
				}
				if err := os.WriteFile(tc.goldenPath, got, 0o644); err != nil {
					t.Fatalf("write golden: %v", err)
				}
				t.Logf("wrote golden: %s (%d bytes)", tc.goldenPath, len(got))
				return
			}

			want, err := os.ReadFile(tc.goldenPath)
			if err != nil {
				t.Fatalf("missing golden %s — capture with UPDATE_ROUNDTRIP_GOLDENS=1: %v", tc.goldenPath, err)
			}

			if string(want) != string(got) {
				t.Fatalf("marshalled output differs from golden %s.\nIf intentional, re-capture with UPDATE_ROUNDTRIP_GOLDENS=1.\n--- want (%d bytes) ---\n%s\n--- got (%d bytes) ---\n%s", tc.goldenPath, len(want), string(want), len(got), string(got))
			}

			// Confirm unmarshal(golden) -> re-marshal is byte-stable too.
			out := tc.unmarshal(t, want)
			if string(out) != string(want) {
				t.Fatalf("round-trip unstable for %s: re-marshalled bytes differ from golden", tc.goldenPath)
			}
		})
	}
}

// buildSampleState constructs a representative State for golden capture.
// It exercises the native (non-AWS-SDK) types under the refactor's control;
// AWSClientInformation is left zero-valued because populating its kafkatypes.*
// fields adds noise without value (those are external to internal/types).
func buildSampleState() *types.State {
	scramMech := "SCRAM-SHA-256"
	return &types.State{
		KcpBuildInfo: types.KcpBuildInfo{
			Version: "test-0.0.0",
			Commit:  "deadbeef",
			Date:    "2025-01-15",
		},
		Timestamp: fixedTime,
		MSKSources: &types.MSKSourcesState{
			Regions: []types.DiscoveredRegion{
				{
					Name:     "us-east-1",
					Clusters: []types.DiscoveredCluster{
						{
							Name:   "cluster-a",
							Arn:    "arn:aws:kafka:us-east-1:111:cluster/cluster-a/abc",
							Region: "us-east-1",
							KafkaAdminClientInformation: types.KafkaAdminClientInformation{
								ClusterID:     "cluster-a-id",
								SaslMechanism: scramMech,
								Topics: &types.Topics{
									Details: []types.TopicDetails{
										{Name: "orders", Partitions: 6, ReplicationFactor: 3},
										{Name: "payments", Partitions: 3, ReplicationFactor: 3},
									},
									Summary: types.TopicSummary{
										Topics:          2,
										TotalPartitions: 9,
									},
								},
								Acls: []types.Acls{
									{
										ResourceType:        "TOPIC",
										ResourceName:        "orders",
										ResourcePatternType: "LITERAL",
										Principal:           "User:alice",
										Host:                "*",
										Operation:           "READ",
										PermissionType:      "ALLOW",
									},
								},
							},
							DiscoveredClients: []types.DiscoveredClient{
								{
									CompositeKey: "consumer-1|consumer",
									ClientId:     "consumer-1",
									Role:         "consumer",
									Topic:        "orders",
									Auth:         "SASL/SCRAM",
									Principal:    "User:alice",
									Timestamp:    fixedTime,
								},
							},
						},
					},
				},
			},
		},
		OSKSources: &types.OSKSourcesState{
			Clusters: []types.OSKDiscoveredCluster{
				{
					ID:               "osk-prod-1",
					BootstrapServers: []string{"broker-1:9092", "broker-2:9092"},
					KafkaAdminClientInformation: types.KafkaAdminClientInformation{
						ClusterID:     "osk-prod-1-id",
						SaslMechanism: scramMech,
					},
					DiscoveredClients: []types.DiscoveredClient{},
					Metadata: types.OSKClusterMetadata{
						Environment:  "production",
						Location:     "dc-1",
						KafkaVersion: "3.6.0",
						Labels:       map[string]string{"team": "platform"},
						LastScanned:  fixedTime,
					},
				},
			},
		},
	}
}

func buildSampleMSKCredentials() *types.Credentials {
	return &types.Credentials{
		Regions: []types.RegionAuth{
			{
				Name: "us-east-1",
				Clusters: []types.ClusterAuth{
					{
						Name: "cluster-a",
						Arn:  "arn:aws:kafka:us-east-1:111:cluster/cluster-a/abc",
						AuthMethod: types.AuthMethodConfig{
							IAM: &types.IAMConfig{Use: true},
						},
					},
					{
						Name: "cluster-b",
						Arn:  "arn:aws:kafka:us-east-1:111:cluster/cluster-b/def",
						AuthMethod: types.AuthMethodConfig{
							SASLScram: &types.SASLScramConfig{
								Use:       true,
								Username:  "kcp",
								Password:  "redacted",
								Mechanism: "SHA512",
							},
						},
					},
				},
			},
		},
	}
}

func buildSampleOSKCredentials() *types.OSKCredentials {
	return &types.OSKCredentials{
		Clusters: []types.OSKClusterAuth{
			{
				ID:               "osk-prod-1",
				BootstrapServers: []string{"broker-1:9092", "broker-2:9092"},
				AuthMethod: types.AuthMethodConfig{
					SASLScram: &types.SASLScramConfig{
						Use:       true,
						Username:  "kcp",
						Password:  "redacted",
						Mechanism: "SHA256",
					},
				},
				Metadata: types.OSKCredentialMetadata{
					Environment: "production",
					Location:    "dc-1",
				},
				Jolokia: &types.JolokiaConfig{
					Endpoints: []string{"http://broker-1:8778/jolokia"},
					Auth: &types.JolokiaAuthConfig{
						Username: "monitor",
						Password: "redacted",
					},
				},
			},
			{
				ID:               "osk-prod-2",
				BootstrapServers: []string{"broker-3:9092"},
				AuthMethod: types.AuthMethodConfig{
					TLS: &types.TLSConfig{
						Use:        true,
						CACert:     "/etc/kcp/ca.pem",
						ClientCert: "/etc/kcp/client.pem",
						ClientKey:  "/etc/kcp/client.key",
					},
				},
				Prometheus: &types.PrometheusConfig{
					URL: "http://prom:9090",
					Auth: &types.PrometheusAuthConfig{
						Username: "promuser",
						Password: "redacted",
					},
				},
			},
		},
	}
}

func buildSampleMigrationState() *types.MigrationState {
	return &types.MigrationState{
		Timestamp: fixedTime,
		KcpBuildInfo: types.KcpBuildInfo{
			Version: "test-0.0.0",
			Commit:  "deadbeef",
			Date:    "2025-01-15",
		},
		Migrations: []types.MigrationConfig{
			{
				MigrationId:         "mig-001",
				CurrentState:        "initialized",
				KubeConfigPath:      "/home/kcp/.kube/config",
				SourceBootstrap:     "source-broker:9092",
				ClusterBootstrap:    "dest-broker:9092",
				ClusterId:           "lkc-abc123",
				ClusterRestEndpoint: "https://pkc.cloud:443",
				ClusterLinkName:     "link-001",
				Topics:              []string{"orders", "payments"},
				ClusterLinkTopics:   []string{"orders", "payments"},
				ClusterLinkConfigs: map[string]string{
					"acl.sync.enable":             "true",
					"consumer.offset.sync.enable": "true",
				},
				InitialCrName:    "gw-cr-001",
				K8sNamespace:     "confluent",
				InitialCrYAML:    []byte("apiVersion: v1\nkind: Gateway"),
				FencedCrYAML:     []byte("apiVersion: v1\nfenced: true"),
				SwitchoverCrYAML: []byte("apiVersion: v1\nswitchover: true"),
			},
			{
				MigrationId:      "mig-002",
				CurrentState:     "fenced",
				SourceBootstrap:  "source-2:9092",
				ClusterBootstrap: "dest-2:9092",
				ClusterId:        "lkc-def456",
				ClusterLinkName:  "link-002",
				Topics:           []string{"events"},
			},
		},
	}
}
