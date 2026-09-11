package reconcile

import (
	"strings"
	"testing"
)

func TestBuildFenceFragment(t *testing.T) {
	b, err := BuildFenceFragment()
	if err != nil {
		t.Fatalf("BuildFenceFragment: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "fence:") || !strings.Contains(s, "scope: ALL") || !strings.Contains(s, "errorCode: BROKER_NOT_AVAILABLE") {
		t.Fatalf("fragment = %q, want a fence block with scope ALL and errorCode BROKER_NOT_AVAILABLE", s)
	}
}

func TestBuildSwitchoverFragment(t *testing.T) {
	b, err := BuildSwitchoverFragment("cc", "cc-bootstrap")
	if err != nil {
		t.Fatalf("BuildSwitchoverFragment: %v", err)
	}
	s := string(b)
	if !strings.Contains(s, "streamingDomain:") || !strings.Contains(s, "name: cc") || !strings.Contains(s, "bootstrapServerId: cc-bootstrap") {
		t.Fatalf("fragment = %q, want a streamingDomain block naming cc/cc-bootstrap", s)
	}
}
