package main

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

// TestIsEmoji is the spec for what counts as an emoji. Rune literals are numeric
// code points so this file stays free of emoji bytes.
func TestIsEmoji(t *testing.T) {
	banned := map[string]rune{
		"magnifying glass":           0x1F50D,
		"red circle":                 0x1F534,
		"green circle":               0x1F7E2,
		"regional indicator (flag)":  0x1F1EC, // not EP, covered by extraBanned
		"warning sign":               0x26A0,
		"check mark button":          0x2705,
		"cross mark":                 0x274C,
		"heavy check mark":           0x2714,
		"check mark (dingbat)":       0x2713, // not EP, covered by extraBanned
		"ballot x":                   0x2717, // not EP, covered by extraBanned
		"information source":         0x2139,
		"white medium star":          0x2B50,
		"hourglass with sand":        0x23F3, // Miscellaneous Technical block
		"pause button":               0x23F8, // Miscellaneous Technical block
		"trade mark":                 0x2122,
		"copyright":                  0x00A9,
		"keycap combining enclosure": 0x20E3, // not EP, covered by extraBanned
		"emoji variation selector":   0xFE0F, // not EP, covered by extraBanned
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
		"partial diff":           0x2202,
		"less-than-or-equal":     0x2264,
		"greater-than-or-equal":  0x2265,
		"n-ary summation":        0x2211,
		"black circle bullet":    0x25CF, // lag-check TUI bullet
		"black small square":     0x25AA, // EP, but allow-listed
	}
	for name, r := range allowed {
		if isEmoji(r) {
			t.Errorf("U+%04X (%s) is a structural/ASCII glyph and must NOT be flagged", r, name)
		}
	}
}

func writeFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestCheckFileFlagsEmoji counts every emoji rune, including base+variation-selector
// sequences: magnifier (1) + warning sign + FE0F (2) + check mark button (1) = 4.
func TestCheckFileFlagsEmoji(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sample.go")
	writeFile(t, path, "package x\n"+
		"// magnifier "+string(rune(0x1F50D))+" in a comment\n"+
		"var s = \"warn "+string(rune(0x26A0))+string(rune(0xFE0F))+
		" done "+string(rune(0x2705))+"\"\n")

	var out bytes.Buffer
	got, err := checkFile(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got != 4 {
		t.Fatalf("checkFile counted %d emoji, want 4", got)
	}
	if lines := strings.Count(strings.TrimSpace(out.String()), "\n") + 1; lines != 4 {
		t.Fatalf("checkFile printed %d report lines, want 4:\n%s", lines, out.String())
	}
}

func TestCheckFileReportsLineAndColumn(t *testing.T) {
	path := filepath.Join(t.TempDir(), "loc.go")
	// Line 2, rune column 6: "//abc" then the emoji.
	writeFile(t, path, "package x\n//abc"+string(rune(0x1F50D))+"\n")

	var out bytes.Buffer
	if _, err := checkFile(path, &out); err != nil {
		t.Fatal(err)
	}
	want := path + ":2:6: emoji U+1F50D is not allowed in kcp source"
	if got := strings.TrimSpace(out.String()); got != want {
		t.Fatalf("checkFile report =\n  %q\nwant\n  %q", got, want)
	}
}

func TestCheckFileAllowsStructuralGlyphs(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ok.go")
	writeFile(t, path, "package x\n// tree: → ↳  box: ┌─┐  bars: ▁█  math: ≤ ≥  bullets: ● ▪\n")

	var out bytes.Buffer
	got, err := checkFile(path, &out)
	if err != nil {
		t.Fatal(err)
	}
	if got != 0 {
		t.Fatalf("checkFile counted %d emoji, want 0:\n%s", got, out.String())
	}
}

func TestCheckFileSkipsMissingFile(t *testing.T) {
	got, err := checkFile(filepath.Join(t.TempDir(), "deleted.go"), io.Discard)
	if err != nil || got != 0 {
		t.Fatalf("checkFile on a missing file = (%d, %v), want (0, nil)", got, err)
	}
}

func TestRunExitCodes(t *testing.T) {
	dir := t.TempDir()
	clean := filepath.Join(dir, "clean.go")
	bad1 := filepath.Join(dir, "bad1.go")
	bad2 := filepath.Join(dir, "bad2.go")
	writeFile(t, clean, "package x\n// arrows → ↳ are fine\n")
	writeFile(t, bad1, "package x\n// "+string(rune(0x1F50D))+"\n")
	writeFile(t, bad2, "package x\n// "+string(rune(0x26A0))+"\n")

	t.Run("clean returns 0", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{clean}, &out, &errb); code != 0 {
			t.Fatalf("run = %d, want 0\nstdout: %s\nstderr: %s", code, out.String(), errb.String())
		}
		if !strings.Contains(out.String(), "no emoji found") {
			t.Fatalf("stdout = %q, want it to mention 'no emoji found'", out.String())
		}
	})

	t.Run("violations accumulate and return 1", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{bad1, clean, bad2}, &out, &errb); code != 1 {
			t.Fatalf("run = %d, want 1", code)
		}
		if !strings.Contains(errb.String(), "found 2 emoji") {
			t.Fatalf("stderr = %q, want it to mention 'found 2 emoji'", errb.String())
		}
	})

	t.Run("read error returns 2", func(t *testing.T) {
		var out, errb bytes.Buffer
		if code := run([]string{dir}, &out, &errb); code != 2 {
			t.Fatalf("run on a directory = %d, want 2", code)
		}
	})
}

func TestGoFilesHonoursGitignore(t *testing.T) {
	if _, err := exec.LookPath("git"); err != nil {
		t.Skip("git not on PATH")
	}
	t.Chdir(t.TempDir())
	git := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", args...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	git("init", "-q")
	writeFile(t, ".gitignore", "scratch/\n")
	writeFile(t, "tracked.go", "package x\n")
	writeFile(t, "pkg/untracked.go", "package x\n")
	writeFile(t, "scratch/ignored.go", "package x\n")
	writeFile(t, "notes.txt", "not go\n")
	git("add", "tracked.go")

	got, err := goFiles(io.Discard)
	if err != nil {
		t.Fatal(err)
	}
	slices.Sort(got)
	if want := []string{"pkg/untracked.go", "tracked.go"}; !slices.Equal(got, want) {
		t.Fatalf("goFiles = %q, want %q", got, want)
	}
}
