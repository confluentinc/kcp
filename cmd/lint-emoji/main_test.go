package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestIsEmoji is the spec for what counts as an emoji. Banned runes are the
// pictographic glyphs the project removed plus the emoji-only components and the
// check/cross dingbats kcp forbids; allowed runes are the structural glyphs kcp
// uses for terminal layout, plus ordinary ASCII. Rune literals are written as
// numeric code points so this test file stays free of emoji bytes.
func TestIsEmoji(t *testing.T) {
	banned := map[string]rune{
		"magnifying glass":           0x1F50D,
		"red circle":                 0x1F534,
		"green circle":               0x1F7E2,
		"regional indicator (flag)":  0x1F1EC, // Emoji_Component, not EP — covered by extraBanned
		"warning sign":               0x26A0,
		"check mark button":          0x2705,
		"cross mark":                 0x274C,
		"heavy check mark":           0x2714,
		"check mark (dingbat)":       0x2713, // not EP — covered by extraBanned
		"ballot x":                   0x2717, // not EP — covered by extraBanned
		"information source":         0x2139,
		"white medium star":          0x2B50,
		"hourglass with sand":        0x23F3, // Miscellaneous Technical — the old range-table gap
		"pause button":               0x23F8, // Miscellaneous Technical — the old range-table gap
		"trade mark":                 0x2122,
		"copyright":                  0x00A9,
		"keycap combining enclosure": 0x20E3, // not EP — covered by extraBanned
		"emoji variation selector":   0xFE0F, // not EP — covered by extraBanned
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
		"black circle bullet":    0x25CF, // bullet used by the lag-check TUI
		"black small square":     0x25AA, // Extended_Pictographic, but allow-listed (cost-report bullet)
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

	var out bytes.Buffer
	got, err := checkFile(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if want := 4; got != want {
		t.Fatalf("checkFile counted %d emoji, want %d", got, want)
	}
	if lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1; lines != 4 {
		t.Fatalf("checkFile printed %d report lines, want 4:\n%s", lines, out.String())
	}
}

// TestCheckFileReportsLineAndColumn pins the "path:line:col: ..." diagnostic so an
// off-by-one in line/column tracking is caught.
func TestCheckFileReportsLineAndColumn(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "loc.go")
	// The magnifier sits on line 2, at rune column 4 ("abc" then the emoji).
	content := "package x\n" + "//abc" + string(rune(0x1F50D)) + "\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if _, err := checkFile(path, &out); err != nil {
		t.Fatal(err)
	}
	want := path + ":2:6: emoji U+1F50D is not allowed in kcp source"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("checkFile report =\n  %q\nwant\n  %q", got, want)
	}
}

// TestCheckFileAllowsStructuralGlyphs verifies the allowed structural glyphs
// (tree arrows, box-drawing, sparklines, math, and the ●/▪ bullets) never trip the
// checker, even when present as real bytes in a file.
func TestCheckFileAllowsStructuralGlyphs(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "ok.go")
	content := "package x\n// tree: → ↳  box: ┌─┐  bars: ▁█  math: ≤ ≥  bullets: ● ▪\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	got, err := checkFile(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("checkFile counted %d emoji, want 0 (structural glyphs must be allowed):\n%s", got, out.String())
	}
}

// TestCheckFileIgnoresNonUTF8 verifies a non-UTF-8 (binary) file is skipped rather
// than mis-scanned or erroring.
func TestCheckFileIgnoresNonUTF8(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.go")
	if err := os.WriteFile(path, []byte{0xff, 0xfe, 0x00, 0x01}, 0o644); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	got, err := checkFile(path, &out)
	if err != nil {
		t.Fatalf("checkFile on non-UTF-8 returned error: %v", err)
	}
	if got != 0 {
		t.Fatalf("checkFile counted %d emoji in non-UTF-8 file, want 0", got)
	}
}

