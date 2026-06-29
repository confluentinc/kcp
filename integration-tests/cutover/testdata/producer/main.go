// Minimal sarama producer used in the cutover e2e tests.
//
// Writes records to --topic at --rate records/sec for --duration. Exits
// cleanly on SIGTERM/SIGINT. Plaintext unauthenticated — matches the e2e
// fixture cluster link's auth posture.
package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/IBM/sarama"
)

func main() {
	var (
		bootstrap = flag.String("bootstrap", "", "comma-separated Kafka bootstrap servers (required)")
		topic     = flag.String("topic", "", "topic to produce to (required)")
		duration  = flag.Duration("duration", 5*time.Minute, "how long to run before exiting cleanly")
		rate      = flag.Int("rate", 10, "records per second")
		clientID  = flag.String("client-id", "kcp-e2e-producer", "Kafka client id")
	)
	flag.Parse()

	if *bootstrap == "" || *topic == "" {
		log.Fatal("--bootstrap and --topic are required")
	}

	cfg := sarama.NewConfig()
	cfg.Producer.Return.Successes = true
	cfg.Producer.RequiredAcks = sarama.WaitForAll
	cfg.ClientID = *clientID
	cfg.Version = sarama.V2_8_0_0

	brokers := strings.Split(*bootstrap, ",")
	producer, err := sarama.NewSyncProducer(brokers, cfg)
	if err != nil {
		log.Fatalf("failed to create sync producer: %v", err)
	}
	defer producer.Close()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)

	deadline := time.Now().Add(*duration)
	interval := time.Second / time.Duration(*rate)
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	var count int
	log.Printf("producer started — topic=%s rate=%d/s duration=%s", *topic, *rate, *duration)

	for {
		select {
		case sig := <-sigCh:
			log.Printf("received %s — exiting after %d records", sig, count)
			return
		case now := <-ticker.C:
			if now.After(deadline) {
				log.Printf("duration elapsed — exiting after %d records", count)
				return
			}
			msg := &sarama.ProducerMessage{
				Topic: *topic,
				Value: sarama.StringEncoder(fmt.Sprintf("record-%d", count)),
			}
			if _, _, err := producer.SendMessage(msg); err != nil {
				log.Printf("send error (continuing): %v", err)
				continue
			}
			count++
			if count%100 == 0 {
				log.Printf("produced %d records", count)
			}
		}
	}
}
