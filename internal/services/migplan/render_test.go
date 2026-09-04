package migplan

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/fatih/color"
)

func TestRenderReport(t *testing.T) {
	color.NoColor = true // deterministic, plain output
	r := reconcile.Report{
		Preconditions: []reconcile.PreconditionResult{
			{Name: "route is dynamic", OK: true},
			{Name: "offset sync disabled", OK: false, Detail: "sync is enabled"},
		},
		Migratable: []reconcile.TopicVerdict{{Topic: "orders"}},
		Unchanged:  []reconcile.TopicVerdict{{Topic: "legacy"}},
		FailFast:   []reconcile.TopicVerdict{{Topic: "broken", Reason: "F3: not on the cluster link"}},
		Warnings:   []string{"shadowed condition for x"},
	}
	var buf bytes.Buffer
	RenderReport(&buf, r)
	out := buf.String()
	for _, want := range []string{
		"route is dynamic", "offset sync disabled", "sync is enabled",
		"orders", "legacy", "broken", "F3: not on the cluster link", "shadowed condition for x",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("report output missing %q; got:\n%s", want, out)
		}
	}
}

func TestWriteArtifacts(t *testing.T) {
	dir := t.TempDir()
	a := &reconcile.Artifacts{
		Topics:          []string{"a", "b"},
		FenceRules:      []byte("fencing: []\n"),
		SwitchoverRules: []byte("routing: {}\n"),
	}
	if err := WriteArtifacts(dir, a); err != nil {
		t.Fatal(err)
	}
	tj, err := os.ReadFile(filepath.Join(dir, "topics.json"))
	if err != nil {
		t.Fatal(err)
	}
	var topics []string
	if err := json.Unmarshal(tj, &topics); err != nil {
		t.Fatalf("topics.json is not a JSON array: %v", err)
	}
	if len(topics) != 2 || topics[0] != "a" {
		t.Errorf("topics.json = %v, want [a b]", topics)
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "fence-rules.yaml")); !strings.Contains(string(b), "fencing") {
		t.Error("fence-rules.yaml missing or wrong content")
	}
	if b, _ := os.ReadFile(filepath.Join(dir, "switchover-rules.yaml")); !strings.Contains(string(b), "routing") {
		t.Error("switchover-rules.yaml missing or wrong content")
	}
}

func TestWriteArtifactsNil(t *testing.T) {
	if err := WriteArtifacts(t.TempDir(), nil); err == nil {
		t.Fatal("expected an error writing nil artifacts")
	}
}
