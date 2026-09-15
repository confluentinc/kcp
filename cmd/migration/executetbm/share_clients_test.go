package executetbm

import (
	"testing"

	"github.com/confluentinc/kcp/internal/services/offset"
	"github.com/confluentinc/kcp/internal/testsupport"
	"github.com/stretchr/testify/assert"
)

func TestOffsetClient(t *testing.T) {
	t.Run("returns the underlying client for a real offset service", func(t *testing.T) {
		_, c := testsupport.MockSaramaClient(t)
		p := offset.NewOffsetService(c)
		assert.True(t, offset.ClientOf(p) == c, "offset.ClientOf must surface the service's own client for reuse")
	})

	t.Run("returns nil for a provider that exposes no client", func(t *testing.T) {
		// A stub provider (as the command's own tests use) surfaces no client, so
		// reconcile falls back to dialing its own topic-lister connections.
		assert.Nil(t, offset.ClientOf(zeroLagOffsetProvider{}))
	})
}
