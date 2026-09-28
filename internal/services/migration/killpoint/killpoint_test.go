package killpoint

import (
	"strings"
	"testing"
)

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

func TestFailAt_FailsOnlyTheConfiguredStep(t *testing.T) {
	t.Setenv(FailEnvVar, "verify_fence")

	err := FailAt("verify_fence")
	if err == nil {
		t.Fatalf("FailAt(%q) = nil, want an error when %s=%q", "verify_fence", FailEnvVar, "verify_fence")
	}
	if !strings.Contains(err.Error(), FailEnvVar) {
		t.Fatalf("the error must name %s so a log reader sees it was injected, got %q", FailEnvVar, err)
	}
	if err := FailAt("promote"); err != nil {
		t.Fatalf("FailAt(%q) = %v, want nil — only the configured step fails", "promote", err)
	}
}

func TestFailAt_UnsetIsAlwaysNil(t *testing.T) {
	t.Setenv(FailEnvVar, "")

	if err := FailAt("verify_fence"); err != nil {
		t.Fatalf("FailAt must be inert (nil) when %s is unset — it must never fire in production, got %v", FailEnvVar, err)
	}
}
