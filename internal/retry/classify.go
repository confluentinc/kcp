package retry

import (
	"errors"
	"io"
	"net"
	"syscall"

	"github.com/IBM/sarama"
)

// statusCoder is an HTTP error that carries its response status; clusterlink's errors do.
type statusCoder interface {
	HTTPStatusCode() int
}

// connectionErrors are the failures of a connection itself: what a cluster resetting or dropping clients
// produces.
var connectionErrors = []error{
	syscall.ECONNRESET,
	syscall.EPIPE,
	syscall.ECONNREFUSED,
	syscall.ECONNABORTED,
	io.EOF,
	io.ErrUnexpectedEOF,
	sarama.ErrNotConnected,
}

// Retryable reports whether err is a connection-level failure worth trying again: a reset, dropped, refused
// or timed-out connection, or an HTTP 429, 502, 503 or 504. A Kafka error code, any other HTTP status, a
// failed login, a TLS error, a closed client, a cancellation and a *GaveUpError are not. sarama's
// ErrOutOfBrokers is judged by the errors it wraps, so a failed login stays a failure. Do checks its own
// context before asking, so a caller's deadline is never retried even though a deadline is a timeout.
func Retryable(err error) bool {
	if err == nil {
		return false
	}
	var gave *GaveUpError
	if errors.As(err, &gave) {
		return false
	}
	for _, target := range connectionErrors {
		if errors.Is(err, target) {
			return true
		}
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return true
	}
	var sc statusCoder
	if errors.As(err, &sc) {
		switch sc.HTTPStatusCode() {
		case 429, 502, 503, 504:
			return true
		}
	}
	return false
}
