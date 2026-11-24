package main

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"regexp"
	"strings"
	"unicode/utf8"
)

type Limits struct {
	Target int // longer than this warns
	Lines  int // longer than this fails
	Width  int
}

var DefaultLimits = Limits{Target: 1, Lines: 3, Width: 100}

type Finding struct {
	Line    int
	Rule    string
	Message string
	Warn    bool // heuristic only; never fails the run
	Text    string
}

var (
	processRef = regexp.MustCompile(`(?i)\btask[- ]?\d+(\.\d+)?\b|\b(task|design) brief\b|\bthis (task|pr|branch|commit)\b|\bfinding \d|\bphase \d+[a-z]\b|\bslice \d\b|\bper (the )?review\b|\breview (round|feedback)\b|\b(implementation|design) plan\b|\bplan step\b|§\s?\d|#\d{7,}`)
	historyRef = regexp.MustCompile(`(?i)\b(previously|no longer|used to (be|do|have|run|return|call|live)|it used to|originally|formerly|anymore|was changed|we now|now (uses|does|returns|runs|lives)|the old (code|behaviou?r|version|implementation|path)|before this change)\b`)
	emphasis   = regexp.MustCompile(`\b(NOT|NEVER|ONLY|ALWAYS|MUST|BEFORE|AFTER|EXACT|EXACTLY|BOTH|ONE|NO|EVERY|ORIGINAL|DISTINCT|EFFECTIVELY)\b`)
	reqRef     = regexp.MustCompile(`\b(R|AE)\d+\b`)
	banner     = regexp.MustCompile(`^(//|/\*)\s*[-=*#~─━═]{4,}|[-=*#~─━═]{4,}\s*(\*/)?$`)
	directive  = regexp.MustCompile(`^//(go:|nolint|lint:|export |line |extern |\+build)`)
	generated  = regexp.MustCompile(`(?m)^// Code generated .* DO NOT EDIT\.$`)
)

// Checks comment groups overlapping changed lines (nil means all lines).
func Lint(filename string, src []byte, changed map[int]bool, lim Limits) ([]Finding, error) {
	if generated.Match(src) {
		return nil, nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, err
	}

	// Go convention: exported production identifiers' docs start with the name.
	isTest := strings.HasSuffix(filename, "_test.go")
	named := map[*ast.CommentGroup]string{}
	for _, d := range f.Decls {
		switch d := d.(type) {
		case *ast.FuncDecl:
			named[d.Doc] = d.Name.Name
		case *ast.GenDecl:
			for _, sp := range d.Specs {
				if ts, ok := sp.(*ast.TypeSpec); ok {
					named[ts.Doc] = ts.Name.Name
					if len(d.Specs) == 1 {
						named[d.Doc] = ts.Name.Name
					}
				}
			}
		}
	}
	delete(named, nil)
	for cg, name := range named {
		if !isTest && ast.IsExported(name) {
			delete(named, cg)
		}
	}

	var out []Finding
	for _, cg := range f.Comments {
		start, end := fset.Position(cg.Pos()).Line, fset.Position(cg.End()).Line
		if !touches(changed, start, end) {
			continue
		}
		lines, banners := commentLines(cg)
		if len(lines) == 0 {
			continue
		}
		text := strings.Join(lines, "\n")
		add := func(rule, msg string, warn bool) {
			out = append(out, Finding{Line: start, Rule: rule, Message: msg, Warn: warn, Text: text})
		}
		if banners > 0 {
			add("banner", "decorative rule lines; keep at most a short label", false)
		}
		n := len(lines) - banners

		switch {
		case n > lim.Lines:
			add("too-long", fmt.Sprintf("comment is %d lines (max %d)", n, lim.Lines), false)
		case n > lim.Target:
			add("multi-line", fmt.Sprintf("comment is %d lines (aim for %d); keep only if the WHY can't fit", n, lim.Target), true)
		}
		for _, l := range lines {
			if w := utf8.RuneCountInString(l); w > lim.Width && !banner.MatchString(l) {
				add("too-wide", fmt.Sprintf("comment line is %d chars (max %d)", w, lim.Width), false)
				break
			}
		}
		if name, ok := named[cg]; ok {
			if first, _, _ := strings.Cut(strings.TrimPrefix(lines[0], "// "), " "); first == name {
				add("restates-name", fmt.Sprintf("opens with the name %s, which is right below", name), false)
			}
		}
		if m := processRef.FindString(text) + reqRef.FindString(text); m != "" {
			add("process-ref", fmt.Sprintf("references a plan/review artefact (%q)", m), false)
		}
		if m := historyRef.FindString(text); m != "" {
			add("history", fmt.Sprintf("may reference a past implementation (%q); fine if it describes runtime behaviour", m), true)
		}
		if ms := emphasis.FindAllString(text, -1); len(ms) > 1 {
			add("emphasis", fmt.Sprintf("capitalised emphasis (%s)", strings.Join(uniq(ms), ", ")), true)
		}
	}
	return out, nil
}

// Returns the group's non-directive lines, trimmed, and how many are banners.
func commentLines(cg *ast.CommentGroup) ([]string, int) {
	var out []string
	var banners int
	for _, c := range cg.List {
		if directive.MatchString(c.Text) {
			continue
		}
		for _, l := range strings.Split(c.Text, "\n") {
			switch l = strings.TrimSpace(l); {
			case l == "" || l == "//":
			default:
				out = append(out, l)
				if banner.MatchString(l) {
					banners++
				}
			}
		}
	}
	return out, banners
}

func touches(changed map[int]bool, start, end int) bool {
	if changed == nil {
		return true
	}
	for l := start; l <= end; l++ {
		if changed[l] {
			return true
		}
	}
	return false
}

func uniq(ss []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range ss {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

// Reports whether a and b have identical token streams, ignoring comments.
func SameCode(a, b []byte) bool {
	ta, tb := tokens(a), tokens(b)
	if len(ta) != len(tb) {
		return false
	}
	for i := range ta {
		if ta[i] != tb[i] {
			return false
		}
	}
	return true
}

func tokens(src []byte) []string {
	fset := token.NewFileSet()
	file := fset.AddFile("", fset.Base(), len(src))
	var s scanner.Scanner
	s.Init(file, src, nil, 0)
	var out []string
	for {
		_, tok, lit := s.Scan()
		if tok == token.EOF {
			return out
		}
		if tok == token.SEMICOLON {
			continue // automatic semicolons move with line breaks
		}
		out = append(out, tok.String()+lit)
	}
}
