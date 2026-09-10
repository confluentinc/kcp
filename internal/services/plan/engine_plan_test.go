package plan

import (
	"os"
	"strings"
	"testing"
	"time"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// declFor wraps a single cluster's declared inputs.
func declFor(cluster string, in IntakeInputs) DeclaredInputs {
	return DeclaredInputs{Clusters: map[string]DeclaredCluster{cluster: {Inputs: in}}}
}

// provisionedCluster is a minimal Provisioned+SCRAM cluster for fleet tests.
func provisionedCluster(name, region string, partitions int) report.ProcessedCluster {
	return report.ProcessedCluster{
		Name:   name,
		Region: region,
		AWSClientInformation: types.AWSClientInformation{
			MskClusterConfig: kafkatypes.Cluster{
				ClusterType: kafkatypes.ClusterTypeProvisioned,
				Provisioned: &kafkatypes.Provisioned{
					ClientAuthentication: &kafkatypes.ClientAuthentication{
						Sasl: &kafkatypes.Sasl{Scram: &kafkatypes.Scram{Enabled: bp(true)}},
					},
				},
			},
		},
		KafkaAdminClientInformation: types.KafkaAdminClientInformation{
			Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: partitions}},
		},
	}
}

func TestRenderPlanInputs_LayeredMultiCluster(t *testing.T) {
	state := report.ProcessedState{
		Timestamp: time.Now(),
		Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{
						provisionedCluster("alpha", "us-east-1", 3000),
						provisionedCluster("beta", "us-east-1", 4000),
					},
				}},
			},
		}},
	}
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(state, DeclaredInputs{}, "kcp-state.json", fixed)
	if len(ep.Clusters) != 2 {
		t.Fatalf("clusters=%d, want 2", len(ep.Clusters))
	}
	yaml := RenderPlanInputsYAML(ep)

	// Every question is listed once under all_clusters.
	if !strings.Contains(yaml, "\nall_clusters:\n") {
		t.Errorf("multi-cluster output must have an all_clusters block:\n%s", yaml)
	}
	// A genuinely-required question renders as an active `# not set` line.
	// move_existing_data always changes the plan (start-fresh vs a data migration), so
	// it stays required here.
	if !strings.Contains(yaml, "move_existing_data:   # not set") {
		t.Errorf("a required question should be an active `# not set` line under all_clusters:\n%s", yaml)
	}
	// Both clusters are size-forced to Enterprise (band 2, above the shared-tier max),
	// so private_networking_required is inert — the tier is private either way. It must
	// be a commented, answerable knob, never a blocking `# not set`.
	if strings.Contains(yaml, "private_networking_required:   # not set") {
		t.Errorf("private_networking_required is inert for a size-forced fleet; it must not block as `# not set`:\n%s", yaml)
	}
	if !strings.Contains(yaml, "# private_networking_required:   # optional here — doesn't change the plan") {
		t.Errorf("an inert required question should render as a commented, answerable knob:\n%s", yaml)
	}
	// source_cluster_type is a read-only scan fact: it appears per cluster in the
	// read-only reference line (not advertised as an editable override), once each.
	if strings.Count(yaml, "source_cluster_type = provisioned") != 2 {
		t.Errorf("each cluster should list its read-only scan facts for audit:\n%s", yaml)
	}
	// A read-only fact is not offered as an editable override anywhere.
	if strings.Contains(yaml, "# source_cluster_type: provisioned   # from scan") || strings.Contains(yaml, "# source_cluster_type:   # from scan") {
		t.Errorf("a read-only scan fact must not be advertised as an override:\n%s", yaml)
	}
	// Single-cluster output must NOT emit an all_clusters block.
	single := RenderPlanInputsYAML(&EnginePlan{Clusters: ep.Clusters[:1]})
	if strings.Contains(single, "all_clusters:") {
		t.Errorf("single-cluster output must stay flat (no all_clusters):\n%s", single)
	}
}

