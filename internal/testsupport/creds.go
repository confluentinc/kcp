// Package testsupport holds small helpers shared by test files across
// packages. It is not itself a _test.go file (Go doesn't let those be
// imported), but nothing outside test code should import it.
package testsupport

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// WriteCredFile writes body (or def, if body is empty) to name inside dir at
// 0600, and returns the file's path. Extracted from four near-identical
// copies across the migration command test suites (execute, init, lagcheck,
// executetbm) that each wrote a credentials file into a manifest fixture.
func WriteCredFile(t *testing.T, dir, name, body, def string) string {
	t.Helper()
	if body == "" {
		body = def
	}
	p := filepath.Join(dir, name)
	require.NoError(t, os.WriteFile(p, []byte(body), 0600))
	return p
}
