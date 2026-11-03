// Command lint-emoji fails if any Go source contains an emoji (see CLAUDE.md
// "No emojis"). It checks the files given as arguments or, with none, every Go
// file under the current directory that git does not ignore.
package main

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"strings"
	"unicode"
)

// allowedPictographic carves structural glyphs back out of Extended_Pictographic.
// U+25AA is the cost report's markdown bullet.
var allowedPictographic = map[rune]bool{
	0x25AA: true,
}

// extraBanned covers code points kcp forbids that are not Extended_Pictographic:
// the keycap combiner and emoji variation selector (FE0F also catches keycap emoji,
// whose base is ASCII), the check-mark and ballot-x dingbats, and the regional
// indicators that form flag emoji.
var extraBanned = &unicode.RangeTable{
	R16: []unicode.Range16{
		{0x20E3, 0x20E3, 1},
		{0x2713, 0x2713, 1},
		{0x2717, 0x2718, 1},
		{0xFE0F, 0xFE0F, 1},
	},
	R32: []unicode.Range32{
		{0x1F1E6, 0x1F1FF, 1},
	},
}

func isEmoji(r rune) bool {
	if allowedPictographic[r] {
		return false
	}
	return unicode.Is(extendedPictographic, r) || unicode.Is(extraBanned, r)
}

// goFiles lists tracked and untracked-but-not-ignored Go files, so gitignored
// trees (node_modules, dist, scratch, ...) are never scanned.
func goFiles(stderr io.Writer) ([]string, error) {
	cmd := exec.Command("git", "ls-files", "-z", "--cached", "--others", "--exclude-standard", "--", "*.go")
	cmd.Stderr = stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git ls-files: %w", err)
	}
	return strings.FieldsFunc(string(out), func(r rune) bool { return r == 0 }), nil
}

// checkFile writes "path:line:col: ..." to out for each emoji in the file and
// returns the count. Column is the 1-based rune position within the line.
func checkFile(path string, out io.Writer) (int, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		// Deleted from the working tree but still in the index.
		return 0, nil
	}
	if err != nil {
		return 0, err
	}

	var count int
	line, col := 1, 0
	for _, r := range string(data) {
		if r == '\n' {
			line++
			col = 0
			continue
		}
		col++
		if isEmoji(r) {
			_, _ = fmt.Fprintf(out, "%s:%d:%d: emoji U+%04X is not allowed in kcp source\n", path, line, col, r)
			count++
		}
	}
	return count, nil
}

// run returns the process exit code: 0 clean, 1 emoji found, 2 error.
func run(files []string, stdout, stderr io.Writer) int {
	if len(files) == 0 {
		var err error
		if files, err = goFiles(stderr); err != nil {
			_, _ = fmt.Fprintf(stderr, "lint-emoji: %v\n", err)
			return 2
		}
	}

	var violations int
	for _, f := range files {
		n, err := checkFile(f, stdout)
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "lint-emoji: %v\n", err)
			return 2
		}
		violations += n
	}

	if violations > 0 {
		_, _ = fmt.Fprintf(stderr, "\nlint-emoji: found %d emoji in Go source; kcp output must be plain text (see CLAUDE.md \"No emojis\")\n", violations)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "lint-emoji: no emoji found (%d files checked)\n", len(files))
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
