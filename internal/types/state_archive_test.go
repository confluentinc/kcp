package types

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// TestStateArchiveLoads loads every archived real kcp-state.json (one per release
// v0.4.0+) through the current loader and asserts each migrates + decodes and lands
// non-empty. This is the ground-truth backward-compat regression net: any change
// that breaks loading of a historical file fails here.
//
// $KCP_STATE_ARCHIVE is the SOLE trigger — it must never auto-run during a plain
// `go test ./...` (the archive lives in S3 / a local cache, not in CI). When the var is
// UNSET the test skips; when it is SET but does not point at a directory the test FAILS
// loudly (a misconfigured path must not masquerade as a clean skip). Run via
// `make test-state-archive`, which fetches the archive and sets the var for you.
//
// NOTE: with the v2 archive pinned, the v0.4.2–v0.7.1 subtests fail by design — their
// schema_registries is the old array form the loader can't yet read. They turn green
// automatically when the Plan 2 array→object upcaster lands; do not skip them.
func TestStateArchiveLoads(t *testing.T) {
	dir := os.Getenv("KCP_STATE_ARCHIVE")
	if dir == "" {
		t.Skip("KCP_STATE_ARCHIVE not set — run `make test-state-archive` (fetches the archive and runs this), or set KCP_STATE_ARCHIVE to an extracted archive dir")
	}
	if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
		t.Fatalf("KCP_STATE_ARCHIVE=%q is set but is not a directory (err: %v) — fetch/extract the archive first (`make fetch-state-archive`)", dir, err)
	}

	matches, err := filepath.Glob(filepath.Join(dir, "v*", "kcp-state.json"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) == 0 {
		t.Fatalf("no v*/kcp-state.json files under %s — is the archive extracted?", dir)
	}
	sort.Strings(matches)

	for _, path := range matches {
		version := filepath.Base(filepath.Dir(path))
		t.Run(version, func(t *testing.T) {
			data, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			st, err := NewStateFromBytes(data)
			if err != nil {
				t.Fatalf("load failed: %v", err)
			}

			regions, clusters := 0, 0
			if st.MSKSources != nil {
				regions = len(st.MSKSources.Regions)
				for _, r := range st.MSKSources.Regions {
					clusters += len(r.Clusters)
				}
			}
			oskClusters := 0
			if st.OSKSources != nil {
				oskClusters = len(st.OSKSources.Clusters)
			}
			// Guard against a silently-empty load (e.g. a migration that drops all
			// data without erroring): every archived scan has real content.
			if regions == 0 && oskClusters == 0 {
				t.Errorf("loaded but empty: no MSK regions and no OSK clusters")
			}
			t.Logf("loaded: msk_regions=%d msk_clusters=%d osk_clusters=%d", regions, clusters, oskClusters)
		})
	}
}