// TestSkipDir verifies the lint surface: the frontend (by exact path and by
// suffix), the docs site and local-only trees (anchored to the scan root), and
// vendored/generated and hidden dirs are excluded; ordinary source directories —
// including the real cmd/docs Go package that merely shares the "docs" name — are
// scanned.
func TestSkipDir(t *testing.T) {
	skipped := []struct{ rel, name string }{
		{"cmd/ui/frontend", "frontend"},                  // exact-path arm
		{"/abs/repo/cmd/ui/frontend", "frontend"},        // HasSuffix arm (absolute root)
		{"vendor", "vendor"},                             // vendored, top level
		{"internal/x/vendor", "vendor"},                  // vendored, nested
		{"cmd/ui/frontend/node_modules", "node_modules"}, // generated, nested
		{"dist", "dist"},                                 // build output
		{"docs", "docs"},                                 // docs site, top level
		{"site", "site"},                                 // docs build, top level
		{"scratch", "scratch"},                           // local-only
		{"local", "local"},                               // local-only
		{".git", ".git"},                                 // hidden
		{".venv-docs", ".venv-docs"},                     // hidden
	}
	for _, c := range skipped {
		if !skipDir(c.rel, c.name) {
			t.Errorf("skipDir(%q, %q) = false, want true", c.rel, c.name)
		}
	}

	scanned := []struct{ rel, name string }{
		{"cmd/docs", "docs"},               // real Go package sharing the docs name — MUST be scanned
		{"cmd", "cmd"},                     //
		{"cmd/gen-docs", "gen-docs"},       //
		{"internal/services/plan", "plan"}, //
		{".", "."},                         // scan root
	}
	for _, c := range scanned {
		if skipDir(c.rel, c.name) {
			t.Errorf("skipDir(%q, %q) = true, want false", c.rel, c.name)
		}
	}
}

// TestRunExitCodes verifies run's contract: 0 clean, 1 emoji found, 2 walk error,
// and that violations accumulate across multiple roots.
func TestRunExitCodes(t *testing.T) {
	writeGo := func(dir, name, body string) {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("clean returns 0", func(t *testing.T) {
		dir := t.TempDir()
		writeGo(dir, "clean.go", "package x\n// arrows → ↳ are fine\n")
		var out, errb bytes.Buffer
		if code := run([]string{dir}, &out, &errb); code != 0 {
			t.Fatalf("run = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
		}
		if !strings.Contains(out.String(), "no emoji found") {
			t.Fatalf("clean run stdout = %q, want it to mention 'no emoji found'", out.String())
		}
	})

	t.Run("emoji returns 1", func(t *testing.T) {
		dir := t.TempDir()
		writeGo(dir, "bad.go", "package x\n// oops "+string(rune(0x1F50D))+"\n")
		var out, errb bytes.Buffer
		if code := run([]string{dir}, &out, &errb); code != 1 {
			t.Fatalf("run = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "found 1 emoji") {
			t.Fatalf("stderr = %q, want it to mention 'found 1 emoji'", errb.String())
		}
	})

	t.Run("missing root returns 2", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{filepath.Join(t.TempDir(), "does-not-exist")}, &out, &errb); code != 2 {
			t.Fatalf("run = %d, want 2", code)
		}
	})

	t.Run("violations accumulate across roots", func(t *testing.T) {
		d1, d2 := t.TempDir(), t.TempDir()
		writeGo(d1, "a.go", "package x\n// "+string(rune(0x1F50D))+"\n")
		writeGo(d2, "b.go", "package x\n// "+string(rune(0x26A0))+"\n")
		var out, errb bytes.Buffer
		if code := run([]string{d1, d2}, &out, &errb); code != 1 {
			t.Fatalf("run = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "found 2 emoji") {
			t.Fatalf("stderr = %q, want it to mention 'found 2 emoji'", errb.String())
		}
	})
}

// TestRunPrunesExcludedTrees verifies the walk actually skips the frontend and
// vendored trees (emoji inside them are not reported) while scanning ordinary
// packages, including a cmd/docs package that shares the excluded "docs" name.
func TestRunPrunesExcludedTrees(t *testing.T) {
	root := t.TempDir()
	mk := func(rel, body string) {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	emoji := "package x\n// " + string(rune(0x1F50D)) + "\n"
	clean := "package x\n// fine\n"
	mk("cmd/ui/frontend/app.go", emoji) // excluded: frontend
	mk("vendor/dep.go", emoji)          // excluded: vendored
	mk("docs/gen.go", emoji)            // excluded: top-level docs site
	mk("cmd/docs/cmd_docs.go", clean)   // scanned: real package sharing the name
	mk("internal/svc.go", clean)        // scanned

	var out, errb bytes.Buffer
	if code := run([]string{root}, &out, &errb); code != 0 {
		t.Fatalf("run = %d, want 0 (excluded trees must be pruned)\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
	}
}
