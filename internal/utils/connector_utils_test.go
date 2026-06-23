package utils

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSanitizeConnectorFilename(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "ordinary name unchanged", in: "pg-sink", want: "pg-sink"},
		{name: "dots and underscores preserved", in: "my.connector_v2", want: "my.connector_v2"},
		{name: "forward slashes neutralized", in: "../../etc/cron.d/evil", want: ".._.._etc_cron.d_evil"},
		{name: "backslashes neutralized", in: `..\..\windows\evil`, want: ".._.._windows_evil"},
		{name: "absolute path neutralized", in: "/etc/passwd", want: "_etc_passwd"},
		{name: "spaces and colons neutralized", in: "weird: name", want: "weird__name"},
		{name: "bare dot collapses", in: ".", want: "connector"},
		{name: "bare dot-dot collapses", in: "..", want: "connector"},
		{name: "all dots collapses", in: "...", want: "connector"},
		{name: "empty collapses", in: "", want: "connector"},
		{name: "slashes-only collapses", in: "/", want: "_"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SanitizeConnectorFilename(tt.in)
			assert.Equal(t, tt.want, got)
		})
	}
}

// The security invariant: no sanitized name, joined under a base dir, may resolve
// outside that base dir. This guards the property the callers rely on regardless
// of the exact replacement scheme.
func TestSanitizeConnectorFilename_NeverEscapesBaseDir(t *testing.T) {
	base := filepath.Clean("/tmp/out")
	hostile := []string{
		"../../etc/cron.d/evil",
		`..\..\..\windows\system32\evil`,
		"/etc/passwd",
		"..",
		".",
		"foo/../../bar",
		"a/b/c",
	}
	for _, name := range hostile {
		filename := SanitizeConnectorFilename(name) + "-connector.tf"
		assert.NotContains(t, filename, string(filepath.Separator),
			"sanitized filename must contain no path separator")
		resolved := filepath.Clean(filepath.Join(base, filename))
		assert.True(t, strings.HasPrefix(resolved, base+string(filepath.Separator)),
			"resolved path %q must stay under base dir %q for input %q", resolved, base, name)
	}
}
