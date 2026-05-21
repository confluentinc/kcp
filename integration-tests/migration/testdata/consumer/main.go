// Minimal sarama consumer for the migration e2e tests.
//
// Joins --group, reads up to --max-messages from --topic, commits offsets,
// and exits. Plaintext unauthenticated.
package main

import (
	"context"
	"flag"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/IBM/sarama"
)

type handler struct {
	maxMessages int
	mu          sync.Mutex
	consumed    int
	done        chan struct{}
	once        sync.Once
}

func (h *handler) Setup(sarama.ConsumerGroupSession) error   { return nil }
func (h *handler) Cleanup(sarama.ConsumerGroupSession) error { return nil }

func (h *handler) ConsumeClaim(session sarama.ConsumerGroupSession, claim sarama.ConsumerGroupClaim) error {
	for msg := range claim.Messages() {
		session.MarkMessage(msg, "")
		h.mu.Lock()
		h.consumed++
		reached := h.consumed >= h.maxMessages
		h.mu.Unlock()
		if reached {
			h.once.Do(func() { close(h.done) })
			return nil
		}
	}
	return nil
}

func main() {
	var (
		bootstrap   = flag.String("bootstrap", "", "comma-separated Kafka bootstrap servers (required)")
		topic       = flag.String("topic", "", "topic to consume from (required)")
		group       = flag.String("group", "kcp-e2e-consumer-group", "consumer group id")
		maxMessages = flag.Int("max-messages", 10, "stop after this many messages")
		timeout     = flag.Duration("timeout", 60*time.Second, "give up if max-messages not reached within this duration")
		clientID    = flag.String("client-id", "kcp-e2e-consumer", "Kafka client id")
	)
	flag.Parse()

	if *bootstrap == "" || *topic == "" {
		log.Fatal("--bootstrap and --topic are required")
	}

	cfg := sarama.NewConfig()
	cfg.Version = sarama.V2_8_0_0
	cfg.ClientID = *clientID
	// Read from the earliest available offset on first join (gives us deterministic
	// behavior in tests where the producer started writing seconds before).
	cfg.Consumer.Offsets.Initial = sarama.OffsetOldest
	cfg.Consumer.Offsets.AutoCommit.Enable = true
	cfg.Consumer.Offsets.AutoCommit.Interval = 500 * time.Millisecond

	brokers := strings.Split(*bootstrap, ",")
	cg, err := sarama.NewConsumerGroup(brokers, *group, cfg)
	if err != nil {
		log.Fatalf("failed to create consumer group: %v", err)
	}
	defer cg.Close()

	h := &handler{maxMessages: *maxMessages, done: make(chan struct{})}

	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	go func() {
		<-h.done
		cancel()
	}()

	go func() {
		for err := range cg.Errors() {
			log.Printf("consumer group error: %v", err)
		}
	}()

	for {
		if err := cg.Consume(ctx, []string{*topic}, h); err != nil && err != sarama.ErrClosedConsumerGroup {
			log.Printf("consume error: %v", err)
		}
		if ctx.Err() != nil {
			break
		}
	}

	h.mu.Lock()
	final := h.consumed
	h.mu.Unlock()
	log.Printf("consumer exiting — group=%s topic=%s consumed=%d/%d", *group, *topic, final, *maxMessages)
}