func TestFilterState(t *testing.T) {
	state := report.ProcessedState{
		Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{
					{Name: "us-east-1", Clusters: []report.ProcessedCluster{
						provisionedCluster("alpha", "us-east-1", 100),
						provisionedCluster("beta", "us-east-1", 100),
					}},
					{Name: "eu-west-1", Clusters: []report.ProcessedCluster{
						provisionedCluster("gamma", "eu-west-1", 100),
					}},
				},
			},
		}},
	}
	// No filter → everything.
	if _, n := FilterState(state, "", ""); n != 3 {
		t.Errorf("no filter matched %d, want 3", n)
	}
	// By cluster id.
	if got, n := FilterState(state, "beta", ""); n != 1 || collectClusters(got)[0].Name != "beta" {
		t.Errorf("cluster filter matched %d (%+v), want 1 beta", n, collectClusters(got))
	}
	// By region.
	if got, n := FilterState(state, "", "us-east-1"); n != 2 || countRegions(got) != 1 {
		t.Errorf("region filter matched %d clusters / %d regions, want 2 / 1", n, countRegions(got))
	}
	// No match.
	if _, n := FilterState(state, "nope", ""); n != 0 {
		t.Errorf("bad filter matched %d, want 0", n)
	}
}

// twoClusterState is a minimal 2-cluster Provisioned+SCRAM fleet for round-trip
// and layered-rendering tests.
func twoClusterState() report.ProcessedState {
	return report.ProcessedState{
		Timestamp: time.Now(),
		Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{
						provisionedCluster("alpha", "us-east-1", 3000),
						provisionedCluster("beta", "us-east-1", 3000),
					},
				}},
			},
		}},
	}
}

// T1: a parse -> build -> render -> re-parse cycle must preserve the customer's
// answers, and a single cluster's answer must NOT leak to the fleet (H2).
func TestRoundTrip_PreservesAnswersNoLeak(t *testing.T) {
	state := twoClusterState()
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }

	in := `
defaults:
  private_networking_required: true
clusters:
  alpha:
    downtime_tolerance: zero
`
	d1, w1, err := ParseDeclaredInputs([]byte(in))
	if err != nil || len(w1) != 0 {
		t.Fatalf("initial parse: err=%v warnings=%v", err, w1)
	}
	ep1 := BuildEnginePlan(state, d1, "s.json", fixed)
	rendered := RenderPlanInputsYAML(ep1)

	d2, w2, err := ParseDeclaredInputs([]byte(rendered))
	if err != nil {
		t.Fatalf("re-parse rendered YAML: %v", err)
	}
	if len(w2) != 0 {
		t.Errorf("re-parsing kcp's own output should produce no warnings, got: %v\n%s", w2, rendered)
	}
	// Fleet default preserved.
	if d2.Defaults.PublicEndpointsOK != "No" {
		t.Errorf("defaults private_networking_required lost on round-trip: %+v", d2.Defaults)
	}
	// alpha's per-cluster answer preserved...
	if got := d2.For("alpha").DowntimeTolerance; got != "Zero downtime" {
		t.Errorf("alpha downtime_tolerance lost on round-trip: %q", got)
	}
	// ...and NOT leaked to beta (the H2 guard).
	if got := d2.For("beta").DowntimeTolerance; got != "" {
		t.Errorf("beta must not inherit alpha's downtime override (H2 leak): %q", got)
	}
	// beta still inherits the genuine fleet default.
	if got := d2.For("beta").PublicEndpointsOK; got != "No" {
		t.Errorf("beta should inherit the fleet networking default: %q", got)
	}
}

