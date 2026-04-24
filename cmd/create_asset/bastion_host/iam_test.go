package bastion_host

import (
	"os"
	"path/filepath"
	"testing"
)

// TestBastionHostIAMAnnotationGolden locks down the rendered markdown for
// `kcp create-asset bastion-host`. Set UPDATE_GOLDEN=1 to refresh after
// an intentional change.
func TestBastionHostIAMAnnotationGolden(t *testing.T) {
	got := bastionHostIAMAnnotation()
	path := filepath.Join("testdata", "iam_annotation.golden.md")

	if envFlag("UPDATE_GOLDEN") {
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
		t.Fatalf("bastionHostIAMAnnotation() output differs from golden %s.\n"+
			"Set UPDATE_GOLDEN=1 to accept the new output after review.\n"+
			"--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

func envFlag(name string) bool {
	v, ok := os.LookupEnv(name)
	return ok && v != "" && v != "0" && v != "false"
}
