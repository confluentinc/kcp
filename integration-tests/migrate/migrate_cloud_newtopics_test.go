//go:build integration

package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/IBM/sarama"
	"github.com/stretchr/testify/require"
)

// TestCloud_MSKtoCC_NewTopics exercises the mode:new MSK→CC path end-to-end live
// (the cloud suite otherwise only covers mirror mode): KCP reads an existing
// source topic and recreates it on Confluent Cloud.
//
// This is the live proof of the M2 fix's wiring: KCP no longer sends
// replication_factor, so CC applies its default (3) and the create succeeds.
// The RF≠3-rejection itself — the original bug — is proven by the unit test
// asserting the CreateTopic request body OMITS replication_factor, plus the
// verified CC behavior (replication_factor=1 → 400 "must be 3"; omitted → 201).
// A live RF≠3 source can't be exercised here: kafka-user-1 cannot create topics
// on the MSK playground and every topic it can read is RF=3, so seeding an RF≠3
// source would require IAM/AWS or a CREATE grant.
func TestCloud_MSKtoCC_NewTopics(t *testing.T) {
	cfg := loadCloudConfig(t)
	rc := cfg.ccRestClient()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "scram")

	// An existing source topic kafka-user-1 is authorized to read.
	const srcTopic = "test-topic-1"
	defer rc.deleteTopic(t, cfg.ccClusterID, srcTopic)

	mf := writeNewModeCloudManifest(t, dir, "m2-newtopics", cfg, target, sourceRead, splitCSV(cfg.mskScramBootstrap), []string{srcTopic})

	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.True(t, rc.topicExists(cfg.ccClusterID, srcTopic),
		"mode:new must create %q on CC (replication_factor omitted → CC default):\n%s", srcTopic, out)
}

// TestCloud_MSKtoCC_NewTopics_RF2Source is the true RF≠3 regression for M2: it
// seeds an RF=2 topic on the MSK playground (valid on its 3 brokers; CC requires
// RF=3), then mode:new MSK→CC must still create it — because KCP omits the source
// replication factor and CC applies its default (3). Pre-fix, KCP forwarded the
// source RF and the create failed with 40002 "replication factor must be 3".
// Seeded and read via IAM (admin principal) — needs AWS creds, like the other IAM
// cloud tests; kafka-user-1 (SCRAM) cannot create topics.
func TestCloud_MSKtoCC_NewTopics_RF2Source(t *testing.T) {
	cfg := loadCloudConfig(t)
	rc := cfg.ccRestClient()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")

	topic := fmt.Sprintf("m2-rf2-%d", time.Now().UnixNano())
	iamBootstrap := splitCSV(cfg.mskIAMBootstrap)
	admin := newMSKIAMAdmin(t, iamBootstrap, cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	require.NoError(t, admin.CreateTopic(topic, &sarama.TopicDetail{NumPartitions: 2, ReplicationFactor: 2}, false),
		"seed RF=2 source topic on MSK")
	defer func() { _ = admin.DeleteTopic(topic) }()
	defer rc.deleteTopic(t, cfg.ccClusterID, topic)

	mf := writeNewModeCloudManifest(t, dir, "m2-rf2", cfg, target, sourceRead, iamBootstrap, []string{topic})

	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.True(t, rc.topicExists(cfg.ccClusterID, topic),
		"mode:new must create the RF=2 source topic on CC (RF omitted → CC default 3):\n%s", out)
}

// writeNewModeCloudManifest writes a mode:new MSK→CC manifest (no cluster link)
// and returns its path.
func writeNewModeCloudManifest(t *testing.T, dir, name string, cfg cloudConfig, target, sourceRead string, sourceBootstrap, include []string) string {
	t.Helper()
	yamlList := func(ss []string) string { return "[\"" + strings.Join(ss, "\",\"") + "\"]" }
	b := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: " + name + "\nspec:\n" +
		"  source:\n    type: msk\n    bootstrapServers: " + yamlList(sourceBootstrap) + "\n    credentials: " + sourceRead + "\n" +
		"  target:\n    type: confluent-cloud\n    clusterId: " + cfg.ccClusterID + "\n    credentials: " + target + "\n    kafka:\n      restEndpoint: " + cfg.ccRestEndpoint + "\n" +
		"  topics:\n    mode: new\n    include: " + yamlList(include) + "\n"
	mf := filepath.Join(dir, name+".yaml")
	require.NoError(t, os.WriteFile(mf, []byte(b), 0600))
	return mf
}
