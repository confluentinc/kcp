package retry

import (
	"context"
	"crypto/x509"
	"errors"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/assert"
)

// statusError is an HTTP error that carries its status, as clusterlink's does.
type statusError int

func (e statusError) Error() string       { return fmt.Sprintf("unexpected status code %d", int(e)) }
func (e statusError) HTTPStatusCode() int { return int(e) }

func opErr(op string, errno syscall.Errno) error {
	return &net.OpError{Op: op, Net: "tcp", Err: os.NewSyscallError(op, errno)}
}

func TestRetryable(t *testing.T) {
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"connection reset":                    {opErr("read", syscall.ECONNRESET), true},
		"connection reset, wrapped":           {fmt.Errorf("listing consumer groups on broker b1: %w", opErr("read", syscall.ECONNRESET)), true},
		"broken pipe":                         {opErr("write", syscall.EPIPE), true},
		"connection refused":                  {opErr("dial", syscall.ECONNREFUSED), true},
		"connection aborted":                  {opErr("read", syscall.ECONNABORTED), true},
		"EOF":                                 {io.EOF, true},
		"unexpected EOF, wrapped":             {fmt.Errorf("reading: %w", io.ErrUnexpectedEOF), true},
		"HTTP client timeout":                 {&url.Error{Op: "Get", URL: "https://x", Err: context.DeadlineExceeded}, true},
		"sarama not connected":                {sarama.ErrNotConnected, true},
		"out of brokers after resets":         {sarama.Wrap(sarama.ErrOutOfBrokers, opErr("read", syscall.ECONNRESET)), true},
		"out of brokers after a failed login": {sarama.Wrap(sarama.ErrOutOfBrokers, sarama.ErrSASLAuthenticationFailed), false},
		"x509":                                {sarama.Wrap(sarama.ErrOutOfBrokers, x509.UnknownAuthorityError{}), false},
		"HTTP 429":                            {statusError(429), true},
		"HTTP 502":                            {statusError(502), true},
		"HTTP 503":                            {fmt.Errorf("listing: %w", statusError(503)), true},
		"HTTP 504":                            {statusError(504), true},
		"HTTP 500":                            {statusError(500), false},
		"HTTP 400":                            {statusError(400), false},
		"HTTP 401":                            {statusError(401), false},
		"HTTP 404":                            {statusError(404), false},
		"Kafka error code":                    {sarama.ErrGroupAuthorizationFailed, false},
		"coordinator loading":                 {sarama.ErrOffsetsLoadInProgress, false},
		"closed client":                       {sarama.ErrClosedClient, false},
		"cancelled":                           {context.Canceled, false},
		"gave up":                             {&GaveUpError{Operation: "rest.get", Err: opErr("read", syscall.ECONNRESET)}, false},
		"plain error":                         {errors.New("boom"), false},
		"nil":                                 {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			assert.Equal(t, tc.want, Retryable(tc.err))
		})
	}
}