// A customer's override of a scan fact must survive regeneration: it is written
// live (not re-commented), so a later run still honors it (M5 override case).
func TestRoundTrip_ScanFactOverridePersists(t *testing.T) {
	state := twoClusterState()
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }

	in := `
clusters:
  alpha:
    kafka_version: older      # override the scanned version
`
	d1, w1, err := ParseDeclaredInputs([]byte(in))
	if err != nil || len(w1) != 0 {
		t.Fatalf("parse: err=%v warnings=%v", err, w1)
	}
	if got := d1.For("alpha").OvKafkaVersion; got != "Older than 2.4" {
		t.Fatalf("override not parsed: %q", got)
	}
	ep := BuildEnginePlan(state, d1, "s.json", fixed)
	rendered := RenderPlanInputsYAML(ep)

	// The override must be written LIVE (not commented out).
	if !strings.Contains(rendered, "kafka_version: older") || strings.Contains(rendered, "# kafka_version: older") {
		t.Fatalf("overridden scan fact should render live, not commented:\n%s", rendered)
	}
	// And it must round-trip.
	d2, _, err := ParseDeclaredInputs([]byte(rendered))
	if err != nil {
		t.Fatal(err)
	}
	if got := d2.For("alpha").OvKafkaVersion; got != "Older than 2.4" {
		t.Errorf("scan-fact override lost on round-trip: %q", got)
	}
	// A non-overridden read-only scan fact stays commented, in the read-only
	// reference (never advertised as an editable override).
	if !strings.Contains(rendered, "# source_cluster_type = ") {
		t.Errorf("non-overridden read-only scan facts should stay in the read-only reference:\n%s", rendered)
	}
}

// Optional questions with no default (the multis target_auth / eos_streams) must
// NOT be listed under the REQUIRED header in the defaults block.
func TestFleetDefaults_OptionalMultiNotRequired(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(twoClusterState(), DeclaredInputs{}, "s.json", fixed)
	out := RenderPlanInputsYAML(ep)

	optIdx := strings.Index(out, "Optional: a default applies")
	if optIdx < 0 {
		t.Fatalf("no Optional section:\n%s", out)
	}
	requiredSection := out[:optIdx]
	if strings.Contains(requiredSection, "target_auth:") || strings.Contains(requiredSection, "eos_streams:") {
		t.Errorf("optional multis must not appear under REQUIRED:\n%s", requiredSection)
	}
	if !strings.Contains(out, "eos_streams: []") {
		t.Errorf("an unanswered optional multi should render as []:\n%s", out)
	}
}

// A multi-valued fleet answer set in different orders on different clusters is the
// same answer — it stays in defaults, not written per-cluster (order-insensitive).
func TestFleetDefaults_MultiOrderInsensitive(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	in := "clusters:\n  alpha:\n    target_auth: [api-keys, oauth]\n  beta:\n    target_auth: [oauth, api-keys]\n"
	d, _, err := ParseDeclaredInputs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out := RenderPlanInputsYAML(BuildEnginePlan(twoClusterState(), d, "s.json", fixed))

	clustersSection := out[strings.Index(out, "\nclusters:"):]
	// A commented "# target_auth:" override placeholder is fine (inert on re-parse);
	// only an ACTIVE (uncommented) per-cluster override would break round-trip.
	if strings.Contains(clustersSection, "\n    target_auth:") {
		t.Errorf("a reordered-but-equal multi must not produce an active per-cluster override:\n%s", clustersSection)
	}
	if !strings.Contains(out, "target_auth: [") {
		t.Errorf("the shared multi answer should be promoted to defaults:\n%s", out)
	}
}

// A multi-valued scan-fact override (source_auth) must survive regeneration live.
func TestRoundTrip_MultiScanOverridePersists(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	in := "clusters:\n  alpha:\n    source_auth: [mtls, scram]\n"
	d, _, err := ParseDeclaredInputs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	out := RenderPlanInputsYAML(BuildEnginePlan(twoClusterState(), d, "s.json", fixed))
	if strings.Contains(out, "# source_auth: [mtls, scram]") {
		t.Fatalf("an overridden multi scan fact must render live, not commented:\n%s", out)
	}
	d2, _, err := ParseDeclaredInputs([]byte(out))
	if err != nil {
		t.Fatal(err)
	}
	got := d2.For("alpha").OvSourceAuth
	if len(got) != 2 {
		t.Errorf("multi scan override lost on round-trip: %v", got)
	}
}

// A scan-fact key under `defaults:` is accepted (a fleet-wide override of the
// scan) — no warning, and the value applies to every cluster.
func TestParseDeclaredInputs_ScanKeyUnderDefaultsApplies(t *testing.T) {
	in := "defaults:\n  source_auth: [scram]\n"
	d, warnings, err := ParseDeclaredInputs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	if len(d.Defaults.OvSourceAuth) == 0 {
		t.Errorf("scan key under defaults should apply fleet-wide, got %v", d.Defaults.OvSourceAuth)
	}
	for _, w := range warnings {
		if strings.Contains(w, "source_auth") {
			t.Errorf("did not expect a warning for a scan key under defaults, got %q", w)
		}
	}
}

