package migplan

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const starGroupFilter = `{"groupFilters":[{"name":"*","patternType":"LITERAL","filterType":"INCLUDE","topicTypes":["LOCAL_MIRROR"]}]}`

// fakeCopyReader is one cluster's REST view of its copy of the link.
type fakeCopyReader struct {
	link    *clusterlink.ClusterLink
	linkErr error
	configs map[string]string
	cfgErr  error
	mirrors []clusterlink.MirrorTopic
	mirErr  error
	calls   []string
}

func (f *fakeCopyReader) GetClusterLink(context.Context, clusterlink.Config) (*clusterlink.ClusterLink, error) {
	f.calls = append(f.calls, "link")
	return f.link, f.linkErr
}

func (f *fakeCopyReader) ListConfigs(context.Context, clusterlink.Config) (map[string]string, error) {
	f.calls = append(f.calls, "configs")
	return f.configs, f.cfgErr
}

func (f *fakeCopyReader) ListMirrorTopics(context.Context, clusterlink.Config) ([]clusterlink.MirrorTopic, error) {
	f.calls = append(f.calls, "mirrors")
	return f.mirrors, f.mirErr
}

func healthyCopyReader() *fakeCopyReader {
	return &fakeCopyReader{
		link: &clusterlink.ClusterLink{LinkName: "rev-link", LinkID: "link-1", ClusterID: "tgt-id", RemoteClusterID: "src-id",
			LinkState: "ACTIVE", LinkError: "NO_ERROR"},
		configs: map[string]string{
			"link.mode":                     "BIDIRECTIONAL",
			"cluster.link.prefix":           "",
			"consumer.offset.sync.enable":   "true",
			"consumer.offset.group.filters": starGroupFilter,
			"consumer.offset.sync.ms":       "5000",
		},
		mirrors: []clusterlink.MirrorTopic{
			{MirrorTopicName: "a", SourceTopicName: "a", MirrorStatus: "ACTIVE"},
			{MirrorTopicName: "b", SourceTopicName: "b", MirrorStatus: "PENDING_MIRROR"},
		},
	}
}

func TestClusterLinkCopy_ReadsEveryField(t *testing.T) {
	got, err := NewClusterLinkCopy(healthyCopyReader(), clusterlink.Config{}).LinkCopy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, reconcile.LinkCopy{
		Found:             true,
		LinkID:            "link-1",
		RemoteClusterID:   "src-id",
		LinkError:         "NO_ERROR",
		Mode:              "BIDIRECTIONAL",
		OffsetSyncEnabled: true,
		GroupFilters:      starGroupFilter,
		Rows: map[string]reconcile.MirrorRow{
			"a": {Status: reconcile.RowActive, Raw: "ACTIVE"},
			"b": {Status: reconcile.RowPendingMirror, Raw: "PENDING_MIRROR"},
		},
	}, got)
}

func TestClusterLinkCopy_PrefixAndSyncOffRead(t *testing.T) {
	f := healthyCopyReader()
	f.configs["cluster.link.prefix"] = "src."
	f.configs["consumer.offset.sync.enable"] = "false"
	got, err := NewClusterLinkCopy(f, clusterlink.Config{}).LinkCopy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "src.", got.Prefix)
	assert.False(t, got.OffsetSyncEnabled)
}

func TestClusterLinkCopy_NotFoundIsNotAnError(t *testing.T) {
	f := &fakeCopyReader{linkErr: fmt.Errorf("cluster link %q not found: %w", "rev-link", clusterlink.ErrLinkNotFound)}
	got, err := NewClusterLinkCopy(f, clusterlink.Config{}).LinkCopy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, reconcile.LinkCopy{}, got)
	assert.Equal(t, []string{"link"}, f.calls, "a missing copy has nothing else to read")
}

// A link created on one cluster only leaves a nameless UNMANAGED_SOURCE entry
// on the other, which must read as never created, not as a copy.
func TestClusterLinkCopy_NamelessPlaceholderIsNotFound(t *testing.T) {
	f := &fakeCopyReader{link: &clusterlink.ClusterLink{LinkID: "link-1", LinkState: "UNMANAGED_SOURCE"}}
	got, err := NewClusterLinkCopy(f, clusterlink.Config{}).LinkCopy(context.Background())
	require.NoError(t, err)
	assert.False(t, got.Found)
	assert.Equal(t, []string{"link"}, f.calls)
}

func TestClusterLinkCopy_RowsKeyedByMirrorName(t *testing.T) {
	f := healthyCopyReader()
	f.mirrors = []clusterlink.MirrorTopic{{MirrorTopicName: "src.orders", SourceTopicName: "orders", MirrorStatus: "ACTIVE"}}
	got, err := NewClusterLinkCopy(f, clusterlink.Config{}).LinkCopy(context.Background())
	require.NoError(t, err)
	assert.Equal(t, map[string]reconcile.MirrorRow{"src.orders": {Status: reconcile.RowActive, Raw: "ACTIVE"}}, got.Rows)
}

func TestClusterLinkCopy_ReadErrorsPropagate(t *testing.T) {
	boom := errors.New("boom")
	for name, mutate := range map[string]func(f *fakeCopyReader){
		"describe": func(f *fakeCopyReader) { f.linkErr = boom },
		"configs":  func(f *fakeCopyReader) { f.cfgErr = boom },
		"mirrors":  func(f *fakeCopyReader) { f.mirErr = boom },
	} {
		t.Run(name, func(t *testing.T) {
			f := healthyCopyReader()
			mutate(f)
			_, err := NewClusterLinkCopy(f, clusterlink.Config{}).LinkCopy(context.Background())
			require.ErrorIs(t, err, boom)
		})
	}
}

func TestMapRowStatus(t *testing.T) {
	for status, want := range map[string]reconcile.RowStatus{
		"ACTIVE":              reconcile.RowActive,
		"STOPPED":             reconcile.RowStopped,
		"PENDING_SYNCHRONIZE": reconcile.RowPendingSynchronize,
		"PENDING_MIRROR":      reconcile.RowPendingMirror,
		"PENDING_STOPPED":     reconcile.RowPendingStopped,
		"PAUSED":              reconcile.RowOther,
		"FAILED":              reconcile.RowOther,
		"":                    reconcile.RowOther,
	} {
		assert.Equal(t, want, mapRowStatus(status), "status %q", status)
	}
}
