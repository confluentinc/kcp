package regexanchor

import "testing"

func TestCompileAnchorsFullMatch(t *testing.T) {
	re, err := Compile("team-a.*")
	if err != nil {
		t.Fatalf("Compile: %v", err)
	}
	if !re.MatchString("team-a.orders") {
		t.Error(`"team-a.orders" should match team-a.*`)
	}
	// full-match: a suffix/substring must NOT match.
	if re.MatchString("x-team-a.orders") {
		t.Error(`"x-team-a.orders" must not match an anchored team-a.*`)
	}
}

// TestCompileRejectsAnchorEscape is the reason this package exists: a pattern
// that would escape the anchor if naively spliced into "^(?:p)$" must be
// rejected, not silently turned into a prefix/suffix match. "foo)|(evil" closes
// the wrapper group early and promotes a top-level alternation.
func TestCompileRejectsAnchorEscape(t *testing.T) {
	if _, err := Compile("foo)|(evil"); err == nil {
		t.Fatal("an unbalanced pattern must be rejected, not anchor-escaped")
	}
}

func TestCompileRejectsInvalidRegex(t *testing.T) {
	if _, err := Compile("([a-z"); err == nil {
		t.Fatal("an invalid regex must return an error")
	}
}
