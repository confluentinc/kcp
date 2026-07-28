package types

import (
	"crypto/sha256"
	"encoding/hex"
	"reflect"
	"sort"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/state/migrate"
)

// schemaShapes pins the reflected shape hash of each schema_version as of the PR
// that introduced it. Entries are APPEND-ONLY and IMMUTABLE: to change the
// on-disk shape you add a NEW entry and bump migrate.CurrentSchemaVersion in the
// same change — you never edit an existing entry. Editing one is a
// backward-compatibility break (see the CLAUDE.md "State files" procedure).
//
// This complements TestStateSchemaSnapshot (which shows a readable diff of WHAT
// changed) by making a shape change impossible to land without also bumping the
// version — otherwise TestCurrentSchemaShapeMatchesEntry goes red.
var schemaShapes = map[int]string{
	1: "sha256:720619a5a172c612894076b92921683302818ad1c02372310e3e2e4291c81660",
}

// schemaFloor is the first versioned schema.
const schemaFloor = 1

// shapeHash is the canonical fingerprint of a state schema: sha256 of the sorted
// json field-path list (the exact content walkSchema produces for the golden).
func shapeHash(t reflect.Type) string {
	var lines []string
	walkSchema(t, "", map[reflect.Type]bool{}, &lines)
	sort.Strings(lines)
	sum := sha256.Sum256([]byte(strings.Join(lines, "\n")))
	return "sha256:" + hex.EncodeToString(sum[:])
}

// TestCurrentSchemaShapeMatchesEntry forces a version bump on any shape change:
// the current shape must equal its own frozen schemaShapes entry, so changing
// types.State without bumping migrate.CurrentSchemaVersion (and adding a new
// entry) goes red.
func TestCurrentSchemaShapeMatchesEntry(t *testing.T) {
	v := migrate.CurrentSchemaVersion
	want, ok := schemaShapes[v]
	if !ok {
		t.Fatalf("no schemaShapes entry for CurrentSchemaVersion %d — add schemaShapes[%d] with the current shape hash", v, v)
	}
	if got := shapeHash(reflect.TypeOf(State{})); got != want {
		t.Fatalf("shape of schema_version %d changed (got %s, want %s).\n"+
			"Do NOT edit the existing entry. This is a schema change — bump "+
			"migrate.CurrentSchemaVersion, add an upcaster + fixture, regenerate the "+
			"golden, and add a NEW schemaShapes entry for the new version.", v, got, want)
	}
}

// TestSchemaShapesHaveNoGaps ensures every version from the floor to the current
// has a frozen entry, so a bump can't land without recording its shape.
func TestSchemaShapesHaveNoGaps(t *testing.T) {
	for v := schemaFloor; v <= migrate.CurrentSchemaVersion; v++ {
		if _, ok := schemaShapes[v]; !ok {
			t.Errorf("schema_version %d has no schemaShapes entry — record its shape hash", v)
		}
	}
}
