package msk_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/msk"
)

func TestMSKSource_Type(t *testing.T) {
	source := msk.NewMSKSource()
	if source.Type() != sources.SourceTypeMSK {
		t.Errorf("expected source type %s, got %s", sources.SourceTypeMSK, source.Type())
	}
}

func TestMSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := msk.NewMSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}
