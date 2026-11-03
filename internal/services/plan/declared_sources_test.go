package plan

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
)

// mskState is a minimal one-cluster MSK-scanned state (SourceType untagged, the
// scan's platform is Amazon MSK).
func mskState(name string) report.ProcessedState {
	return report.ProcessedState{Sources: []report.ProcessedSource{{
		MSKData: &report.ProcessedMSKSource{Regions: []report.ProcessedRegion{{
			Clusters: []report.ProcessedCluster{{Name: name}},
		}}},
	}}}
}

// oskState is a minimal one-cluster OSK-scanned state (SourceType "osk", the
// scan's platform is Apache Kafka).
func oskState(id string) report.ProcessedState {
	return report.ProcessedState{Sources: []report.ProcessedSource{{
		OSKData: &report.ProcessedOSKSource{Clusters: []report.ProcessedOSKCluster{{ID: id}}},
	}}}
}

func TestValidateDeclaredSources(t *testing.T) {
	tests := []struct {
		name     string
		declared DeclaredInputs
		state    report.ProcessedState
		scanless bool
		wantErrs []string // substrings every one of which must appear in some returned error
		wantN    int      // expected number of errors; -1 = don't check
	}{
		{
			name: "iam on confluent-platform declared per-cluster errors naming cluster source_platform",
			declared: declFor("osk-playground", IntakeInputs{
				SourcePlatform: enginePlatformCP,
				OvSourceAuth:   []string{engineAuthAWSIAM},
			}),
			state:    oskState("osk-playground"),
			wantErrs: []string{`cluster "osk-playground": source_auth "iam" is only offered for an Amazon MSK source, but this cluster's source is Confluent Platform (from cluster source_platform). Remove it or change the source.`},
			wantN:    1,
		},
		{
			name: "iam via defaults confluent-platform errors naming all_clusters.source_platform",
			declared: DeclaredInputs{
				Defaults: IntakeInputs{SourcePlatform: enginePlatformCP, OvSourceAuth: []string{engineAuthAWSIAM}},
				Clusters: map[string]DeclaredCluster{},
			},
			state:    oskState("osk-playground"),
			wantErrs: []string{`(from all_clusters.source_platform)`},
			wantN:    1,
		},
		{
			name:     "sasl-plain on an OSK scan with no declared source is OK",
			declared: declFor("osk-playground", IntakeInputs{OvSourceAuth: []string{engineAuthSASLPlain}}),
			state:    oskState("osk-playground"),
			wantN:    0,
		},
		{
			name:     "kerberos on an OSK scan with no declared source is OK",
			declared: declFor("osk-playground", IntakeInputs{OvSourceAuth: []string{"Kerberos (GSSAPI)"}}),
			state:    oskState("osk-playground"),
			wantN:    0,
		},
		{
			name:     "iam on an OSK scan errors naming scan",
			declared: declFor("osk-playground", IntakeInputs{OvSourceAuth: []string{engineAuthAWSIAM}}),
			state:    oskState("osk-playground"),
			wantErrs: []string{`(from scan)`},
			wantN:    1,
		},
		{
			name: "declared confluent-platform on an OSK scan is a refinement; kerberos is OK",
			declared: declFor("osk-playground", IntakeInputs{
				SourcePlatform: enginePlatformCP,
				OvSourceAuth:   []string{"Kerberos (GSSAPI)"},
			}),
			state: oskState("osk-playground"),
			wantN: 0,
		},
		{
			name:     "declared msk on an OSK scan is a contradiction",
			declared: declFor("osk-playground", IntakeInputs{SourcePlatform: enginePlatformMSK}),
			state:    oskState("osk-playground"),
			wantErrs: []string{`cluster "osk-playground": cluster source_platform declares source_platform "msk", but the scan detected Apache Kafka for this cluster. Remove it or change the source.`},
			wantN:    1,
		},
		{
			name:     "declared apache-kafka on an MSK scan is a contradiction",
			declared: declFor("alpha", IntakeInputs{SourcePlatform: enginePlatformOSK}),
			state:    mskState("alpha"),
			wantErrs: []string{`cluster "alpha": cluster source_platform declares source_platform "apache-kafka", but the scan detected Amazon MSK for this cluster. Remove it or change the source.`},
			wantN:    1,
		},
		{
			name:     "scanless with no declared source skips the option check",
			declared: declFor(ScanlessClusterName, IntakeInputs{OvSourceAuth: []string{engineAuthAWSIAM, engineAuthKerberos}}),
			state:    ScanlessState(),
			scanless: true,
			wantN:    0,
		},
		{
			name: "multiple violations across clusters are all reported",
			declared: DeclaredInputs{Clusters: map[string]DeclaredCluster{
				"osk-playground": {Inputs: IntakeInputs{SourcePlatform: enginePlatformCP, OvSourceAuth: []string{engineAuthAWSIAM}}},
				"alpha":          {Inputs: IntakeInputs{SourcePlatform: enginePlatformOSK}},
			}},
			state: combineStates(oskState("osk-playground"), mskState("alpha")),
			wantErrs: []string{
				`cluster "osk-playground": source_auth "iam" is only offered for`,
				`cluster "alpha": cluster source_platform declares source_platform "apache-kafka"`,
			},
			wantN: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateDeclaredSources(tt.declared, tt.state, tt.scanless)
			if tt.wantN >= 0 && len(got) != tt.wantN {
				t.Fatalf("got %d error(s), want %d: %v", len(got), tt.wantN, got)
			}
			for _, want := range tt.wantErrs {
				found := false
				for _, g := range got {
					if strings.Contains(g, want) {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected an error containing %q, got: %v", want, got)
				}
			}
		})
	}
}

