// Command lint-emoji fails if any Go source in the repository contains an emoji.
//
// kcp output must be plain text (see CLAUDE.md "No emojis"): the ban covers log
// lines, terminal narrative, error strings, generated artifacts, and code
// comments. This checker guards the Go source against regressions and is wired
// into `make lint`, the pre-commit hook, and CI static analysis. It is not a
// user-facing kcp subcommand — invoke via `make lint` or, directly,
// `go run ./cmd/lint-emoji [path ...]` (defaults to the current directory).
//
// It flags pictographic emoji only. The non-pictographic structural glyphs that
// kcp is allowed to use for terminal layout are deliberately NOT flagged: tree
// arrows (Arrows block, U+2190..U+21FF), box-drawing (U+2500..U+257F), block and
// sparkline elements (U+2580..U+259F), and math operators (U+2200..U+22FF, e.g.
// the less/greater-than-or-equal signs). None of those blocks appear in the
// emoji range table below.
//
// The frontend (React/TypeScript), docs, and vendored/generated/local-only trees
// are outside the lint surface — same spirit as .golangci.yml's exclusions.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"
)

// emojiRanges lists the inclusive rune ranges treated as emoji. They cover the
// astral emoji planes plus the BMP symbol and dingbat blocks that hold the
// glyphs the project removed (magnifier, warning sign, check/cross marks,
// information source, coloured circles/squares). The ranges are chosen so they
// never overlap the structural glyphs kcp is allowed to use — arrows, box
// drawing, block/sparkline elements, and math operators all live in U+2190..
// U+259F and U+2200..U+22FF, none of which is listed here.
var emojiRanges = [...][2]rune{
	{0x1F000, 0x1FAFF}, // astral emoji: emoticons, transport, symbols and pictographs (+Extended-A/B), regional-indicator flags, playing cards
	{0x2600, 0x27BF},   // Miscellaneous Symbols (U+2600..U+26FF) + Dingbats (U+2700..U+27BF): warning sign, check/cross marks, etc.
	{0x2B00, 0x2BFF},   // Miscellaneous Symbols and Arrows: emoji-style stars/arrows/squares
	{0x2139, 0x2139},   // information source (Letterlike Symbols)
	{0x20E3, 0x20E3},   // combining enclosing keycap (keycap emoji)
	{0xFE0F, 0xFE0F},   // emoji-presentation variation selector (turns a neutral base glyph into an emoji)
}

// isEmoji reports whether r falls in one of the emoji ranges.
func isEmoji(r rune) bool {
	for _, rg := range emojiRanges {
		if r >= rg[0] && r <= rg[1] {
			return true
		}
	}
	return false
}

// skipDir reports whether a directory is outside the lint surface, given its
// repo-relative slash path and base name. Hidden directories (.git, .venv-docs,
// .cache, .worktrees, .idea, ...) are always skipped, along with the frontend,
// docs, and vendored/generated/local-only trees.
func skipDir(rel, name string) bool {
	if rel == "cmd/ui/frontend" || strings.HasSuffix(rel, "/cmd/ui/frontend") {
		return true // React/TypeScript frontend — excluded, same as .golangci.yml
	}
	if name != "." && strings.HasPrefix(name, ".") {
		return true // hidden directories
	}
	switch name {
	case "vendor", "node_modules", "dist", "site", "docs", "scratch", "local":
		return true
	}
	return false
}

// checkFile scans a single file for emoji, printing "file:line:col: ..." for
// each and returning the count. Column is a 1-based rune position within the
// line, so an editor jump lands on (or just before) the offending glyph.
func checkFile(path string) (int, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0, err
	}
	if !utf8.Valid(data) {
		return 0, nil // non-UTF-8 (binary): nothing to check
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
			_, _ = fmt.Printf("%s:%d:%d: emoji U+%04X is not allowed in kcp source\n", path, line, col, r)
			count++
		}
	}
	return count, nil
}

func main() {
	roots := os.Args[1:]
	if len(roots) == 0 {
		roots = []string{"."}
	}

	var violations, filesScanned int
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel := filepath.ToSlash(path)
			if d.IsDir() {
				if skipDir(rel, d.Name()) {
					return fs.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(path, ".go") {
				return nil
			}
			filesScanned++
			n, err := checkFile(path)
			violations += n
			return err
		})
		if err != nil {
			_, _ = fmt.Fprintf(os.Stderr, "lint-emoji: %v\n", err)
			os.Exit(2)
		}
	}

	if violations > 0 {
		_, _ = fmt.Fprintf(os.Stderr, "\nlint-emoji: found %d emoji in Go source; kcp output must be plain text (see CLAUDE.md \"No emojis\")\n", violations)
		os.Exit(1)
	}
	_, _ = fmt.Printf("lint-emoji: no emoji found (%d Go files scanned)\n", filesScanned)
}
