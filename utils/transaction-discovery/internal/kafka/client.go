// Package kafka builds franz-go clients from the tool's connection config.
//
// franz-go is used (rather than IBM/sarama or confluent-kafka-go) because it exposes
// the record batch header (Record.ProducerID / Attrs.IsTransactional) on consumed
// records — which Phase 4 needs to correlate __consumer_offsets commits — and ships
// kmsg decoders and a kadm client for the consumer-group calls. Being pure Go it also
// produces a single static binary (CGO_ENABLED=0) that cross-compiles cleanly for
// handing to a customer, with no librdkafka or JRE to install.
package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/confluentinc/kcp/utils/transaction-discovery/internal/config"
)

// NewClient builds a franz-go client from cfg. Extra options let callers add
// consumer settings (used by the __transaction_state reader and the Phase 4
// __consumer_offsets tail) without duplicating auth/TLS.
func NewClient(cfg config.Config, extra ...kgo.Opt) (*kgo.Client, error) {
	if len(cfg.Brokers) == 0 {
		return nil, fmt.Errorf("no brokers configured")
	}

	opts := []kgo.Opt{kgo.SeedBrokers(cfg.Brokers...)}

	switch cfg.SASL {
	case config.SASLNone, "":
		// no auth
	case config.SASLPlain:
		opts = append(opts, kgo.SASL(plain.Auth{User: cfg.Username, Pass: cfg.Password}.AsMechanism()))
	case config.SASLScramSHA256:
		opts = append(opts, kgo.SASL(scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha256Mechanism()))
	case config.SASLScramSHA512:
		opts = append(opts, kgo.SASL(scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha512Mechanism()))
	default:
		return nil, fmt.Errorf("unsupported SASL mechanism %q", cfg.SASL)
	}

	if cfg.TLS {
		tc, err := buildTLSConfig(cfg)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tc))
	}

	opts = append(opts, extra...)
	return kgo.NewClient(opts...)
}

// NewAdmin builds a kadm.Client (for the KIP-664 admin methods) together with the
// underlying kgo.Client, which the caller must Close when done.
func NewAdmin(cfg config.Config) (*kadm.Client, *kgo.Client, error) {
	cl, err := NewClient(cfg)
	if err != nil {
		return nil, nil, err
	}
	return kadm.NewClient(cl), cl, nil
}

func buildTLSConfig(cfg config.Config) (*tls.Config, error) {
	tc := &tls.Config{MinVersion: tls.VersionTLS12}
	if cfg.TLSInsecure {
		tc.InsecureSkipVerify = true
	}
	if cfg.CACertFile != "" {
		pem, err := os.ReadFile(cfg.CACertFile)
		if err != nil {
			return nil, fmt.Errorf("read CA cert %q: %w", cfg.CACertFile, err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("no certificates parsed from %q", cfg.CACertFile)
		}
		tc.RootCAs = pool
	}
	// Mutual TLS: present a client certificate so the broker can authenticate the
	// client by its cert (common on self-managed Kafka / Confluent Platform). Both
	// the cert and its key are required together.
	if cfg.ClientCertFile != "" || cfg.ClientKeyFile != "" {
		if cfg.ClientCertFile == "" || cfg.ClientKeyFile == "" {
			return nil, fmt.Errorf("mutual TLS needs both --tls-cert and --tls-key")
		}
		cert, err := tls.LoadX509KeyPair(cfg.ClientCertFile, cfg.ClientKeyFile)
		if err != nil {
			return nil, fmt.Errorf("load client cert/key: %w", err)
		}
		tc.Certificates = []tls.Certificate{cert}
	}
	return tc, nil
}