// combineStates merges the Sources of several processed states into one, for
// tests that need more than one cluster across different source kinds.
func combineStates(states ...report.ProcessedState) report.ProcessedState {
	var out report.ProcessedState
	for _, s := range states {
		out.Sources = append(out.Sources, s.Sources...)
	}
	return out
}

// fromScanLine matches an active plan-inputs.yaml key line commented out as a
// scan fact (e.g. "    # source_auth: [sasl-plain]   # from scan (uncomment to
// override)"), but not the legend/header prose that merely mentions "from scan"
// (which never starts with a lowercase key immediately after the "# ").
var fromScanLine = regexp.MustCompile(`(?m)^(\s*)# ([a-z_]+:.*# from scan.*)$`)

// uncommentFromScanLines uncomments every scan-derived override line in a
// plan-inputs.yaml, so a round-trip test can assert that accepting the scan's own
// values back never errors.
func uncommentFromScanLines(data string) string {
	return fromScanLine.ReplaceAllString(data, "$1$2")
}

// TestValidateDeclaredSources_ExamplesRoundTrip guards the exact regression this
// validation was written to fix: uncommenting a `# ... # from scan` line in one of
// the committed example plan-inputs.yaml files must always be accepted, never
// rejected, no matter which source platform the example's scan is for.
func TestValidateDeclaredSources_ExamplesRoundTrip(t *testing.T) {
	loadScan := func(t *testing.T, file string) report.ProcessedState {
		t.Helper()
		scanBytes, err := os.ReadFile(filepath.Join(examplesRoot, file))
		if err != nil {
			t.Fatalf("read %s fixture: %v", file, err)
		}
		state, err := types.NewStateFromBytes(scanBytes)
		if err != nil {
			t.Fatalf("load %s fixture: %v", file, err)
		}
		return report.NewReportService().ProcessState(*state)
	}

	modes := []struct {
		name     string
		scanFile string // "" means scanless
	}{
		{name: "filled", scanFile: "demo-scan.json"},
		{name: "first-run", scanFile: "demo-scan.json"},
		{name: "no-scan"},
		{name: "osk-scan", scanFile: "demo-osk-scan.json"},
	}

	for _, m := range modes {
		t.Run(m.name, func(t *testing.T) {
			path := filepath.Join(examplesRoot, m.name, "plan-inputs.yaml")
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read %s: %v", path, err)
			}
			uncommented := uncommentFromScanLines(string(data))
			// A scanless example has no scan, so no "from scan" line exists to
			// uncomment; every scanned example must have at least one.
			if m.scanFile != "" && uncommented == string(data) {
				t.Fatalf("%s: no `# ... # from scan` line was uncommented; the fixture or the regex has drifted", m.name)
			}

			declared, warnings, err := ParseDeclaredInputs([]byte(uncommented))
			if err != nil {
				t.Fatalf("%s: parse: %v", m.name, err)
			}
			if len(warnings) != 0 {
				t.Fatalf("%s: uncommenting a scan-derived value must parse cleanly, got warnings: %v", m.name, warnings)
			}

			scanless := m.scanFile == ""
			var processed report.ProcessedState
			if scanless {
				processed = ScanlessState()
			} else {
				processed = loadScan(t, m.scanFile)
			}
			if errs := ValidateDeclaredSources(declared, processed, scanless); len(errs) != 0 {
				t.Errorf("%s: uncommenting a scan-derived value must never fail validation, got: %v", m.name, errs)
			}
		})
	}
}

