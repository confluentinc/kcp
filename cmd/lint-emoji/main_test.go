package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestIsEmoji is the spec for what counts as an emoji. Banned runes are the
// pictographic glyphs the project removed; allowed runes are the structural
// glyphs kcp uses for terminal layout, plus ordinary ASCII. Rune literals are
// written as numeric code points so this test file stays free of emoji bytes.
func TestIsEmoji(t *testing.T) {
	banned := map[string]rune{
		"magnifying glass":           0x1F50D,
		"red circle":                 0x1F534,
		"green square":               0x1F7E2,
		"regional indicator (flag)":  0x1F1EC,
		"warning sign":               0x26A0,
		"check mark button":          0x2705,
		"cross mark":                 0x274C,
		"check mark":                 0x2713,
		"ballot x":                   0x2717,
		"information source":         0x2139,
		"white medium star":          0x2B50,
		"keycap combining enclosure": 0x20E3,
		"emoji variation selector":   0xFE0F,
	}
	for name, r := range banned {
		if !isEmoji(r) {
			t.Errorf("U+%04X (%s) should be flagged as an emoji", r, name)
		}
	}

	allowed := map[string]rune{
		"letter a":               'a',
		"space":                  ' ',
		"hash (keycap base)":     '#',
		"asterisk (keycap base)": '*',
		"digit 0":                '0',
		"digit 9":                '9',
		"hyphen":                 '-',
		"leftwards arrow":        0x2190,
		"rightwards arrow":       0x2192, // tree arrow used by discover/scan
		"downwards arrow w/ tip": 0x21B3, // tree continuation arrow
		"box down and right":     0x250C,
		"box horizontal":         0x2500,
		"box vertical":           0x2502,
		"box up and right":       0x2514,
		"lower one eighth block": 0x2581, // sparkline
		"full block":             0x2588, // sparkline
		"partial diff":           0x2202, // math
		"less-than-or-equal":     0x2264, // math
		"greater-than-or-equal":  0x2265, // math
		"n-ary summation":        0x2211, // math
	}
	for name, r := range allowed {
		if isEmoji(r) {
			t.Errorf("U+%04X (%s) is a structural/ASCII glyph and must NOT be flagged", r, name)
		}
	}
}

// TestCheckFileFlagsEmoji verifies that checkFile counts every emoji rune,
// including base+variation-selector sequences. Emoji are written as Go escapes
// so this source file contains no emoji bytes.
func TestCheckFileFlagsEmoji(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sample.go")
	// magnifier (1 rune) + warning-sign + selector (2 runes) + check-mark (1 rune) = 4.
	// Built from code points so this source file itself contains no emoji bytes.
	content := "package x\n" +
		"// magnifier " + string(rune(0x1F50D)) + " in a comment\n" +
		"var s = \"warn " + string(rune(0x26A0)) + string(rune(0xFE0F)) +
		" done " + string(rune(0x2705)) + "\"\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if want := 4; got != want {
		t.Fatalf("checkFile counted %d emoji, want %d", got, want)
	}
}

// TestCheckFileAllowsStructuralGlyphs verifies the allowed structural glyphs
// (tree arrows, box-drawing, sparklines, math) never trip the checker, even
// when present as real bytes in a file.
func TestCheckFileAllowsStructuralGlyphs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.go")
	content := "package x\n// tree: → ↳  box: ┌─┐  bars: ▁█  math: ≤ ≥\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got, err := checkFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("checkFile counted %d emoji, want 0 (structural glyphs must be allowed)", got)
	}
}

// TestSkipDir verifies the lint surface: the frontend, docs, vendored/generated
// and local-only trees, and hidden directories are excluded; ordinary source
// directories are scanned.
func TestSkipDir(t *testing.T) {
	skipped := map[string]string{
		"cmd/ui/frontend":              "frontend",
		"vendor":                       "vendor",
		"docs":                         "docs",
		"dist":                         "dist",
		"site":                         "site",
		"scratch":                      "scratch",
		"local":                        "local",
		".git":                         ".git",
		".venv-docs":                   ".venv-docs",
		"cmd/ui/frontend/node_modules": "node_modules",
	}
	for rel, name := range skipped {
		if !skipDir(rel, name) {
			t.Errorf("skipDir(%q, %q) = false, want true", rel, name)
		}
	}

	scanned := map[string]string{
		"cmd":                    "cmd",
		"cmd/gen-docs":           "gen-docs",
		"internal/services/plan": "plan",
		".":                      ".",
	}
	for rel, name := range scanned {
		if skipDir(rel, name) {
			t.Errorf("skipDir(%q, %q) = true, want false", rel, name)
		}
	}
}
