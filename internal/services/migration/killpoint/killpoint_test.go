package killpoint

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

// HoldAfter is inert unless the env var names exactly this checkpoint: no file
// is needed and nothing is created.
func TestHoldAfter_Inert(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "release")
	t.Setenv(HoldFileEnvVar, file)

	t.Setenv(HoldEnvVar, "")
	if err := HoldAfter(context.Background(), "fenced"); err != nil {
		t.Fatalf("unset: HoldAfter = %v, want nil", err)
	}
	t.Setenv(HoldEnvVar, "promoted")
	if err := HoldAfter(context.Background(), "fenced"); err != nil {
		t.Fatalf("other checkpoint: HoldAfter = %v, want nil", err)
	}
	if _, err := os.Stat(file + ".held"); !os.IsNotExist(err) {
		t.Fatalf("an inert hold must not create %s.held (stat err = %v)", file, err)
	}
}

func TestHoldAfter_HoldsUntilReleased(t *testing.T) {
	file := filepath.Join(t.TempDir(), "release")
	t.Setenv(HoldEnvVar, "fenced")
	t.Setenv(HoldFileEnvVar, file)

	done := make(chan error, 1)
	go func() { done <- HoldAfter(context.Background(), "fenced") }()

	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(file + ".held"); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s.held never appeared", file)
		}
		time.Sleep(10 * time.Millisecond)
	}
	select {
	case err := <-done:
		t.Fatalf("HoldAfter returned (%v) before the release file existed", err)
	case <-time.After(300 * time.Millisecond):
	}

	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("HoldAfter = %v, want nil once released", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HoldAfter did not return within 2s of the release")
	}
}

func TestHoldAfter_ContextCancelled(t *testing.T) {
	t.Setenv(HoldEnvVar, "fenced")
	t.Setenv(HoldFileEnvVar, filepath.Join(t.TempDir(), "release"))

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- HoldAfter(ctx, "fenced") }()
	time.Sleep(50 * time.Millisecond)
	cancel()

	select {
	case err := <-done:
		if err != context.Canceled {
			t.Fatalf("HoldAfter = %v, want context.Canceled", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HoldAfter did not return after the context was cancelled")
	}
}

func TestHoldAfter_NeedsFile(t *testing.T) {
	t.Setenv(HoldEnvVar, "fenced")
	t.Setenv(HoldFileEnvVar, "")

	err := HoldAfter(context.Background(), "fenced")
	if err == nil || !strings.Contains(err.Error(), HoldFileEnvVar) {
		t.Fatalf("HoldAfter = %v, want an error naming %s", err, HoldFileEnvVar)
	}
}