// A declared fact about the source that doesn't apply to it (MSK Connect on a
// non-MSK source, tiered storage on MSK Serverless, source_cloud on MSK) is a hard
// error instead of being used by one run and dropped on the rewrite.
func TestValidateDeclaredSources_InapplicableSourceFacts(t *testing.T) {
	// msk_connect_present on a Confluent Platform source.
	errs := ValidateDeclaredSources(declFor("osk-playground", IntakeInputs{SourcePlatform: enginePlatformCP, OvMSKConnect: "Yes"}), oskState("osk-playground"), false)
	if len(errs) != 1 || !strings.Contains(errs[0], `cluster "osk-playground": msk_connect_present does not apply`) || !strings.Contains(errs[0], "Amazon MSK source") {
		t.Errorf("msk_connect_present on CP: got %v", errs)
	}
	// A "No" there is what kcp itself writes for a non-MSK source: not an error.
	if errs = ValidateDeclaredSources(declFor("osk-playground", IntakeInputs{SourcePlatform: enginePlatformCP, OvMSKConnect: "No"}), oskState("osk-playground"), false); len(errs) != 0 {
		t.Errorf("msk_connect_present: false on CP should be tolerated, got %v", errs)
	}
	if errs = ValidateDeclaredSources(DeclaredInputs{Defaults: IntakeInputs{OvMSKConnect: "No"}}, oskState("o"), false); len(errs) != 0 {
		t.Errorf("all_clusters msk_connect_present: false on an OSK fleet should be tolerated, got %v", errs)
	}
	// tiered_storage on MSK Serverless.
	errs = ValidateDeclaredSources(declFor("sl", IntakeInputs{OvClusterType: "Serverless", OvTiered: "No"}), mskState("sl"), false)
	if len(errs) != 1 || !strings.Contains(errs[0], "tiered_storage does not apply") || !strings.Contains(errs[0], "Serverless has no tiered storage") {
		t.Errorf("tiered_storage on Serverless: got %v", errs)
	}
	// source_cloud on an MSK scan.
	errs = ValidateDeclaredSources(declFor("m", IntakeInputs{SourceCloud: "Azure"}), mskState("m"), false)
	if len(errs) != 1 || !strings.Contains(errs[0], "source_cloud does not apply") {
		t.Errorf("source_cloud on MSK: got %v", errs)
	}
	// The same facts on a source they apply to are fine.
	if errs = ValidateDeclaredSources(declFor("m", IntakeInputs{OvMSKConnect: "No", OvTiered: "No"}), mskState("m"), false); len(errs) != 0 {
		t.Errorf("applicable facts should pass, got %v", errs)
	}
	// An all_clusters fact is fine while some cluster consumes it (mixed fleet) ...
	mixed := mskState("m")
	mixed.Sources = append(mixed.Sources, oskState("o").Sources...)
	if errs = ValidateDeclaredSources(DeclaredInputs{Defaults: IntakeInputs{OvMSKConnect: "No"}}, mixed, false); len(errs) != 0 {
		t.Errorf("all_clusters msk_connect_present in a mixed fleet should pass, got %v", errs)
	}
	// ... and an error only when no cluster does.
	errs = ValidateDeclaredSources(DeclaredInputs{Defaults: IntakeInputs{OvMSKConnect: "Yes"}}, oskState("o"), false)
	if len(errs) != 1 || !strings.Contains(errs[0], "all_clusters: msk_connect_present does not apply to any cluster") {
		t.Errorf("all_clusters msk_connect_present on an all-OSK fleet: got %v", errs)
	}
}

