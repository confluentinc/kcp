// Command gen regenerates emoji_table.go from the upstream Unicode emoji-data.txt.
//
// It extracts the Extended_Pictographic property — Unicode's canonical definition
// of "is this an emoji" (UTS #51) — and emits it as a *unicode.RangeTable that the
// lint-emoji checker consults. Using the official property means the checker covers
// every emoji block (and every future one Unicode adds within the ranges) instead
// of a hand-curated list that silently develops gaps.
//
// Run via `go generate ./cmd/lint-emoji` (fetches the pinned Unicode version over
// the network), or offline with a local copy:
//
//	go run ./cmd/lint-emoji/gen -src path/to/emoji-data.txt -out cmd/lint-emoji/emoji_table.go
//
// The emitted table is numeric-only and carries no emoji bytes, so lint-emoji does
// not flag its own generated source.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
)

// unicodeVersion pins the Unicode release the table is generated from, so
// regeneration is reproducible. Bump it deliberately to adopt a newer emoji set.
const unicodeVersion = "16.0.0"

func dataURL() string {
	return fmt.Sprintf("https://www.unicode.org/Public/%s/ucd/emoji/emoji-data.txt", unicodeVersion)
}

func main() {
	src := flag.String("src", "", "path to a local emoji-data.txt (default: fetch the pinned Unicode version)")
	out := flag.String("out", "emoji_table.go", "output Go file")
	flag.Parse()

	fatal := func(err error) {
		_, _ = fmt.Fprintf(os.Stderr, "gen: %v\n", err)
		os.Exit(1)
	}

	raw, origin, err := load(*src)
	if err != nil {
		fatal(err)
	}

	ranges, err := parseExtendedPictographic(raw)
	if err != nil {
		fatal(err)
	}
	if len(ranges) == 0 {
		fatal(fmt.Errorf("no Extended_Pictographic ranges found"))
	}
	ranges = coalesce(ranges)

	code, err := render(ranges, origin)
	if err != nil {
		fatal(err)
	}
	if err := os.WriteFile(*out, code, 0o644); err != nil {
		fatal(err)
	}
	_, _ = fmt.Printf("gen: wrote %s (%d ranges, Unicode %s)\n", *out, len(ranges), unicodeVersion)
}

// load returns the emoji-data.txt bytes and a human-readable origin string.
func load(src string) ([]byte, string, error) {
	if src != "" {
		b, err := os.ReadFile(src)
		return b, "emoji-data.txt (local copy)", err
	}
	url := dataURL()
	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("GET %s: %s", url, resp.Status)
	}
	buf := new(bytes.Buffer)
	if _, err := buf.ReadFrom(resp.Body); err != nil {
		return nil, "", err
	}
	return buf.Bytes(), url, nil
}

type rng struct{ lo, hi rune }

// parseExtendedPictographic pulls the "<range> ; Extended_Pictographic" rows out of
// emoji-data.txt, ignoring the trailing "# ..." comment (which contains emoji bytes
// we must never copy into generated Go source).
func parseExtendedPictographic(data []byte) ([]rng, error) {
	var out []rng
	sc := bufio.NewScanner(bytes.NewReader(data))
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		line := sc.Text()
		if i := strings.IndexByte(line, '#'); i >= 0 {
			line = line[:i]
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.SplitN(line, ";", 2)
		if len(fields) != 2 {
			continue
		}
		if strings.TrimSpace(fields[1]) != "Extended_Pictographic" {
			continue
		}
		r, err := parseRange(strings.TrimSpace(fields[0]))
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// parseRange parses "1F600" or "1F600..1F64F".
func parseRange(s string) (rng, error) {
	lo, hi, found := strings.Cut(s, "..")
	l, err := strconv.ParseUint(lo, 16, 32)
	if err != nil {
		return rng{}, fmt.Errorf("bad code point %q: %w", s, err)
	}
	h := l
	if found {
		v, err := strconv.ParseUint(hi, 16, 32)
		if err != nil {
			return rng{}, fmt.Errorf("bad code point %q: %w", s, err)
		}
		h = v
	}
	return rng{rune(l), rune(h)}, nil
}

// coalesce sorts and merges adjacent/overlapping ranges so the emitted table is tight.
func coalesce(in []rng) []rng {
	sort.Slice(in, func(i, j int) bool { return in[i].lo < in[j].lo })
	out := in[:0]
	for _, r := range in {
		if n := len(out); n > 0 && r.lo <= out[n-1].hi+1 {
			if r.hi > out[n-1].hi {
				out[n-1].hi = r.hi
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

func render(ranges []rng, origin string) ([]byte, error) {
	var r16, r32 []rng
	for _, r := range ranges {
		switch {
		case r.hi <= 0xFFFF:
			r16 = append(r16, r)
		case r.lo > 0xFFFF:
			r32 = append(r32, r)
		default: // straddles the BMP boundary: split
			r16 = append(r16, rng{r.lo, 0xFFFF})
			r32 = append(r32, rng{0x10000, r.hi})
		}
	}
	var latinOffset int
	for _, r := range r16 {
		if r.hi > 0xFF {
			break
		}
		latinOffset++
	}

	var b strings.Builder
	emit := func(format string, a ...any) { _, _ = fmt.Fprintf(&b, format, a...) }

	emit("// Code generated by \"go run ./cmd/lint-emoji/gen\"; DO NOT EDIT.\n")
	emit("//\n")
	emit("// Extended_Pictographic property from Unicode %s.\n", unicodeVersion)
	emit("// Source: %s\n", origin)
	emit("\npackage main\n\nimport \"unicode\"\n\n")
	emit("// extendedPictographic is the Unicode Extended_Pictographic set — the canonical\n")
	emit("// definition of an emoji code point (UTS #51). lint-emoji flags any rune in this\n")
	emit("// table except the structural glyphs carved out in allowedPictographic.\n")
	emit("var extendedPictographic = &unicode.RangeTable{\n")
	emit("\tR16: []unicode.Range16{\n")
	for _, r := range r16 {
		emit("\t\t{0x%04X, 0x%04X, 1},\n", r.lo, r.hi)
	}
	emit("\t},\n")
	emit("\tR32: []unicode.Range32{\n")
	for _, r := range r32 {
		emit("\t\t{0x%04X, 0x%04X, 1},\n", r.lo, r.hi)
	}
	emit("\t},\n")
	emit("\tLatinOffset: %d,\n", latinOffset)
	emit("}\n")

	return format.Source([]byte(b.String()))
}