// A malformed `applications:` (not a map, or an app value that isn't a map) is
// reported rather than silently dropped (L4).
func TestParseDeclaredInputs_WarnsMalformedApplications(t *testing.T) {
	in := "clusters:\n  alpha:\n    applications: not-a-map\n"
	_, warnings, err := ParseDeclaredInputs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range warnings {
		if strings.Contains(w, "alpha:") && strings.Contains(w, "applications") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a malformed-applications warning, got %v", warnings)
	}

	in2 := "clusters:\n  alpha:\n    applications:\n      payments: not-a-map\n"
	_, warnings2, err := ParseDeclaredInputs([]byte(in2))
	if err != nil {
		t.Fatal(err)
	}
	found = false
	for _, w := range warnings2 {
		if strings.Contains(w, "payments") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a malformed-app-value warning, got %v", warnings2)
	}
}

// T2: an unknown/misspelled key under a cluster or defaults is reported, not
// silently dropped (H1).
func TestParseDeclaredInputs_WarnsUnknownKeys(t *testing.T) {
	in := `
defaults:
  private_networking_required: true
  downtime_tolerence: minutes      # typo -> should warn
clusters:
  alpha:
    not_a_real_key: x              # unknown -> should warn
`
	_, warnings, err := ParseDeclaredInputs([]byte(in))
	if err != nil {
		t.Fatal(err)
	}
	var sawDefaultsTypo, sawClusterUnknown bool
	for _, w := range warnings {
		if strings.Contains(w, "all_clusters:") && strings.Contains(w, "downtime_tolerence") {
			sawDefaultsTypo = true
		}
		if strings.Contains(w, "alpha:") && strings.Contains(w, "not_a_real_key") {
			sawClusterUnknown = true
		}
	}
	if !sawDefaultsTypo {
		t.Errorf("expected a warning for the defaults-level typo; got %v", warnings)
	}
	if !sawClusterUnknown {
		t.Errorf("expected a warning for the cluster-level unknown key; got %v", warnings)
	}
}

// A verdict contingent on an unanswered required question is held (Pending); one
// determined by the scan regardless (size forces Enterprise) is shown.
func TestContingency_ClusterTypeHeldOnlyWhenNotForced(t *testing.T) {
	fixed := func() time.Time { return time.Date(2026, 9, 8, 0, 0, 0, 0, time.UTC) }
	state := func(partitions int) report.ProcessedState {
		return report.ProcessedState{Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{Regions: []report.ProcessedRegion{{
				Name: "us-east-1", Clusters: []report.ProcessedCluster{provisionedCluster("c", "us-east-1", partitions)},
			}}},
		}}}
	}
	// Band 1 (small) with nothing answered: cluster type hinges on private networking.
	small := BuildEnginePlan(state(100), DeclaredInputs{}, "s.json", fixed).Clusters[0]
	if keys := small.Contingent[nodeClusterType]; len(keys) == 0 {
		t.Errorf("Band-1 cluster type should be contingent on private networking, got %v", small.Contingent)
	} else if !containsStr(keys, "private_networking_required") {
		t.Errorf("expected private_networking_required to decide cluster type, got %v", keys)
	}
	// Band 2 (large): Enterprise regardless of the private answer — not contingent.
	big := BuildEnginePlan(state(6000), DeclaredInputs{}, "s.json", fixed).Clusters[0]
	if keys := big.Contingent[nodeClusterType]; len(keys) != 0 {
		t.Errorf("size-forced cluster type should not be contingent, got %v", keys)
	}
	if big.Plan.ClusterType.Value != engine.TierEnterprise {
		t.Errorf("Band-2 should be Enterprise, got %q", big.Plan.ClusterType.Value)
	}
}

