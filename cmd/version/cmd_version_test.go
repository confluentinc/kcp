package version

import (
	"bytes"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/build_info"
)

// TestVersionOutputIncludesEdition asserts the version command reports the
// compile-time edition. Covers AE3. The exact value (prod/gov) is whatever this
// test binary was built with; the gov value is exercised under `-tags=gov`.
func TestVersionOutputIncludesEdition(t *testing.T) {
	cmd := NewVersionCmd()
	var buf bytes.Buffer
	cmd.SetOut(&buf)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("version command failed: %v", err)
	}

	out := buf.String()
	wantLine := "Edition: " + build_info.Mode
	if !strings.Contains(out, wantLine) {
		t.Errorf("version output missing %q\ngot:\n%s", wantLine, out)
	}
}
