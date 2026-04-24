package docs

import (
	"bytes"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/build_info"
)

func TestDocsCmdPrintsDocsURL(t *testing.T) {
	cmd := NewDocsCmd()
	buf := &bytes.Buffer{}
	cmd.SetOut(buf)
	cmd.SetErr(buf)
	cmd.SetArgs(nil)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("Execute() returned error: %v", err)
	}

	got := strings.TrimSpace(buf.String())
	if got != build_info.DocsURL() {
		t.Errorf("cmd output = %q, want %q", got, build_info.DocsURL())
	}
}
