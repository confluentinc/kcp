// Command lint-emoji fails if any Go source in the repository contains an emoji.
//
// kcp output must be plain text (see CLAUDE.md "No emojis"): the ban covers log
// lines, terminal narrative, error strings, generated artifacts, and code
// comments. This checker guards the Go source against regressions and is wired
// into `make lint`, the pre-commit hook, and CI static analysis. It is not a
// user-facing kcp subcommand — invoke via `make lint` or, directly,
// `go run ./cmd/lint-emoji [path ...]` (defaults to the current directory).
//
// What counts as an emoji is the Unicode Extended_Pictographic property (UTS #51,
// the canonical "is this an emoji" definition), generated into emoji_table.go from
// the upstream emoji-data.txt — see ./gen. Using the official property means the
// checker covers every emoji block, including ones Unicode adds later, rather than
// a hand-curated list that silently develops gaps. A small extraBanned set adds the
// few code points kcp also forbids that Unicode does not classify as
// Extended_Pictographic (emoji-presentation and keycap marks, regional-indicator
// flags, and the check/cross dingbats kcp used as status markers).
//
// A tiny allow-list (allowedPictographic) carves back the structural glyphs kcp is
// permitted to use even though Unicode classifies them as pictographic. The other
// structural glyphs kcp uses for terminal layout — tree arrows (→ ↳), box-drawing,
// block/sparkline elements, math operators (≤ ≥), and the ● bullet — are already
// outside Extended_Pictographic and so are never flagged.
//
// The frontend (React/TypeScript), docs, and vendored/generated/local-only trees
// are outside the lint surface — same spirit as .golangci.yml's exclusions.
package main

//go:generate go run ./gen

import (
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"
)

// allowedPictographic lists Extended_Pictographic code points that kcp is
// nonetheless permitted to use as structural glyphs. Today that is only the black
// small square (U+25AA), used as a markdown bullet in the cost report. Every other
// structural glyph kcp uses (arrows, box-drawing, block/sparkline, math, the ●
// bullet U+25CF) is already outside Extended_Pictographic, so no carve-out needed.
var allowedPictographic = map[rune]bool{
	0x25AA: true, // BLACK SMALL SQUARE — markdown bullet in the cost report
}

// extraBanned lists code points kcp forbids that Unicode does NOT classify as
// Extended_Pictographic, so the generated emoji table alone would miss them. This
// keeps the emoji check a strict superset of what kcp bans:
//   - 20E3 / FE0F: keycap combiner and emoji-presentation variation selector.
//     Neither has a legitimate use in plain-text source; flagging FE0F also catches
//     keycap emoji (e.g. 1 U+FE0F U+20E3) whose base is plain ASCII, outside EP.
//   - 2713 / 2717 / 2718: the check-mark and ballot-x dingbats kcp used as status
//     markers and replaced with [OK] / [FAIL]. Unicode treats these as text
//     dingbats, not emoji, so they are outside Extended_Pictographic.
//   - 1F1E6..1F1FF: regional-indicator symbols, i.e. flag emoji. Unicode classifies
//     these as Emoji_Component, not Extended_Pictographic.
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

// isEmoji reports whether r is an emoji (or emoji-only component) that kcp must not
// use: any Extended_Pictographic code point or extraBanned entry, minus the
// structural glyphs carved back by allowedPictographic.
func isEmoji(r rune) bool {
	if allowedPictographic[r] {
		return false
	}
	return unicode.Is(extendedPictographic, r) || unicode.Is(extraBanned, r)
}

// skipDir reports whether a directory is outside the lint surface, given its path
// relative to the current scan root (slash-separated) and its base name. Hidden
// directories (.git, .venv-docs, ...) and the vendored/generated trees that
// legitimately nest anywhere are skipped by base name; the frontend, docs site,
// and local-only working dirs are anchored to their path so a nested Go package
// that merely shares the name (e.g. cmd/docs) is still linted.
func skipDir(rel, name string) bool {
	if name != "." && strings.HasPrefix(name, ".") {
		return true // hidden directories, anywhere
	}
	switch name {
	case "vendor", "node_modules", "dist":
		return true // vendored/generated trees, which legitimately nest anywhere
	}
	// The React/TypeScript frontend — excluded like .golangci.yml. Matched on the
	// path (not base name) so an unrelated dir named "frontend" is unaffected.
	if rel == "cmd/ui/frontend" || strings.HasSuffix(rel, "/cmd/ui/frontend") {
		return true
	}
	// Top-level-only trees: a direct child of the scan root named one of these is
	// the docs site or a local-only working dir. Anchoring to rel (not base name)
	// keeps real Go packages such as cmd/docs in scope.
	switch rel {
	case "docs", "site", "scratch", "local":
		return true
	}
	return false
}

// checkFile scans a single file for emoji, writing "file:line:col: ..." to out for
// each and returning the count. Column is a 1-based rune position within the line,
// so an editor jump lands on (or just before) the offending glyph.
func checkFile(path string, out io.Writer) (int, error) {
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
			_, _ = fmt.Fprintf(out, "%s:%d:%d: emoji U+%04X is not allowed in kcp source\n", path, line, col, r)
			count++
		}
	}
	return count, nil
}

// run walks the given roots (defaulting to "."), reports every emoji it finds to
// stdout, and returns the process exit code: 0 clean, 1 emoji found, 2 walk error.
func run(roots []string, stdout, stderr io.Writer) int {
	if len(roots) == 0 {
		roots = []string{"."}
	}

	var violations, filesScanned int
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			rel, rerr := filepath.Rel(root, path)
			if rerr != nil {
				rel = path
			}
			rel = filepath.ToSlash(rel)
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
			n, err := checkFile(path, stdout)
			violations += n
			return err
		})
		if err != nil {
			_, _ = fmt.Fprintf(stderr, "lint-emoji: %v\n", err)
			return 2
		}
	}

	if violations > 0 {
		_, _ = fmt.Fprintf(stderr, "\nlint-emoji: found %d emoji in Go source; kcp output must be plain text (see CLAUDE.md \"No emojis\")\n", violations)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, "lint-emoji: no emoji found (%d Go files scanned)\n", filesScanned)
	return 0
}

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}
