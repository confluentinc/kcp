package osk_test

import (
	"testing"

	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/osk"
)

func TestOSKSource_Type(t *testing.T) {
	source := osk.NewOSKSource()
	if source.Type() != sources.SourceTypeOSK {
		t.Errorf("expected source type %s, got %s", sources.SourceTypeOSK, source.Type())
	}
}

func TestOSKSource_GetClusters_BeforeLoad(t *testing.T) {
	source := osk.NewOSKSource()
	clusters := source.GetClusters()
	if clusters != nil {
		t.Error("expected nil clusters before loading credentials")
	}
}

func TestOSKSource_LoadCredentials_FileNotFound(t *testing.T) {
	source := osk.NewOSKSource()
	err := source.LoadCredentials("nonexistent.yaml")
	if err == nil {
		t.Error("expected error when loading nonexistent credentials file")
	}
}
