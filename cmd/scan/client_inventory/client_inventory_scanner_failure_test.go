package client_inventory

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

// scriptedSource returns canned chunks, with an error for any chunk named in failing.
type scriptedSource struct {
	chunks  []LogChunk
	content map[string][]byte
	failing map[string]error
}

func (s *scriptedSource) Name() string                                   { return "scripted" }
func (s *scriptedSource) ListChunks(context.Context) ([]LogChunk, error) { return s.chunks, nil }
func (s *scriptedSource) ReadChunk(_ context.Context, c LogChunk) ([]byte, error) {
	if err, ok := s.failing[c.ID]; ok {
		return nil, err
	}
	return s.content[c.ID], nil
}

// A chunk that cannot be read must fail the run. Before the fix this printed
// "No clients discovered" and exited 0, so a wrong region, a missing log group or a
// window outside retention looked like a cluster with no clients.
func TestRun_FailedChunkReturnsError(t *testing.T) {
	boom := errors.New("ResourceNotFoundException: log group does not exist")
	src := &scriptedSource{
		chunks:  []LogChunk{{ID: "kcp"}},
		failing: map[string]error{"kcp": boom},
	}
	scanner, _ := NewClientInventoryScanner(src, types.State{}, ClientInventoryScannerOpts{StateFile: t.TempDir() + "/state.json"})

	err := scanner.Run()
	if err == nil {
		t.Fatal("want an error when the only chunk fails to read, got nil")
	}
	if !errors.Is(err, boom) {
		t.Errorf("error should wrap the underlying cause, got %v", err)
	}
	if !strings.Contains(err.Error(), "1 of 1") {
		t.Errorf("error should say how many chunks failed, got %q", err)
	}
}

func TestHandleLogFiles_ReportsFailuresAndKeepsGoodChunks(t *testing.T) {
	boom := errors.New("access denied")
	src := &scriptedSource{
		chunks:  []LogChunk{{ID: "bad"}, {ID: "good"}},
		content: map[string][]byte{"good": []byte(realIAMProduceLine + "\n")},
		failing: map[string]error{"bad": boom},
	}
	scanner, _ := NewClientInventoryScanner(src, types.State{}, ClientInventoryScannerOpts{})

	clients, failures := scanner.handleLogFiles(context.Background(), src.chunks)
	if len(failures) != 1 || !errors.Is(failures[0], boom) {
		t.Fatalf("want exactly the one read failure, got %v", failures)
	}
	if len(clients) != 1 {
		t.Fatalf("clients from the good chunk must be kept, got %d", len(clients))
	}
}

// Healthy chunks with no matching lines are not an error.
func TestRun_NoClientsWithoutFailuresIsNotAnError(t *testing.T) {
	src := &scriptedSource{
		chunks:  []LogChunk{{ID: "quiet"}},
		content: map[string][]byte{"quiet": []byte("[2025-07-25 14:45:53,662] INFO something else\n")},
	}
	scanner, _ := NewClientInventoryScanner(src, types.State{}, ClientInventoryScannerOpts{})
	if err := scanner.Run(); err != nil {
		t.Fatalf("want nil for a healthy empty window, got %v", err)
	}
}

// Clients from the chunks that did read are saved, and the run still fails so the exit code
// is non-zero.
func TestRun_PartialFailureSavesGoodClientsAndStillErrors(t *testing.T) {
	boom := errors.New("access denied")
	src := &scriptedSource{
		chunks:  []LogChunk{{ID: "bad"}, {ID: "good"}},
		content: map[string][]byte{"good": []byte(realIAMProduceLine + "\n")},
		failing: map[string]error{"bad": boom},
	}
	state := types.State{MSKSources: &types.MSKSourcesState{Regions: []types.DiscoveredRegion{
		{Name: "us-east-1", Clusters: []types.DiscoveredCluster{{Name: "c"}}},
	}}}
	stateFile := t.TempDir() + "/state.json"
	scanner, _ := NewClientInventoryScanner(src, state, ClientInventoryScannerOpts{Region: "us-east-1", ClusterName: "c", StateFile: stateFile})

	err := scanner.Run()
	if err == nil || !errors.Is(err, boom) {
		t.Fatalf("want the read failure returned, got %v", err)
	}
	if _, statErr := os.Stat(stateFile); statErr != nil {
		t.Fatalf("clients from the good chunk should have been saved: %v", statErr)
	}
}
