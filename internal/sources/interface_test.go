package sources_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
)

func TestSourceType_Constants(t *testing.T) {
	if sources.SourceTypeMSK != "msk" {
		t.Errorf("expected SourceTypeMSK to be 'msk', got '%s'", sources.SourceTypeMSK)
	}
	if sources.SourceTypeOSK != "osk" {
		t.Errorf("expected SourceTypeOSK to be 'osk', got '%s'", sources.SourceTypeOSK)
	}
}

func TestClusterIdentifier_Structure(t *testing.T) {
	id := sources.ClusterIdentifier{
		Name:             "test-cluster",
		UniqueID:         "cluster-123",
		BootstrapServers: []string{"broker1:9092", "broker2:9092"},
	}

	if id.Name != "test-cluster" {
		t.Errorf("expected Name 'test-cluster', got '%s'", id.Name)
	}
	if len(id.BootstrapServers) != 2 {
		t.Errorf("expected 2 bootstrap servers, got %d", len(id.BootstrapServers))
	}
}
