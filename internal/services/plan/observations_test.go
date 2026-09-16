package plan

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

func obsTitles(obs []Observation) string {
	var t []string
	for _, o := range obs {
		t = append(t, o.Title)
	}
	return strings.Join(t, " | ")
}

func TestClusterObservations(t *testing.T) {
	c := report.ProcessedCluster{
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			Topics: &types.Topics{Details: []types.TopicDetails{
				{Name: "orders"},
				{Name: "confluent-link-abc"},           // -> "migration may already be in progress"
				{Name: "cluster.checkpoints.internal"}, // -> MM2 checkpoints
			}},
			Acls: make([]types.Acls, 1001), // -> high ACL count
		},
		// No ClusterMetrics.Aggregates -> "no throughput metrics" info.
	}
	p := engine.Profile{StorageMode: sp("Yes")} // -> tiered storage info

	obs := clusterObservations(c, p)
	got := obsTitles(obs)
	for _, want := range []string{
		"Tiered storage in use",
		"No throughput metrics scanned",
		"Migration may already be in progress",
		"MirrorMaker 2 checkpoints detected",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("missing observation %q; got: %s", want, got)
		}
	}
}

func TestTopicsHaveCustomSettings(t *testing.T) {
	cfg := func(m map[string]string) map[string]*string {
		o := map[string]*string{}
		for k, v := range m {
			vv := v
			o[k] = &vv
		}
		return o
	}
	mk := func(d ...types.TopicDetails) report.ProcessedCluster {
		return report.ProcessedCluster{KafkaAdminClientInformation: types.KafkaAdminClientInformation{Topics: &types.Topics{Details: d}}}
	}
	// No topics scanned -> nil (surface as a question).
	if topicsHaveCustomSettings(report.ProcessedCluster{}) != nil {
		t.Error("no topic scan should return nil")
	}
	// All defaults -> No.
	if got := topicsHaveCustomSettings(mk(types.TopicDetails{ReplicationFactor: 3, Configurations: cfg(map[string]string{"cleanup.policy": "delete", "retention.ms": "604800000"})})); got == nil || *got != "No" {
		t.Errorf("all-default -> No, got %v", got)
	}
	cases := []struct {
		name string
		td   types.TopicDetails
	}{
		{"rf!=3", types.TopicDetails{ReplicationFactor: 2}},
		{"compact+delete", types.TopicDetails{ReplicationFactor: 3, Configurations: cfg(map[string]string{"cleanup.policy": "compact,delete"})}},
		{"retention>7d", types.TopicDetails{ReplicationFactor: 3, Configurations: cfg(map[string]string{"retention.ms": "999999999999"})}},
		{"infinite retention", types.TopicDetails{ReplicationFactor: 3, Configurations: cfg(map[string]string{"retention.ms": "-1"})}},
		{"maxmsg>2mb", types.TopicDetails{ReplicationFactor: 3, Configurations: cfg(map[string]string{"max.message.bytes": "3000000"})}},
	}
	for _, tc := range cases {
		if got := topicsHaveCustomSettings(mk(tc.td)); got == nil || *got != "Yes" {
			t.Errorf("%s should be Yes, got %v", tc.name, got)
		}
	}
}

// An undetected required-when-missing scan fact (source_auth with nothing scanned)
// surfaces as an open required question, not a silent scan fact.
func TestRequiredWhenMissing_SurfacesUndetectedAuth(t *testing.T) {
	// A bare provisioned cluster with no ClientAuthentication -> no auth detected.
	p := engine.Profile{MSKClusterType: engine.MSKProvisioned, SourcePlatform: "Amazon MSK"}
	qs := resolveQuestions(p, IntakeInputs{})
	q := find(qs, "source_auth")
	if q == nil || q.Status != "open_required" {
		t.Errorf("undetected source_auth should be open_required, got %+v", q)
	}
}

