package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

type fakeMirrorLister struct {
	mirrors []clusterlink.MirrorTopic
	err     error
}

func (f *fakeMirrorLister) ListMirrorTopics(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	return f.mirrors, f.err
}

func TestClusterLinkStatusMapping(t *testing.T) {
	f := &fakeMirrorLister{mirrors: []clusterlink.MirrorTopic{
		{SourceTopicName: "a", MirrorStatus: clusterlink.MirrorStatusActive},
		{SourceTopicName: "b", MirrorStatus: clusterlink.MirrorStatusStopped},
		{SourceTopicName: "c", MirrorStatus: "PENDING_STOPPED"},
	}}
	ls, err := NewClusterLinkStatus(f, clusterlink.Config{}, false).LinkStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ls.OffsetSyncEnabled {
		t.Error("OffsetSyncEnabled should be false")
	}
	want := map[string]reconcile.MirrorState{
		"a": reconcile.MirrorActive,
		"b": reconcile.MirrorStopped,
		"c": reconcile.MirrorBad,
	}
	for k, v := range want {
		if ls.Mirrors[k] != v {
			t.Errorf("Mirrors[%q] = %v, want %v", k, ls.Mirrors[k], v)
		}
	}
	// a topic that is not a mirror is simply absent from the map (→ MirrorNone
	// via the zero value when the core looks it up)
	if _, ok := ls.Mirrors["not-a-mirror"]; ok {
		t.Error("a non-mirror topic must be absent from the map")
	}
}

func TestClusterLinkStatusOffsetSyncEnabled(t *testing.T) {
	ls, err := NewClusterLinkStatus(&fakeMirrorLister{}, clusterlink.Config{}, true).LinkStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !ls.OffsetSyncEnabled {
		t.Error("OffsetSyncEnabled should be true")
	}
}

func TestClusterLinkStatusError(t *testing.T) {
	f := &fakeMirrorLister{err: errors.New("link boom")}
	if _, err := NewClusterLinkStatus(f, clusterlink.Config{}, false).LinkStatus(context.Background()); err == nil {
		t.Fatal("expected the list error to propagate")
	}
}
