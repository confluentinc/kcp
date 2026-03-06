package msk_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources/msk"
	"github.com/confluentinc/kcp/internal/types"
)

func TestMSKSource_Type(t *testing.T) {
	source := msk.NewMSKSource()
	if source.Type() != types.SourceTypeMSK {
		t.Errorf("expected source type %s, got %s", types.SourceTypeMSK, source.Type())
	}
}

func TestMSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := msk.NewMSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}
