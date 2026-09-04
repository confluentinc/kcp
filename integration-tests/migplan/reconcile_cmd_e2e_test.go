//go:build e2e

package migplan_e2e

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"testing"

	cmdreconcile "github.com/confluentinc/kcp/cmd/migration/reconcile"
	"github.com/goccy/go-yaml"
)

// TestReconcileCommandArtifactsReflectManifest drives the WHOLE user-facing path
// — the real `kcp migration reconcile` cobra command: manifest load → live
// providers → engine → WriteArtifacts to disk — against the docker env, then
// asserts the three written artifacts faithfully encode the operator's declared
// intent from the manifest:
//
//   - topics.json is the resolved selector (the promote list the caller feeds to
//     cluster-link promotion);
//   - both rules artifacts are wrapped under a top-level rules: key;
//   - the fence artifact fences exactly the selected batch;
//   - the switchover artifact routes exactly that batch to the manifest's
//     target streaming-domain (cc), and nothing else.
//
// This validates that the artifacts REFLECT the manifest change. Whether they
// APPLY correctly against a running gateway (routing actually flips) is not
// covered here — a static gateway fixture cannot observe applied routing; that
// needs a live topic-based-routing gateway.
func TestReconcileCommandArtifactsReflectManifest(t *testing.T) {
	out := t.TempDir()

	cmd := cmdreconcile.NewMigrationReconcileCmd()
	cmd.SetOut(&bytes.Buffer{})
	cmd.SetErr(&bytes.Buffer{})
	cmd.SetArgs([]string{
		"--migration-yaml", "testdata/manifest.yaml",
		"--gateway-config", "testdata/gateway.yaml",
		"--route", "migration-route",
		"--target-domain", "cc",
		"--topics", "team-a.orders,team-a.payments,billing-v2",
		"--out-dir", out,
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("reconcile command failed: %v", err)
	}

	want := []string{"billing-v2", "team-a.orders", "team-a.payments"} // sorted

	// topics.json — the promote list the caller feeds to cluster-link promotion.
	var topics []string
	readJSON(t, filepath.Join(out, "topics.json"), &topics)
	if !reflect.DeepEqual(topics, want) {
		t.Errorf("topics.json = %v, want %v", topics, want)
	}

	// fence-rules.yaml — whole rules subtree; fences exactly the batch.
	fence := readRulesSubtree(t, filepath.Join(out, "fence-rules.yaml"))
	if got := firstFencingTopics(t, fence); !reflect.DeepEqual(got, want) {
		t.Errorf("fence topics = %v, want %v", got, want)
	}

	// switchover-rules.yaml — routes exactly the batch to the manifest's target (cc).
	sw := readRulesSubtree(t, filepath.Join(out, "switchover-rules.yaml"))
	domain, condTopics := firstSwitchCondition(t, sw)
	if domain != "cc" {
		t.Errorf("switchover routes to %q, want the manifest target domain cc", domain)
	}
	if !reflect.DeepEqual(condTopics, want) {
		t.Errorf("switchover condition topics = %v, want %v", condTopics, want)
	}
}

func readJSON(t *testing.T, path string, v any) {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	if err := json.Unmarshal(b, v); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
}

// readRulesSubtree reads an artifact and returns the subtree under its required
// top-level rules: key (also asserting that wrapper exists).
func readRulesSubtree(t *testing.T, path string) map[string]any {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	var doc map[string]any
	if err := yaml.Unmarshal(b, &doc); err != nil {
		t.Fatalf("unmarshal %s: %v", path, err)
	}
	rules, ok := doc["rules"].(map[string]any)
	if !ok {
		t.Fatalf("%s must be wrapped under a top-level rules: key:\n%s", path, b)
	}
	return rules
}

func firstFencingTopics(t *testing.T, rules map[string]any) []string {
	t.Helper()
	fencing, ok := rules["fencing"].([]any)
	if !ok || len(fencing) == 0 {
		t.Fatalf("rules.fencing missing or empty: %+v", rules)
	}
	first, _ := fencing[0].(map[string]any)
	return sortedStrings(first["topics"])
}

func firstSwitchCondition(t *testing.T, rules map[string]any) (string, []string) {
	t.Helper()
	routing, _ := rules["routing"].(map[string]any)
	conds, ok := routing["conditions"].([]any)
	if !ok || len(conds) == 0 {
		t.Fatalf("rules.routing.conditions missing or empty: %+v", rules)
	}
	first, _ := conds[0].(map[string]any)
	domain, _ := first["streamingDomain"].(string)
	return domain, sortedStrings(first["topics"])
}

func sortedStrings(v any) []string {
	raw, _ := v.([]any)
	out := make([]string, 0, len(raw))
	for _, e := range raw {
		if s, ok := e.(string); ok {
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}
