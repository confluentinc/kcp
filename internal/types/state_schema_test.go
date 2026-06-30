package types

import (
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

var updateSchemaGolden = false // flip to true locally to regenerate, then back

// walkSchema returns sorted "path:jsontag" lines for every json-tagged field
// reachable from t, so any add/remove/rename/re-parent shows up as a diff.
func walkSchema(t reflect.Type, prefix string, seen map[reflect.Type]bool, out *[]string) {
	for t.Kind() == reflect.Ptr || t.Kind() == reflect.Slice || t.Kind() == reflect.Array {
		t = t.Elem()
	}
	if t.Kind() == reflect.Map {
		walkSchema(t.Elem(), prefix+"[]", seen, out)
		return
	}
	if t.Kind() != reflect.Struct {
		return
	}
	// Treat time.Time as a leaf.
	if t == reflect.TypeOf(time.Time{}) {
		return
	}
	if seen[t] {
		return
	}
	seen[t] = true
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		tag := f.Tag.Get("json")
		if tag == "" || tag == "-" {
			continue
		}
		name := strings.Split(tag, ",")[0]
		path := name
		if prefix != "" {
			path = prefix + "." + name
		}
		*out = append(*out, path)
		walkSchema(f.Type, path, seen, out)
	}
}

func TestStateSchemaSnapshot(t *testing.T) {
	var lines []string
	walkSchema(reflect.TypeOf(State{}), "", map[reflect.Type]bool{}, &lines)
	sort.Strings(lines)
	got := strings.Join(lines, "\n") + "\n"

	golden := filepath.Join("testdata", "state_schema.golden")
	if updateSchemaGolden {
		if err := os.WriteFile(golden, []byte(got), 0644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
	}
	want, err := os.ReadFile(golden)
	if err != nil {
		t.Fatalf("read golden (set updateSchemaGolden=true once to create it): %v", err)
	}
	if got != string(want) {
		t.Fatalf("kcp-state.json schema changed.\nIf this is intentional you MUST:\n"+
			"  1. bump migrate.CurrentSchemaVersion\n"+
			"  2. add an upcaster in internal/state/migrate/steps.go\n"+
			"  3. add a fixture in internal/state/migrate/testdata\n"+
			"  4. regenerate this golden (updateSchemaGolden=true once)\n\n--- got ---\n%s\n--- want ---\n%s", got, want)
	}
}