func TestStoredGB(t *testing.T) {
	f := func(v float64) *float64 { return &v }
	mk := func(aggs map[string]types.MetricAggregate) report.ProcessedCluster {
		return report.ProcessedCluster{ClusterMetrics: types.ProcessedClusterMetrics{Aggregates: aggs}}
	}
	// No storage metrics -> nil.
	if storedGB(mk(nil)) != nil {
		t.Error("no storage metrics -> nil")
	}
	// Local + remote summed.
	got := storedGB(mk(map[string]types.MetricAggregate{
		"TotalLocalStorageUsage(GB)":  {Maximum: f(800)},
		"TotalRemoteStorageUsage(GB)": {Maximum: f(1200)},
	}))
	if got == nil || *got != 2000 {
		t.Errorf("local+remote = %v, want 2000", got)
	}
	// Local only (non-tiered) still counts.
	got = storedGB(mk(map[string]types.MetricAggregate{"TotalLocalStorageUsage(GB)": {Maximum: f(500)}}))
	if got == nil || *got != 500 {
		t.Errorf("local only = %v, want 500", got)
	}
}

func TestHasLongRetention(t *testing.T) {
	cfg := func(ms string) map[string]*string { return map[string]*string{"retention.ms": &ms} }
	mk := func(d ...types.TopicDetails) report.ProcessedCluster {
		return report.ProcessedCluster{KafkaAdminClientInformation: types.KafkaAdminClientInformation{Topics: &types.Topics{Details: d}}}
	}
	if hasLongRetention(report.ProcessedCluster{}) != nil {
		t.Error("no topics -> nil")
	}
	if got := hasLongRetention(mk(types.TopicDetails{Configurations: cfg("604800000")})); got == nil || *got != "No" {
		t.Errorf("7-day retention -> No, got %v", got)
	}
	if got := hasLongRetention(mk(types.TopicDetails{Configurations: cfg("-1")})); got == nil || *got != "Yes" {
		t.Errorf("infinite retention -> Yes, got %v", got)
	}
	if got := hasLongRetention(mk(types.TopicDetails{Configurations: cfg("7776000000")})); got == nil || *got != "Yes" { // 90 days
		t.Errorf("90-day retention -> Yes, got %v", got)
	}
}

// Schema Registry: Glue is derived outright; Confluent narrows to the edition
// question; nothing detected asks the full question. (ask only what we don't know)
func TestSchemaRegistryDerivation(t *testing.T) {
	c := provisionedCluster("c", "us-east-1", 100)

	// Glue detected -> derived, no schema-registry question asked.
	p := buildProfile(c, IntakeInputs{}, "glue", false)
	if p.SourceSRType != engineSRGlue {
		t.Errorf("glue should be derived, got %q", p.SourceSRType)
	}
	qs := resolveQuestions(p, IntakeInputs{})
	if find(qs, "schema_registry") != nil || find(qs, "schema_registry_edition") != nil {
		t.Error("glue: no schema-registry question should be asked")
	}

	// Confluent detected -> only the edition question.
	p = buildProfile(c, IntakeInputs{}, "confluent", false)
	qs = resolveQuestions(p, IntakeInputs{})
	if find(qs, "schema_registry") != nil {
		t.Error("confluent: full schema_registry should not be asked")
	}
	if q := find(qs, "schema_registry_edition"); q == nil || q.Status != "open_required" {
		t.Errorf("confluent: edition question should be asked, got %+v", q)
	}

	// Nothing detected -> full question.
	p = buildProfile(c, IntakeInputs{}, "", false)
	qs = resolveQuestions(p, IntakeInputs{})
	if find(qs, "schema_registry") == nil {
		t.Error("no SR detected: full schema_registry should be asked")
	}
	if find(qs, "schema_registry_edition") != nil {
		t.Error("no SR detected: edition should not be asked")
	}
}

func TestSourceAuthsDetected_SCRAM(t *testing.T) {
	c := provisionedCluster("c", "us-east-1", 100) // Provisioned + SCRAM
	auths := sourceAuthsDetected(c)
	if len(auths) != 1 || auths[0] != SourceAuthSCRAM {
		t.Errorf("expected [%s], got %v", SourceAuthSCRAM, auths)
	}
}
