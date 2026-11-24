// Command commentlint enforces .claude/rules/code-comments.md on changed Go lines.
package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	base := flag.String("base", "origin/main", "git ref to diff against (its merge-base with HEAD is used)")
	all := flag.Bool("all", false, "check every comment in the given paths, not just changed lines")
	verify := flag.Bool("verify-code", false, "fail if any changed .go file differs from base in more than comments")
	stop := flag.Bool("stop", false, "Claude Code Stop-hook mode: read the hook payload on stdin")
	lim := DefaultLimits
	flag.IntVar(&lim.Target, "target-lines", lim.Target, "lines per comment above which to warn")
	flag.IntVar(&lim.Lines, "max-lines", lim.Lines, "lines per comment above which to fail")
	flag.IntVar(&lim.Width, "max-width", lim.Width, "max characters per comment line")
	flag.Parse()

	paths, err := toRepoRoot(flag.Args())
	var failed bool
	switch {
	case err != nil:
	case *stop:
		failed, err = runStop(os.Stdin, lim)
		if failed {
			os.Exit(2) // blocks the stop; stderr goes to Claude
		}
	case *verify:
		failed, err = runVerify(*base, paths)
	case *all:
		failed, err = runAll(paths, lim)
	default:
		failed, err = runDiff(*base, paths, lim)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "commentlint:", err)
		os.Exit(2)
	}
	if failed {
		os.Exit(1)
	}
}

// Rebases paths onto the repo root and chdirs there, matching git's paths.
func toRepoRoot(args []string) ([]string, error) {
	out, err := exec.Command("git", "rev-parse", "--show-toplevel").Output()
	if err != nil {
		return nil, fmt.Errorf("not in a git repository")
	}
	root := strings.TrimSpace(string(out))
	paths := make([]string, 0, len(args))
	for _, a := range args {
		abs, err := filepath.Abs(strings.TrimSuffix(a, "/..."))
		if err != nil {
			return nil, err
		}
		rel, err := filepath.Rel(root, abs)
		if err != nil {
			return nil, err
		}
		paths = append(paths, rel)
	}
	return paths, os.Chdir(root)
}

func runAll(paths []string, lim Limits) (bool, error) {
	if len(paths) == 0 {
		paths = []string{"."}
	}
	var files []string
	for _, p := range paths {
		err := filepath.WalkDir(p, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() && skipDir(d.Name()) && path != p {
				return filepath.SkipDir
			}
			if !d.IsDir() && strings.HasSuffix(path, ".go") {
				files = append(files, path)
			}
			return nil
		})
		if err != nil {
			return false, err
		}
	}
	return lintFiles(os.Stdout, files, nil, lim)
}

func runDiff(base string, paths []string, lim Limits) (bool, error) {
	mb, err := mergeBase(base)
	if err != nil {
		return false, err
	}
	changed, err := changedLines(mb, paths)
	if err != nil {
		return false, err
	}
	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	return lintFiles(os.Stdout, files, changed, lim)
}

func lintFiles(out io.Writer, files []string, changed map[string]map[int]bool, lim Limits) (bool, error) {
	sort.Strings(files)
	var errs, warns int
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			return false, err
		}
		var lines map[int]bool
		if changed != nil {
			lines = changed[f]
		}
		findings, err := Lint(f, src, lines, lim)
		if err != nil {
			fmt.Fprintf(os.Stderr, "commentlint: skipping %s: %v\n", f, err)
			continue
		}
		for _, fd := range findings {
			fmt.Fprintln(out, format(f, fd))
			if fd.Warn {
				warns++
			} else {
				errs++
			}
		}
	}
	if errs+warns > 0 {
		fmt.Fprintf(out, "\n%d error(s), %d warning(s). Standard: .claude/rules/code-comments.md\n", errs, warns)
	}
	return errs > 0, nil
}

func format(file string, fd Finding) string {
	sev := "error"
	if fd.Warn {
		sev = "warning"
	}
	return fmt.Sprintf("%s:%d: %s [%s] %s", file, fd.Line, sev, fd.Rule, fd.Message)
}

func runVerify(base string, paths []string) (bool, error) {
	mb, err := mergeBase(base)
	if err != nil {
		return false, err
	}
	changed, err := changedLines(mb, paths)
	if err != nil {
		return false, err
	}
	var bad []string
	for f := range changed {
		now, err := os.ReadFile(f)
		if err != nil {
			return false, err
		}
		old, err := git("show", mb+":"+f)
		if err != nil {
			bad = append(bad, f+" (new file)")
			continue
		}
		if !SameCode(old, now) {
			bad = append(bad, f)
		}
	}
	sort.Strings(bad)
	for _, f := range bad {
		fmt.Printf("%s: code changed, not just comments\n", f)
	}
	if len(bad) == 0 {
		fmt.Printf("OK: %d file(s) differ from %s only in comments\n", len(changed), base)
	}
	return len(bad) > 0, nil
}

func mergeBase(ref string) (string, error) {
	out, err := git("merge-base", ref, "HEAD")
	if err != nil {
		return "", fmt.Errorf("merge-base %s HEAD: %w", ref, err)
	}
	return strings.TrimSpace(string(out)), nil
}

var hunk = regexp.MustCompile(`^@@ -\d+(?:,\d+)? \+(\d+)(?:,(\d+))? @@`)

// Maps each changed or untracked .go file to its added line numbers.
func changedLines(base string, paths []string) (map[string]map[int]bool, error) {
	out, err := git(append([]string{"diff", "-U0", "--no-color", "--no-ext-diff", base, "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	res := map[string]map[int]bool{}
	var cur string
	sc := bufio.NewScanner(bytes.NewReader(out))
	sc.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.HasPrefix(line, "+++ "):
			cur = ""
			if p := strings.TrimPrefix(line, "+++ b/"); p != line && keep(p) {
				cur = p
			}
		case cur != "":
			if m := hunk.FindStringSubmatch(line); m != nil {
				start, _ := strconv.Atoi(m[1])
				count := 1
				if m[2] != "" {
					count, _ = strconv.Atoi(m[2])
				}
				if res[cur] == nil {
					res[cur] = map[int]bool{}
				}
				for l := start; l < start+count; l++ {
					res[cur][l] = true
				}
			}
		}
	}
	untracked, err := git(append([]string{"ls-files", "--others", "--exclude-standard", "--"}, paths...)...)
	if err != nil {
		return nil, err
	}
	for _, p := range strings.Fields(string(untracked)) {
		if keep(p) {
			res[p] = nil // nil: whole file is new
		}
	}
	for p, lines := range res {
		if lines == nil {
			src, err := os.ReadFile(p)
			if err != nil {
				return nil, err
			}
			all := map[int]bool{}
			for l := 1; l <= bytes.Count(src, []byte("\n"))+1; l++ {
				all[l] = true
			}
			res[p] = all
		}
	}
	return res, nil
}

func keep(path string) bool {
	if !strings.HasSuffix(path, ".go") {
		return false
	}
	for _, part := range strings.Split(filepath.ToSlash(path), "/") {
		if skipDir(part) {
			return false
		}
	}
	return true
}

func skipDir(name string) bool {
	return name == "vendor" || name == "testdata" || name == "node_modules" || (strings.HasPrefix(name, ".") && name != ".")
}

func git(args ...string) ([]byte, error) {
	cmd := exec.Command("git", args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git %s: %v: %s", strings.Join(args, " "), err, strings.TrimSpace(stderr.String()))
	}
	return out, nil
}