func TestBuildEnginePlan_PerAppPlans(t *testing.T) {
	yaml := `
defaults:
  private_networking_required: true
  connects_today: same-vpc
  use_case_breadth: few-teams
clusters:
  orders:
    downtime_tolerance: minutes        # cluster default for its apps
    move_existing_data: true
    client_coordination: hard          # app-scoped cluster default its apps inherit
    applications:
      payments:
        downtime_tolerance: zero       # app override
      search:
        move_existing_data: false      # this app starts fresh
        connects_today: peered         # INFRA key under an app -> ignored+warned
`
	tmp := t.TempDir() + "/pi.yaml"
	if err := os.WriteFile(tmp, []byte(yaml), 0o644); err != nil {
		t.Fatal(err)
	}
	declared, warnings, err := LoadDeclaredInputs(tmp)
	if err != nil {
		t.Fatal(err)
	}
	// The infra key placed under an app must be reported (and dropped).
	foundInfraWarn := false
	for _, w := range warnings {
		if strings.Contains(w, "search") && strings.Contains(w, "infrastructure setting") {
			foundInfraWarn = true
		}
	}
	if !foundInfraWarn {
		t.Errorf("expected an infra-key-under-app warning, got %v", warnings)
	}

	state := report.ProcessedState{
		Timestamp: time.Now(),
		Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name:     "us-east-1",
					Clusters: []report.ProcessedCluster{provisionedCluster("orders", "us-east-1", 3000)},
				}},
			},
		}},
	}
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(state, declared, "kcp-state.json", fixed)

	cp := ep.Clusters[0]
	if len(cp.Apps) != 2 {
		t.Fatalf("apps=%d, want 2 (payments, search)", len(cp.Apps))
	}
	// Infra verdicts are identical across the cluster and every app (infra reads
	// zero app fields).
	for _, ap := range cp.Apps {
		if ap.Plan.ClusterType.Value != cp.Plan.ClusterType.Value {
			t.Errorf("app %s cluster type %q != cluster %q", ap.Name, ap.Plan.ClusterType.Value, cp.Plan.ClusterType.Value)
		}
		if ap.Plan.Networking.Value != cp.Plan.Networking.Value {
			t.Errorf("app %s networking %q != cluster %q", ap.Name, ap.Plan.Networking.Value, cp.Plan.Networking.Value)
		}
		if ap.Plan.Auth.Value != cp.Plan.Auth.Value {
			t.Errorf("app %s auth %q != cluster %q", ap.Name, ap.Plan.Auth.Value, cp.Plan.Auth.Value)
		}
	}
	// App verdicts differ: payments (zero downtime) vs search (fresh start).
	byName := map[string]AppPlan{}
	for _, ap := range cp.Apps {
		byName[ap.Name] = ap
	}
	if byName["payments"].Plan.Switchover.Value == byName["search"].Plan.Switchover.Value &&
		byName["payments"].Plan.HistoricalData.Value == byName["search"].Plan.HistoricalData.Value {
		t.Errorf("expected the two apps' switchover/historical verdicts to differ:\n payments=%+v\n search=%+v",
			byName["payments"].Plan.Switchover.Value, byName["search"].Plan.Switchover.Value)
	}
	// search declares only app-scoped keys after the infra key was dropped.
	for _, q := range byName["search"].Questions {
		if !appQuestionKeys[q.Key] {
			t.Errorf("app questions should be app-scoped only, got %q", q.Key)
		}
	}
	// Answer-source attribution: an app's own override reads "app"; a value it
	// inherited from the cluster reads "cluster".
	if q := find(byName["payments"].Questions, "downtime_tolerance"); q == nil || q.Source != "app" {
		t.Errorf("payments downtime_tolerance should be sourced from app, got %+v", q)
	}
	// search starts fresh, so the Cluster-Linking-only downtime question doesn't apply.
	if q := find(byName["search"].Questions, "downtime_tolerance"); q != nil {
		t.Errorf("search (start fresh) should not be asked downtime_tolerance, got %+v", q)
	}
	// A value inherited from the cluster reads "cluster".
	if q := find(byName["search"].Questions, "client_coordination"); q == nil || q.Source != "cluster" {
		t.Errorf("search client_coordination should be inherited from cluster, got %+v", q)
	}
	if q := find(byName["search"].Questions, "move_existing_data"); q == nil || q.Source != "app" {
		t.Errorf("search move_existing_data should be sourced from app, got %+v", q)
	}
}

