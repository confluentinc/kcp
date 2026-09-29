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
			name: "iam via defaults confluent-platform errors naming defaults.source_platform",
			declared: DeclaredInputs{
				Defaults: IntakeInputs{SourcePlatform: enginePlatformCP, OvSourceAuth: []string{engineAuthAWSIAM}},
				Clusters: map[string]DeclaredCluster{},
			},
			state:    oskState("osk-playground"),
			wantErrs: []string{`(from defaults.source_platform)`},
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
			declared: declFor(ScanlessClusterName, IntakeInputs{OvSourceAuth: []string{engineAuthAWSIAM}}),
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
