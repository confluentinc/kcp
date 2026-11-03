package noemoji

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode"
)

// emojiBlocks are the Unicode blocks emoji are drawn from, plus the two
// combiners that turn a plain character into one. The structural glyphs kcp
// uses (arrows, box drawing, block elements, geometric shapes, math) all sit
// outside them.
var emojiBlocks = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x20E3, 0x20E3, 1}, // combining enclosing keycap
		{0x23E9, 0x23FA, 1}, // media-control emoji in Miscellaneous Technical
		{0x2600, 0x27BF, 1}, // Miscellaneous Symbols, Dingbats
		{0x2B00, 0x2BFF, 1}, // Miscellaneous Symbols and Arrows
		{0xFE0F, 0xFE0F, 1}, // emoji presentation selector
	},
	R32: []unicode.Range32{
		{0x1F000, 0x1FAFF, 1}, // supplementary pictographs, incl. flags and skin tones
	},
}

// TestNoEmojiInGoSource scans every Go file git does not ignore. go test's
// result cache does not track the git file list, so a newly added file alone
// can hit a cached pass: run with -count=1 wherever this gates a change.
func TestNoEmojiInGoSource(t *testing.T) {
	root := strings.TrimSpace(git(t, ".", "rev-parse", "--show-toplevel"))
	files := strings.FieldsFunc(
		git(t, root, "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go"),
		func(r rune) bool { return r == 0 },
	)
	if len(files) == 0 {
		t.Fatal("git listed no Go files")
	}

	for _, f := range files {
		data, err := os.ReadFile(filepath.Join(root, f))
		if errors.Is(err, fs.ErrNotExist) {
			continue // deleted from the working tree but still in the index
		}
		if err != nil {
			t.Fatal(err)
		}
		line, col := 1, 0
		for _, r := range string(data) {
			if r == '\n' {
				line++
				col = 0
				continue
			}
			col++
			if unicode.Is(emojiBlocks, r) {
				t.Errorf("%s:%d:%d: emoji U+%04X; kcp output must be plain text (see CLAUDE.md \"No emojis\")", f, line, col, r)
			}
		}
	}
}

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.Output()
	if err != nil {
		if exitErr, ok := errors.AsType[*exec.ExitError](err); ok {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, exitErr.Stderr)
		}
		t.Fatalf("git %s: %v", strings.Join(args, " "), err)
	}
	return string(out)
}