func TestBuildEnginePlan_OneCluster(t *testing.T) {
	state := report.ProcessedState{
		Timestamp: time.Now(),
		Sources: []report.ProcessedSource{{
			MSKData: &report.ProcessedMSKSource{
				Regions: []report.ProcessedRegion{{
					Name: "us-east-1",
					Clusters: []report.ProcessedCluster{{
						Name:   "prod",
						Region: "us-east-1",
						AWSClientInformation: types.AWSClientInformation{
							MskClusterConfig: kafkatypes.Cluster{
								ClusterType: kafkatypes.ClusterTypeProvisioned,
								Provisioned: &kafkatypes.Provisioned{
									ClientAuthentication: &kafkatypes.ClientAuthentication{
										Sasl: &kafkatypes.Sasl{Scram: &kafkatypes.Scram{Enabled: bp(true)}},
									},
								},
							},
						},
						KafkaAdminClientInformation: types.KafkaAdminClientInformation{
							Topics: &types.Topics{Summary: types.TopicSummary{TotalPartitions: 5000}},
						},
					}},
				}},
			},
		}},
	}

	inputs := IntakeInputs{
		PublicEndpointsOK: "No", ConnectsToday: "Same VPC",
		UseCaseBreadth: "One team, one application", NeedsDataMigration: "Yes",
		DowntimeTolerance: "Minutes per service", AnyAppNeedsDataMigration: "No",
	}
	fixed := func() time.Time { return time.Date(2026, 9, 2, 0, 0, 0, 0, time.UTC) }
	ep := BuildEnginePlan(state, declFor("prod", inputs), "kcp-state.json", fixed)

	if len(ep.Clusters) != 1 {
		t.Fatalf("clusters=%d, want 1", len(ep.Clusters))
	}
	cp := ep.Clusters[0]
	if cp.ClusterID != "prod" || cp.Region != "us-east-1" {
		t.Errorf("cluster summary = %+v", cp)
	}
	if cp.Plan.ClusterType.Value != engine.TierEnterprise {
		t.Errorf("private Band 2: cluster type=%s, want Enterprise", cp.Plan.ClusterType.Value)
	}
	if cp.Plan.Withheld {
		t.Errorf("clean plan must not be withheld")
	}

	js, err := RenderEnginePlanJSON(ep)
	if err != nil {
		t.Fatalf("render json: %v", err)
	}
	for _, want := range []string{`"cluster_id": "prod"`, `"Enterprise"`, enginePlanSchemaVersion} {
		if !strings.Contains(js, want) {
			t.Errorf("json missing %q", want)
		}
	}
}

// ValidateDeclaredClusters flags declared cluster keys that match no scanned
// cluster (a misspelled cluster name), and passes correctly spelled ones.
func TestValidateDeclaredClusters(t *testing.T) {
	state := report.ProcessedState{Sources: []report.ProcessedSource{{
		MSKData: &report.ProcessedMSKSource{Regions: []report.ProcessedRegion{{
			Name: "us-east-1", Clusters: []report.ProcessedCluster{provisionedCluster("orders", "us-east-1", 100)},
		}}},
	}}}

	// A correctly spelled key produces no error.
	if errs := ValidateDeclaredClusters(declFor("orders", IntakeInputs{}), state); len(errs) != 0 {
		t.Errorf("valid cluster key should not error, got %v", errs)
	}
	// A misspelled key is reported by name.
	errs := ValidateDeclaredClusters(declFor("orderz", IntakeInputs{}), state)
	if len(errs) != 1 || !strings.Contains(errs[0], `"orderz"`) || !strings.Contains(errs[0], "not in the scan") {
		t.Errorf("misspelled cluster key should be reported, got %v", errs)
	}
	// No declared clusters, no errors.
	if errs := ValidateDeclaredClusters(DeclaredInputs{}, state); len(errs) != 0 {
		t.Errorf("empty declared inputs should not error, got %v", errs)
	}
}
