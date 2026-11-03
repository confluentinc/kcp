package reconcile

import "testing"

func TestOwnerRoute(t *testing.T) {
	conds := []Condition{
		{Topics: []string{"black-team"}, TopicPatterns: []string{"teamB.*"}, Domain: "msk"},
		{TopicPatterns: []string{"teamA.*"}, Domain: "cc"},
		{Topics: []string{"orders"}, Domain: "cc"},
	}
	cases := []struct {
		topic, want string
		wantOK      bool
	}{
		{"black-team", "msk", true}, // exact in condition 0
		{"teamB-x", "msk", true},    // pattern in condition 0
		{"teamA-y", "cc", true},     // condition 1
		{"orders", "cc", true},      // condition 2 (exact)
		{"random", "msk", true},     // default
	}
	for _, c := range cases {
		got, ok := OwnerRoute(c.topic, conds, "msk")
		if got != c.want || ok != c.wantOK {
			t.Errorf("OwnerRoute(%q) = (%q,%v), want (%q,%v)", c.topic, got, ok, c.want, c.wantOK)
		}
	}
}

func TestOwnerRouteStrictNoDefault(t *testing.T) {
	if got, ok := OwnerRoute("x", nil, ""); ok || got != "" {
		t.Fatalf("strict route (empty default) must return (\"\",false), got (%q,%v)", got, ok)
	}
}

func TestOwnerRouteAnchored(t *testing.T) {
	conds := []Condition{{TopicPatterns: []string{"orders"}, Domain: "cc"}}
	// anchored full-match: "orders" matches, "my-orders" and "orders-v2" do NOT
	if _, ok := OwnerRoute("my-orders", conds, ""); ok {
		t.Error("anchored pattern must not match a substring (my-orders)")
	}
	if d, _ := OwnerRoute("orders", conds, "msk"); d != "cc" {
		t.Error("anchored pattern must full-match the exact string")
	}
}
