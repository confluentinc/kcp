package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

type stopInput struct {
	SessionID      string `json:"session_id"`
	StopHookActive bool   `json:"stop_hook_active"`
}

// Lints uncommitted Go changes, reporting each finding once per session.
func runStop(r io.Reader, lim Limits) (bool, error) {
	var in stopInput
	if err := json.NewDecoder(r).Decode(&in); err != nil || in.StopHookActive {
		return false, nil
	}
	changed, err := changedLines("HEAD", nil)
	if err != nil {
		return false, nil
	}
	seenPath := filepath.Join(os.TempDir(), "commentlint", in.SessionID)
	seen := map[string]bool{}
	if b, err := os.ReadFile(seenPath); err == nil {
		for _, k := range strings.Fields(string(b)) {
			seen[k] = true
		}
	}

	files := make([]string, 0, len(changed))
	for f := range changed {
		files = append(files, f)
	}
	sort.Strings(files)
	var report, keys []string
	for _, f := range files {
		src, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		findings, err := Lint(f, src, changed[f], lim)
		if err != nil {
			continue
		}
		for _, fd := range findings {
			k := fingerprint(f, fd)
			if seen[k] {
				continue
			}
			keys = append(keys, k)
			report = append(report, format(f, fd))
		}
	}
	if len(report) == 0 {
		return false, nil
	}
	if err := os.MkdirAll(filepath.Dir(seenPath), 0o755); err == nil {
		if fh, err := os.OpenFile(seenPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644); err == nil {
			fmt.Fprintln(fh, strings.Join(keys, "\n"))
			fh.Close()
		}
	}
	fmt.Fprintf(os.Stderr, "Comment check on your uncommitted Go changes (.claude/rules/code-comments.md).\n"+
		"Cut each to one short line or delete it; leave one only if genuinely warranted (it will not be flagged again this session):\n%s\n",
		strings.Join(report, "\n"))
	return true, nil
}

// Keys on content, not line number, so unrelated edits don't resurface a finding.
func fingerprint(file string, fd Finding) string {
	h := sha256.Sum256([]byte(file + "\x00" + fd.Rule + "\x00" + fd.Text))
	return hex.EncodeToString(h[:8])
}
