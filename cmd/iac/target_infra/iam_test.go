package targetinfra

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
)

// TestTargetInfraIAMFragmentsDisjointFromBase catches contributors adding an
// action to a variant's Additions that already exists in the shared base —
// duplication in the rendered doc is confusing and should be avoided by
// promoting the action into the base (or out of it) rather than listing it
// twice.
func TestTargetInfraIAMFragmentsDisjointFromBase(t *testing.T) {
	for name, additions := range map[string][]string{
		"enterprise": targetInfraEnterpriseAdditions,
		"dedicated":  targetInfraDedicatedAdditions,
	} {
		if overlap := iampolicy.Overlap(targetInfraBase, additions); len(overlap) > 0 {
			t.Errorf("%s additions overlap base: %v", name, overlap)
		}
	}
}

// TestTargetInfraIAMAnnotationGolden locks down the exact rendered markdown.
// To update: run `go test -run TestTargetInfraIAMAnnotationGolden -update`
// (the -update flag is honoured below) and review the diff.
var updateGolden = envFlag("UPDATE_GOLDEN")

func TestTargetInfraIAMAnnotationGolden(t *testing.T) {
	got := iamAnnotation()
	path := filepath.Join("testdata", "iam_annotation.golden.md")

	if updateGolden {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden file: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (set UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Fatalf("iamAnnotation() output differs from golden %s.\n"+
			"Set UPDATE_GOLDEN=1 to accept the new output after review.\n"+
			"--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

func envFlag(name string) bool {
	v, ok := os.LookupEnv(name)
	return ok && v != "" && v != "0" && v != "false"
}
