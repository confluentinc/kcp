package killpoint

import "testing"

// ShouldCancelAfter fires only when the env var names exactly this checkpoint,
// so a live resume test can deterministically interrupt kcp after a chosen
// step. Unset (the production default) is always false.
func TestShouldCancelAfter_MatchesConfiguredCheckpoint(t *testing.T) {
	t.Setenv(EnvVar, "fenced")

	if !ShouldCancelAfter("fenced") {
		t.Fatalf("ShouldCancelAfter(%q) = false, want true when %s=%q", "fenced", EnvVar, "fenced")
	}
	if ShouldCancelAfter("promoted") {
		t.Fatalf("ShouldCancelAfter(%q) = true, want false — only the configured checkpoint cancels", "promoted")
	}
}

func TestShouldCancelAfter_UnsetIsAlwaysFalse(t *testing.T) {
	t.Setenv(EnvVar, "")

	if ShouldCancelAfter("fenced") {
		t.Fatalf("ShouldCancelAfter must be inert (false) when %s is unset — it must never fire in production", EnvVar)
	}
}
