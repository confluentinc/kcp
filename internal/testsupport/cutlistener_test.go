package testsupport

import (
	"errors"
	"io"
	"net"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func echoOnce(t *testing.T, c net.Conn) {
	t.Helper()
	_, err := c.Write([]byte("a"))
	require.NoError(t, err)
	buf := make([]byte, 1)
	_ = c.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = io.ReadFull(c, buf)
	require.NoError(t, err)
}

func TestCutListener_ResetsAcceptedConnectionsAndKeepsAccepting(t *testing.T) {
	l := NewCutListener(t)
	t.Cleanup(func() { _ = l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { _, _ = io.Copy(c, c) }()
		}
	}()

	c1, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c1.Close() })
	echoOnce(t, c1)

	l.Cut()

	_ = c1.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, err = c1.Read(make([]byte, 1))
	require.Error(t, err, "a cut connection must fail")
	assert.False(t, errors.Is(err, os.ErrDeadlineExceeded), "it must fail because of the cut, not the deadline")

	c2, err := net.Dial("tcp", l.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = c2.Close() })
	echoOnce(t, c2)
}
