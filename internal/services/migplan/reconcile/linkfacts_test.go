package reconcile

import (
	"strings"
	"testing"
)

func TestCheckLinkOneWay(t *testing.T) {
	res := checkLinkOneWay("BIDIRECTIONAL")
	if res.OK || res.Name != LinkOneWayCheckName {
		t.Fatalf("BIDIRECTIONAL: got %+v, want a failed %q", res, LinkOneWayCheckName)
	}
	for _, want := range []string{"link.mode=BIDIRECTIONAL", "reversibleStaticMigration"} {
		if !strings.Contains(res.Detail, want) {
			t.Errorf("detail = %q, want it to contain %q", res.Detail, want)
		}
	}
	for _, mode := range []string{"DESTINATION", "SOURCE", ""} {
		if r := checkLinkOneWay(mode); !r.OK || r.Name != LinkOneWayCheckName {
			t.Errorf("mode %q: got %+v, want a pass", mode, r)
		}
	}
}

func TestCheckLinkHealthy(t *testing.T) {
	for _, e := range []string{"", "NO_ERROR"} {
		if r := checkLinkHealthy(e); !r.OK || r.Name != LinkHealthyCheckName {
			t.Errorf("link_error %q: got %+v, want a pass", e, r)
		}
	}
	res := checkLinkHealthy("AUTHENTICATION_ERROR")
	if res.OK {
		t.Fatal("AUTHENTICATION_ERROR must fail")
	}
	if !strings.Contains(res.Detail, "link_error=AUTHENTICATION_ERROR") {
		t.Errorf("detail = %q, want it to name the link error", res.Detail)
	}
}
