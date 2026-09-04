package providers

import (
	"context"
	"errors"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

type fakeLinkReader struct {
	mirrors []clusterlink.MirrorTopic
	configs map[string]string
	err     error
	cfgErr  error
}

func (f *fakeLinkReader) ListMirrorTopics(_ context.Context, _ clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	return f.mirrors, f.err
}

func (f *fakeLinkReader) ListConfigs(_ context.Context, _ clusterlink.Config) (map[string]string, error) {
	return f.configs, f.cfgErr
}

func TestClusterLinkStatusMapping(t *testing.T) {
	f := &fakeLinkReader{mirrors: []clusterlink.MirrorTopic{
		{SourceTopicName: "a", MirrorStatus: clusterlink.MirrorStatusActive},
		{SourceTopicName: "b", MirrorStatus: clusterlink.MirrorStatusStopped},
		{SourceTopicName: "c", MirrorStatus: "PENDING_STOPPED"},
	}}
	ls, err := NewClusterLinkStatus(f, clusterlink.Config{}).LinkStatus(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if ls.OffsetSyncEnabled {
		t.Error("OffsetSyncEnabled should be false when the config is absent")
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

// TestClusterLinkStatusOffsetSyncEnabled proves the provider reads
// consumer.offset.sync.enable from the LIVE link config (not a supplied flag):
// only an explicit "true" is on; any other value or its absence is off.
func TestClusterLinkStatusOffsetSyncEnabled(t *testing.T) {
	cases := []struct {
		name    string
		configs map[string]string
		want    bool
	}{
		{"explicit true", map[string]string{offsetSyncEnableConfig: "true"}, true},
		{"explicit false", map[string]string{offsetSyncEnableConfig: "false"}, false},
		{"absent (link default)", map[string]string{"some.other.config": "x"}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := &fakeLinkReader{configs: c.configs}
			ls, err := NewClusterLinkStatus(f, clusterlink.Config{}).LinkStatus(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if ls.OffsetSyncEnabled != c.want {
				t.Errorf("OffsetSyncEnabled = %v, want %v", ls.OffsetSyncEnabled, c.want)
			}
		})
	}
}

func TestClusterLinkStatusError(t *testing.T) {
	// a mirror-list error propagates
	if _, err := NewClusterLinkStatus(&fakeLinkReader{err: errors.New("mirror boom")}, clusterlink.Config{}).LinkStatus(context.Background()); err == nil {
		t.Fatal("expected the mirror-list error to propagate")
	}
	// a config-read error propagates too
	if _, err := NewClusterLinkStatus(&fakeLinkReader{cfgErr: errors.New("config boom")}, clusterlink.Config{}).LinkStatus(context.Background()); err == nil {
		t.Fatal("expected the config-read error to propagate")
	}
}
