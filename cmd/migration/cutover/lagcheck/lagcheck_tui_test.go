package lagcheck

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/retry"
	"github.com/confluentinc/kcp/internal/retry/retrytest"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

func TestFormatLag_Zero(t *testing.T) {
	got := formatLag(0)
	assert.Equal(t, "0", got)
}

func TestFormatLag_Small(t *testing.T) {
	got := formatLag(999)
	assert.Equal(t, "999", got)
}

func TestFormatLag_Thousands(t *testing.T) {
	tests := []struct {
		input int
		want  string
	}{
		{1000, "1,000"},
		{21655, "21,655"},
	}
	for _, tc := range tests {
		got := formatLag(tc.input)
		assert.Equal(t, tc.want, got, "formatLag(%d)", tc.input)
	}
}

func TestFormatLag_Millions(t *testing.T) {
	got := formatLag(1000000)
	assert.Equal(t, "1,000,000", got)
}

func TestTotalLag(t *testing.T) {
	topic := clusterlink.MirrorTopic{
		MirrorTopicName: "test-topic",
		MirrorStatus:    "ACTIVE",
		MirrorLags: []clusterlink.MirrorLag{
			{Partition: 0, Lag: 100},
			{Partition: 1, Lag: 200},
			{Partition: 2, Lag: 300},
		},
	}
	got := totalLag(topic)
	assert.Equal(t, 600, got)
}

func TestTotalLag_Empty(t *testing.T) {
	topic := clusterlink.MirrorTopic{
		MirrorTopicName: "empty-topic",
		MirrorStatus:    "ACTIVE",
		MirrorLags:      []clusterlink.MirrorLag{},
	}
	got := totalLag(topic)
	assert.Equal(t, 0, got)
}

func TestRenderSparkline_Empty(t *testing.T) {
	got := renderSparkline([]int{})
	assert.Equal(t, "-", got)
}

func TestRenderSparkline_AllZeros(t *testing.T) {
	got := renderSparkline([]int{0, 0, 0})
	assert.Equal(t, "▁▁▁", got)
}

func TestRenderSparkline_Ascending(t *testing.T) {
	got := renderSparkline([]int{0, 50, 100})

	// Should have exactly 3 runes
	runeCount := utf8.RuneCountInString(got)
	require.Equal(t, 3, runeCount, "renderSparkline(ascending) rune count")

	runes := []rune(got)
	lowest := sparkBlocks[0]                   // ▁
	highest := sparkBlocks[len(sparkBlocks)-1] // █

	assert.Equal(t, lowest, runes[0], "first rune should be lowest block")
	assert.Equal(t, highest, runes[2], "last rune should be highest block")
}

func TestRenderSparkline_SingleValue(t *testing.T) {
	got := renderSparkline([]int{100})
	want := string(sparkBlocks[len(sparkBlocks)-1]) // █
	assert.Equal(t, want, got)
}

// TestFetchCmd_MakesOneRequestAndShowsItsError — the view shows a failed refresh as it happened and polls again;
// it does not retry the request, which would hold up the view and write retry warnings over it.
func TestFetchCmd_MakesOneRequestAndShowsItsError(t *testing.T) {
	retrytest.Fast(t)
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer server.Close()
	cfg := clusterlink.Config{RestEndpoint: server.URL, ClusterID: "lkc-1", LinkName: "link-1", APIKey: "k", APISecret: "s"}

	msg := fetchCmd(clusterlink.NewConfluentCloudService(server.Client()), cfg)()

	res, ok := msg.(fetchResultMsg)
	require.True(t, ok, "fetchCmd answers with a fetchResultMsg, got %T", msg)
	require.Error(t, res.err)
	assert.Contains(t, res.err.Error(), "unexpected status code 503")
	var gave *retry.GaveUpError
	assert.False(t, errors.As(res.err, &gave), "the error is the request's own, not a retry's: %v", res.err)
	assert.Equal(t, int32(1), hits.Load())
}