// Answers that stop applying as other answers change (here: connects_today and
// downtime_tolerance on a public-networking plan) are kept, annotated, instead of
// being deleted by the rewrite; they survive repeated rewrites.
func TestPlanInputs_KeepsUnusedAnswers(t *testing.T) {
	inputs := scanlessBase + "    exceeds_enterprise_limits: true\n"
	inputs = strings.Replace(inputs, "private_networking_required: true", "private_networking_required: false", 1)
	declared, _, err := ParseDeclaredInputs([]byte(inputs))
	if err != nil {
		t.Fatal(err)
	}
	yaml1 := RenderPlanInputsYAML(BuildEnginePlan(ScanlessState(), declared, "", fixedExampleClock))
	for _, want := range []string{"connects_today: privatelink   # not used for this plan", "downtime_tolerance: minutes   # not used for this plan", "exceeds_enterprise_limits: true   # not used for this plan"} {
		if !strings.Contains(yaml1, want) {
			t.Errorf("rewrite dropped an unused answer; missing %q in:\n%s", want, yaml1)
		}
	}
	declared2, warns, err := ParseDeclaredInputs([]byte(yaml1))
	if err != nil || len(warns) > 0 {
		t.Fatalf("rewrite does not parse cleanly: %v %v", err, warns)
	}
	yaml2 := RenderPlanInputsYAML(BuildEnginePlan(ScanlessState(), declared2, "", fixedExampleClock))
	declared3, _, _ := ParseDeclaredInputs([]byte(yaml2))
	if yaml3 := RenderPlanInputsYAML(BuildEnginePlan(ScanlessState(), declared3, "", fixedExampleClock)); yaml3 != yaml2 {
		t.Errorf("rewrite is not stable:\n%s", firstDiff(yaml2, yaml3))
	}
	for _, want := range []string{"connects_today: privatelink", "downtime_tolerance: minutes", "exceeds_enterprise_limits: true"} {
		if !strings.Contains(yaml2, want) {
			t.Errorf("second rewrite dropped %q", want)
		}
	}
}

// MSK Serverless is IAM-only: a non-IAM source_auth is rejected, IAM is fine, and
// provisioned MSK still accepts SCRAM.
func TestValidateDeclaredSources_ServerlessIsIAMOnly(t *testing.T) {
	errs := ValidateDeclaredSources(declFor("sl", IntakeInputs{OvClusterType: "Serverless", OvSourceAuth: []string{engineAuthSCRAM}}), mskState("sl"), false)
	if len(errs) != 1 || !strings.Contains(errs[0], "source_auth scram does not apply") || !strings.Contains(errs[0], "IAM-only") {
		t.Errorf("scram on Serverless: got %v", errs)
	}
	if errs = ValidateDeclaredSources(declFor("sl", IntakeInputs{OvClusterType: "Serverless", OvSourceAuth: []string{engineAuthAWSIAM}}), mskState("sl"), false); len(errs) != 0 {
		t.Errorf("iam on Serverless should pass, got %v", errs)
	}
	if errs = ValidateDeclaredSources(declFor("m", IntakeInputs{OvSourceAuth: []string{engineAuthSCRAM}}), mskState("m"), false); len(errs) != 0 {
		t.Errorf("scram on provisioned MSK should pass, got %v", errs)
	}
}

// Migrating schemas out of a source with no Schema Registry contradicts itself, so it is
// a hard error rather than a Schema verdict that stays pending under a ready plan.
func TestValidateDeclaredSources_SchemaMigrateWithNoRegistry(t *testing.T) {
	errs := ValidateDeclaredSources(declFor("m", IntakeInputs{SourceSRType: engineSRNone, SchemaStrategy: engineSchemaStrategyMigrate}), mskState("m"), false)
	if len(errs) != 1 || !strings.Contains(errs[0], `cluster "m": schema_strategy migrate needs a source Schema Registry, but schema_registry is none`) {
		t.Errorf("none + migrate: got %v", errs)
	}
	for _, in := range []IntakeInputs{
		{SourceSRType: engineSRNone, SchemaStrategy: "Stay schemaless"},
		{SourceSRType: engineSRGlue, SchemaStrategy: engineSchemaStrategyMigrate},
		{SchemaStrategy: engineSchemaStrategyMigrate}, // registry type still open
	} {
		if errs := ValidateDeclaredSources(declFor("m", in), mskState("m"), false); len(errs) != 0 {
			t.Errorf("%+v should be accepted, got %v", in, errs)
		}
	}
}
