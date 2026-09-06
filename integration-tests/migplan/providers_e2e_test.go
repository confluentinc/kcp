//go:build e2e

// Package migplan_e2e holds LIVE integration tests for the migplan migration
// reconciliation engine. They run only under `-tags e2e` and require the local
// docker-compose environment (source + dest cp-server + cluster link) to be up.
// See README.md and setup.sh in this directory.
package migplan_e2e

import (
	"context"
	"net/http"
	"testing"

	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/services/migplan/providers"
	"github.com/confluentinc/kcp/internal/services/migplan/reconcile"
)

const (
	sourceBroker     = "localhost:19092"
	destBroker       = "localhost:29092"
	destRESTEndpoint = "http://localhost:28090"
	sourceClusterID  = "6ub6fPVJRzKjE4i-REkq-A"
	destClusterID    = "LKsbYRvfTM-TVXKjdjgdxA"
	linkName         = "migplan-link"
	kafkaVersion     = "4.0.0"
)

// newPlaintextLister builds a live topic lister against a plaintext broker.
func newPlaintextLister(t *testing.T, broker string) *providers.KafkaTopicLister {
	t.Helper()
	admin, err := client.NewKafkaAdmin(
		[]string{broker},
		kafkatypes.ClientBrokerPlaintext,
		"", // region unused for plaintext
		kafkaVersion,
		client.WithUnauthenticatedPlaintextAuth(),
	)
	if err != nil {
		t.Fatalf("NewKafkaAdmin(%s): %v", broker, err)
	}
	return providers.NewKafkaTopicLister(admin)
}

func contains(ss []string, want string) bool {
	for _, s := range ss {
		if s == want {
			return true
		}
	}
	return false
}

// TestSourceTopicListerLive asserts the source lister returns the four user
// topics and never __consumer_offsets.
func TestSourceTopicListerLive(t *testing.T) {
	got, err := newPlaintextLister(t, sourceBroker).ListTopics(context.Background())
	if err != nil {
		t.Fatalf("ListTopics(source): %v", err)
	}
	t.Logf("source topics: %v", got)

	for _, want := range []string{"team-a.orders", "team-a.payments", "billing-v2", "team-b.audit"} {
		if !contains(got, want) {
			t.Errorf("source topics missing %q; got %v", want, got)
		}
	}
	if contains(got, "__consumer_offsets") {
		t.Errorf("source topics must not include __consumer_offsets; got %v", got)
	}
}

// TestTargetTopicListerLive asserts the destination lister returns the three
// mirror topics.
func TestTargetTopicListerLive(t *testing.T) {
	got, err := newPlaintextLister(t, destBroker).ListTopics(context.Background())
	if err != nil {
		t.Fatalf("ListTopics(target): %v", err)
	}
	t.Logf("target topics: %v", got)

	for _, want := range []string{"team-a.orders", "team-a.payments", "billing-v2"} {
		if !contains(got, want) {
			t.Errorf("target topics missing mirror %q; got %v", want, got)
		}
	}
	// team-b.audit is deliberately NOT mirrored, so it must be absent on target.
	if contains(got, "team-b.audit") {
		t.Errorf("team-b.audit is not mirrored and must be absent on target; got %v", got)
	}
}

// TestClusterLinkStatusLive asserts the cluster-link provider reads the real
// link: three ACTIVE mirrors keyed by SOURCE topic name, the un-mirrored
// team-b.audit absent from the map, and OffsetSyncEnabled read live from the
// link's own consumer.offset.sync.enable config (setup.sh leaves it off ⇒ false).
func TestClusterLinkStatusLive(t *testing.T) {
	svc := clusterlink.NewConfluentCloudService(http.DefaultClient)
	cfg := clusterlink.Config{
		RestEndpoint: destRESTEndpoint,
		ClusterID:    destClusterID,
		LinkName:     linkName,
		Topics:       []string{}, // empty => all mirrors on the link
		// REST has no auth; nil falls back to empty basic-auth which the
		// no-auth server ignores. Verified live (see report).
		Auth: nil,
	}

	ls, err := providers.NewClusterLinkStatus(svc, cfg).LinkStatus(context.Background())
	if err != nil {
		t.Fatalf("LinkStatus: %v", err)
	}
	t.Logf("link mirrors: %v  offsetSyncEnabled=%v", ls.Mirrors, ls.OffsetSyncEnabled)

	for _, want := range []string{"team-a.orders", "team-a.payments", "billing-v2"} {
		if ls.Mirrors[want] != reconcile.MirrorActive {
			t.Errorf("Mirrors[%q] = %v, want %v (ACTIVE)", want, ls.Mirrors[want], reconcile.MirrorActive)
		}
	}
	if _, ok := ls.Mirrors["team-b.audit"]; ok {
		t.Errorf("team-b.audit is not a mirror; it must be absent from the map, got %v", ls.Mirrors["team-b.audit"])
	}
	if ls.OffsetSyncEnabled {
		t.Error("OffsetSyncEnabled should be false for this link")
	}
	// the link reports its source cluster id (read live from the describe)
	if ls.SourceClusterID != sourceClusterID {
		t.Errorf("SourceClusterID = %q, want %q", ls.SourceClusterID, sourceClusterID)
	}
}
