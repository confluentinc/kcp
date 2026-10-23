package testsupport

import (
	"net"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// CutListener is a TCP listener for a test server that can reset every connection it has accepted at once, the
// way a cluster drops its clients in a connection storm. It keeps accepting new connections afterwards. Hand it
// to sarama.NewMockBrokerListener to put a mock broker behind it.
type CutListener struct {
	net.Listener
	mu    sync.Mutex
	conns []net.Conn
}

// NewCutListener listens on a free loopback port.
func NewCutListener(t testing.TB) *CutListener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	return &CutListener{Listener: ln}
}

// Accept accepts a connection and remembers it for Cut.
func (l *CutListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	l.conns = append(l.conns, c)
	l.mu.Unlock()
	return c, nil
}

// Cut closes every connection accepted so far with a TCP reset.
func (l *CutListener) Cut() {
	l.mu.Lock()
	conns := l.conns
	l.conns = nil
	l.mu.Unlock()
	for _, c := range conns {
		if tc, ok := c.(*net.TCPConn); ok {
			_ = tc.SetLinger(0)
		}
		_ = c.Close()
	}
}
