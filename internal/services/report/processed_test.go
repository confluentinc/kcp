package report

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestProcessedSource_TypeDiscrimination(t *testing.T) {
	// Test MSK source
	mskSource := ProcessedSource{
		Type: types.SourceTypeMSK,
		MSKData: &ProcessedMSKSource{
			Regions: []ProcessedRegion{},
		},
	}
	if mskSource.Type != types.SourceTypeMSK {
		t.Errorf("Expected MSK type, got %s", mskSource.Type)
	}
	if mskSource.MSKData == nil {
		t.Error("MSKData should not be nil for MSK source")
	}

	// Test OSK source
	oskSource := ProcessedSource{
		Type: types.SourceTypeOSK,
		OSKData: &ProcessedOSKSource{
			Clusters: []ProcessedOSKCluster{},
		},
	}
	if oskSource.Type != types.SourceTypeOSK {
		t.Errorf("Expected OSK type, got %s", oskSource.Type)
	}
	if oskSource.OSKData == nil {
		t.Error("OSKData should not be nil for OSK source")
	}
}
