// TEMPORARY — added as a tripwire for the internal/types reorg.
// Delete this file (and testdata/api_surface.golden.txt) once the reorg has
// landed and the team is satisfied with the result. Keeping it permanently
// is a viable option if you want a guardrail against accidental API drift.
//
// What it does: parses every non-test .go file in this package, collects
// every exported declaration (types, funcs, methods, consts, vars, struct
// fields and their tags, interface methods) in a canonical text form, and
// diffs against testdata/api_surface.golden.txt.
//
// To (re)capture the golden after an intentional public API change, run:
//
//   UPDATE_API_SURFACE_GOLDEN=1 go test ./internal/types/ -run TestReorgNoop_ExportedAPISnapshot
//
// The reorg PR must NOT regenerate this golden — that is exactly the change
// the test exists to detect.

package types_test

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/printer"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

const apiSurfaceGoldenPath = "testdata/api_surface.golden.txt"

func TestReorgNoop_ExportedAPISnapshot(t *testing.T) {
	surface, err := buildExportedAPISurface(".")
	if err != nil {
		t.Fatalf("build surface: %v", err)
	}

	if os.Getenv("UPDATE_API_SURFACE_GOLDEN") != "" {
		if err := os.MkdirAll(filepath.Dir(apiSurfaceGoldenPath), 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		if err := os.WriteFile(apiSurfaceGoldenPath, []byte(surface), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("wrote golden: %s (%d bytes)", apiSurfaceGoldenPath, len(surface))
		return
	}

	want, err := os.ReadFile(apiSurfaceGoldenPath)
	if err != nil {
		t.Fatalf("missing golden file %s — capture with UPDATE_API_SURFACE_GOLDEN=1: %v", apiSurfaceGoldenPath, err)
	}

	if string(want) == surface {
		return
	}

	// Render a line-by-line diff for readability.
	t.Fatalf("exported API surface drift detected.\n%s\n\nIf this drift is intentional, re-capture with UPDATE_API_SURFACE_GOLDEN=1.\nThe internal/types reorg should NOT cause drift — that is what this test is for.", diffLines(string(want), surface))
}

// buildExportedAPISurface walks the .go files in dir, ignoring test files
// and any _test package, and returns a sorted, newline-joined canonical
// rendering of every exported declaration.
func buildExportedAPISurface(dir string) (string, error) {
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.ParseComments)
	if err != nil {
		return "", err
	}

	var lines []string
	for pkgName, pkg := range pkgs {
		if strings.HasSuffix(pkgName, "_test") {
			continue
		}
		fileNames := make([]string, 0, len(pkg.Files))
		for name := range pkg.Files {
			fileNames = append(fileNames, name)
		}
		sort.Strings(fileNames)
		for _, name := range fileNames {
			file := pkg.Files[name]
			for _, decl := range file.Decls {
				lines = append(lines, renderDecl(fset, decl)...)
			}
		}
	}

	// Drop empties, sort for stability.
	filtered := lines[:0]
	for _, l := range lines {
		if l != "" {
			filtered = append(filtered, l)
		}
	}
	sort.Strings(filtered)
	return strings.Join(filtered, "\n") + "\n", nil
}

func renderDecl(fset *token.FileSet, decl ast.Decl) []string {
	switch d := decl.(type) {
	case *ast.GenDecl:
		return renderGenDecl(fset, d)
	case *ast.FuncDecl:
		return renderFuncDecl(fset, d)
	}
	return nil
}

func renderGenDecl(fset *token.FileSet, d *ast.GenDecl) []string {
	var out []string
	for _, spec := range d.Specs {
		switch s := spec.(type) {
		case *ast.TypeSpec:
			if !s.Name.IsExported() {
				continue
			}
			out = append(out, renderTypeSpec(fset, s))
		case *ast.ValueSpec:
			kind := "VAR"
			if d.Tok == token.CONST {
				kind = "CONST"
			}
			typeStr := ""
			if s.Type != nil {
				typeStr = " " + nodeString(fset, s.Type)
			}
			for i, name := range s.Names {
				if !name.IsExported() {
					continue
				}
				valStr := ""
				if i < len(s.Values) {
					valStr = " = " + nodeString(fset, s.Values[i])
				}
				out = append(out, fmt.Sprintf("%s %s%s%s", kind, name.Name, typeStr, valStr))
			}
		}
	}
	return out
}

func renderTypeSpec(fset *token.FileSet, s *ast.TypeSpec) string {
	switch t := s.Type.(type) {
	case *ast.StructType:
		var fields []string
		for _, f := range t.Fields.List {
			tag := ""
			if f.Tag != nil {
				tag = " " + f.Tag.Value
			}
			typeStr := nodeString(fset, f.Type)
			if len(f.Names) == 0 {
				// Embedded field; only emit if the embedded type is exported.
				if exportedTypeName(typeStr) {
					fields = append(fields, fmt.Sprintf("%s%s", typeStr, tag))
				}
				continue
			}
			for _, name := range f.Names {
				if !name.IsExported() {
					continue
				}
				fields = append(fields, fmt.Sprintf("%s %s%s", name.Name, typeStr, tag))
			}
		}
		sort.Strings(fields)
		return fmt.Sprintf("TYPE %s struct { %s }", s.Name.Name, strings.Join(fields, "; "))
	case *ast.InterfaceType:
		var methods []string
		for _, m := range t.Methods.List {
			if len(m.Names) == 0 {
				// Embedded interface.
				typeStr := nodeString(fset, m.Type)
				if exportedTypeName(typeStr) {
					methods = append(methods, typeStr)
				}
				continue
			}
			for _, name := range m.Names {
				if !name.IsExported() {
					continue
				}
				methods = append(methods, fmt.Sprintf("%s%s", name.Name, funcSigString(fset, m.Type)))
			}
		}
		sort.Strings(methods)
		return fmt.Sprintf("TYPE %s interface { %s }", s.Name.Name, strings.Join(methods, "; "))
	default:
		return fmt.Sprintf("TYPE %s = %s", s.Name.Name, nodeString(fset, s.Type))
	}
}

func renderFuncDecl(fset *token.FileSet, d *ast.FuncDecl) []string {
	if !d.Name.IsExported() {
		return nil
	}
	sig := funcSigString(fset, d.Type)
	if d.Recv == nil {
		return []string{fmt.Sprintf("FUNC %s%s", d.Name.Name, sig)}
	}
	recv := nodeString(fset, d.Recv.List[0].Type)
	// Only emit if the receiver's underlying type name is exported.
	if !exportedTypeName(recv) {
		return nil
	}
	return []string{fmt.Sprintf("METHOD (%s) %s%s", recv, d.Name.Name, sig)}
}

func funcSigString(fset *token.FileSet, expr ast.Expr) string {
	ft, ok := expr.(*ast.FuncType)
	if !ok {
		return nodeString(fset, expr)
	}
	params := fieldListString(fset, ft.Params)
	results := fieldListString(fset, ft.Results)
	if results == "" {
		return fmt.Sprintf("(%s)", params)
	}
	return fmt.Sprintf("(%s) %s", params, results)
}

func fieldListString(fset *token.FileSet, fl *ast.FieldList) string {
	if fl == nil {
		return ""
	}
	var parts []string
	for _, f := range fl.List {
		typeStr := nodeString(fset, f.Type)
		if len(f.Names) == 0 {
			parts = append(parts, typeStr)
			continue
		}
		var names []string
		for _, n := range f.Names {
			names = append(names, n.Name)
		}
		parts = append(parts, fmt.Sprintf("%s %s", strings.Join(names, ", "), typeStr))
	}
	return strings.Join(parts, ", ")
}

func nodeString(fset *token.FileSet, n ast.Node) string {
	var buf bytes.Buffer
	if err := printer.Fprint(&buf, fset, n); err != nil {
		return fmt.Sprintf("<render error: %v>", err)
	}
	// Collapse any whitespace runs so re-formatting cannot perturb the golden.
	return strings.Join(strings.Fields(buf.String()), " ")
}

// exportedTypeName returns true if the leading identifier of a (possibly
// pointer / qualified) type name starts with an uppercase letter.
func exportedTypeName(s string) bool {
	t := strings.TrimPrefix(s, "*")
	// Strip any selector prefix (e.g. "pkg.Type" -> "Type"). For the types
	// package, receivers and embeds are local, so this is mostly a safety net.
	if i := strings.LastIndex(t, "."); i >= 0 {
		t = t[i+1:]
	}
	if t == "" {
		return false
	}
	c := t[0]
	return c >= 'A' && c <= 'Z'
}

func diffLines(want, got string) string {
	wantLines := strings.Split(strings.TrimRight(want, "\n"), "\n")
	gotLines := strings.Split(strings.TrimRight(got, "\n"), "\n")
	wantSet := make(map[string]struct{}, len(wantLines))
	for _, l := range wantLines {
		wantSet[l] = struct{}{}
	}
	gotSet := make(map[string]struct{}, len(gotLines))
	for _, l := range gotLines {
		gotSet[l] = struct{}{}
	}
	var b strings.Builder
	for _, l := range wantLines {
		if _, ok := gotSet[l]; !ok {
			b.WriteString("- " + l + "\n")
		}
	}
	for _, l := range gotLines {
		if _, ok := wantSet[l]; !ok {
			b.WriteString("+ " + l + "\n")
		}
	}
	if b.Len() == 0 {
		return "(no line differences; trailing whitespace or ordering changed)"
	}
	return b.String()
}
