//go:build integration

package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file adds live coverage for the spec.topics feature on top of the
// cluster-link auth matrix harness:
//
//   - TestMigrateApply_Topics_MirrorDestination — mode:mirror in destination
//     topology (mirrors created on the target dest, prefix applied, idempotent).
//   - TestMigrateApply_Topics_MirrorSourceInitiated — mode:mirror in source
//     (push) topology (prefix read off the source-side OUTBOUND link, mirrors
//     created on the migration-dest), idempotent.
//   - TestMigrateApply_Topics_New — mode:new (no cluster link): plain topics
//     reproduced on the target with partition count + RF, idempotent, and a
//     report-only partition-count drift case.

// ---------------------------------------------------------------------------
// topic helpers on restClient
// ---------------------------------------------------------------------------

// uniqueTopicName makes topic names unique per test case (and per run), mirroring
// uniqueLinkName, so a re-run never collides with topics left by a prior run.
func uniqueTopicName(prefix string) string {
	return fmt.Sprintf("%s-%s-%d", prefix, runID, <-linkSeqCh)
}

// createTopic creates a plain topic on the cluster. Single-node brokers require
// replication_factor 1. 200/201 are success; 409 (already exists) is tolerated
// so re-runs and pre-seeding are idempotent.
func (c restClient) createTopic(t *testing.T, clusterID, name string, partitions int) {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"topic_name":         name,
		"partitions_count":   partitions,
		"replication_factor": 1,
	})
	require.NoError(t, err)
	req, err := http.NewRequest(http.MethodPost, c.base+"/kafka/v3/clusters/"+clusterID+"/topics", bytes.NewReader(body))
	require.NoError(t, err)
	if c.header != "" {
		req.Header.Set("Authorization", c.header)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	switch resp.StatusCode {
	case http.StatusOK, http.StatusCreated, http.StatusConflict:
		return
	default:
		t.Fatalf("create topic %q on %s: unexpected status %d", name, clusterID, resp.StatusCode)
	}
}

// deleteTopic best-effort removes a topic for cleanup. Failures are logged.
func (c restClient) deleteTopic(t *testing.T, clusterID, name string) {
	t.Helper()
	resp, err := c.do(http.MethodDelete, "/kafka/v3/clusters/"+clusterID+"/topics/"+name)
	if err != nil {
		t.Logf("delete topic %q on %s: %v", name, clusterID, err)
		return
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		t.Logf("delete topic %q on %s: unexpected status %d", name, clusterID, resp.StatusCode)
	}
}

// topicExists reports whether the topic is present on the cluster.
func (c restClient) topicExists(clusterID, name string) bool {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/topics/"+name)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

// topicPartitions returns the topic's partitions_count, or -1 if absent/unreadable.
func (c restClient) topicPartitions(clusterID, name string) int {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/topics/"+name)
	if err != nil {
		return -1
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return -1
	}
	var body struct {
		PartitionsCount int `json:"partitions_count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return -1
	}
	return body.PartitionsCount
}

// listMirrorTopics returns the mirror_topic_name list for the named link.
func (c restClient) listMirrorTopics(clusterID, link string) []string {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID+"/links/"+link+"/mirrors")
	if err != nil {
		return nil
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil
	}
	var body struct {
		Data []struct {
			MirrorTopicName string `json:"mirror_topic_name"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	out := make([]string, 0, len(body.Data))
	for _, m := range body.Data {
		out = append(out, m.MirrorTopicName)
	}
	return out
}

// requireMirrorsPresent polls the named link's mirrors until every wanted mirror
// name is present (mirror creation propagates asynchronously), failing on timeout.
func (c restClient) requireMirrorsPresent(t *testing.T, clusterID, link string, wanted []string) {
	t.Helper()
	deadline := time.Now().Add(60 * time.Second)
	var have map[string]struct{}
	for time.Now().Before(deadline) {
		have = map[string]struct{}{}
		for _, n := range c.listMirrorTopics(clusterID, link) {
			have[n] = struct{}{}
		}
		ok := true
		for _, w := range wanted {
			if _, present := have[w]; !present {
				ok = false
				break
			}
		}
		if ok {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("mirrors %v not all present on link %q (cluster %s); observed %v", wanted, link, clusterID, keys(have))
}

func keys(m map[string]struct{}) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}

// ---------------------------------------------------------------------------
// Test 1 — mirror, destination-initiated
// ---------------------------------------------------------------------------

// TestMigrateApply_Topics_MirrorDestination establishes a destination-initiated
// link with a prefix, then mirrors two selected source topics. It asserts the
// mirrorTopics section creates both with prefixed names, that re-apply is an
// idempotent no-op, and that the prefix is applied to the live mirror names.
func TestMigrateApply_Topics_MirrorDestination(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-dest")
	const prefix = "mt."

	// Source topics live on the source broker (read over the host PLAINTEXT
	// listener; mirrored onto the destination via the docker INTERNAL listener).
	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	t1 := uniqueTopicName("dsrc")
	t2 := uniqueTopicName("dsrc")
	srcPoller.createTopic(t, sourceClusterID, t1, 3)
	srcPoller.createTopic(t, sourceClusterID, t2, 2)
	defer srcPoller.deleteTopic(t, sourceClusterID, t1)
	defer srcPoller.deleteTopic(t, sourceClusterID, t2)

	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, kafkaAuth{authPlaintext, "localhost:19092"})
	linkCreds := filepath.Join(dir, "link-source-creds.yaml")
	writeKafkaCreds(t, linkCreds, kafkaAuth{authPlaintext, "source:29092"})
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", restDest)

	manifest := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\n" +
		"metadata:\n  name: mcl-" + link + "\n" +
		"spec:\n  source:\n    type: apache-kafka\n    bootstrapServers: [\"localhost:19092\"]\n    credentials: " + srcCreds + "\n" +
		"  target:\n    type: confluent-platform\n    credentials: " + targetCreds + "\n" +
		"    kafka:\n      restEndpoint: " + restDest.baseURL + "\n" +
		"  clusterLink:\n    name: " + link + "\n    mode: destination\n" +
		"    source:\n      bootstrapServers: [\"source:29092\"]\n      credentials: " + linkCreds + "\n" +
		"    prefix: \"" + prefix + "\"\n" +
		"  topics:\n    mode: mirror\n    include: [\"" + t1 + "\", \"" + t2 + "\"]\n"
	m := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(m, []byte(manifest), 0600))

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	// apply: link created + ACTIVE, then both mirrors created.
	out, err := runKCP(t, m)
	require.NoError(t, err, out)
	require.Contains(t, out, "== mirrorTopics", out)
	require.Contains(t, out, "mirrorTopics: 2 created", out)
	poller.requireLinkActive(t, destClusterID, link)

	wantMirrors := []string{prefix + t1, prefix + t2}
	poller.requireMirrorsPresent(t, destClusterID, link, wantMirrors)

	// re-apply: mirrors already present, nothing new created.
	out, err = runKCP(t, m)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 0 created, 2 already present", out)
}

// ---------------------------------------------------------------------------
// Test 2 — mirror, source-initiated
// ---------------------------------------------------------------------------

// TestMigrateApply_Topics_MirrorSourceInitiated drives mode:mirror in the
// source-initiated (push) topology. Per the baseline source case, the data
// SOURCE is the migration-source (dest-basic broker, destBasicClusterID), the
// OUTBOUND link carrying the prefix lives there, and mirrors are created on the
// migration-dest (source broker, sourceClusterID). It verifies the prefix is read
// off the OUTBOUND (source-side) link and applied to the mirror names created on
// the migration-dest, and that re-apply is idempotent.
func TestMigrateApply_Topics_MirrorSourceInitiated(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-source")
	const prefix = "mts."

	// migration-source = dest-basic broker (host PLAINTEXT listener localhost:29192,
	// clusterID destBasicClusterID). Topics to mirror are created there.
	migSrcPoller := newRestClient(t, restDestBasic)
	migSrcPoller.waitForClusterID(t)
	// migration-dest = source broker (restSource / sourceClusterID): mirrors land here.
	migDestPoller := newRestClient(t, restSource)
	migDestPoller.waitForClusterID(t)

	t1 := uniqueTopicName("ssrc")
	t2 := uniqueTopicName("ssrc")
	migSrcPoller.createTopic(t, destBasicClusterID, t1, 3)
	migSrcPoller.createTopic(t, destBasicClusterID, t2, 1)
	defer migSrcPoller.deleteTopic(t, destBasicClusterID, t1)
	defer migSrcPoller.deleteTopic(t, destBasicClusterID, t2)

	// D1: read migration-source cluster id over its host PLAINTEXT listener.
	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, kafkaAuth{authPlaintext, "localhost:29192"})
	// D4: migration-source REST creds (where the OUTBOUND link is created).
	srcRestCreds := writeRestCreds(t, dir, "source-rest-creds.yaml", restDestBasic)
	// D3: migration-dest (target) REST creds (where the INBOUND link is created).
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", restSource)
	// D5: source→destination connection creds (OUTBOUND link dials migration-dest).
	destConnCreds := filepath.Join(dir, "dest-conn-creds.yaml")
	writeKafkaCreds(t, destConnCreds, kafkaAuth{authPlaintext, "source:29092"})

	manifest := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\n" +
		"metadata:\n  name: mcl-" + link + "\n" +
		"spec:\n  source:\n    type: confluent-platform\n    bootstrapServers: [\"localhost:29192\"]\n    credentials: " + srcCreds + "\n" +
		"  target:\n    type: confluent-platform\n    credentials: " + targetCreds + "\n" +
		"    kafka:\n      restEndpoint: " + restSource.baseURL + "\n" +
		"  clusterLink:\n    name: " + link + "\n    mode: source\n" +
		"    sourceRest:\n      endpoint: " + restDestBasic.baseURL + "\n      credentials: " + srcRestCreds + "\n" +
		"    destination:\n      bootstrapServers: [\"source:29092\"]\n      credentials: " + destConnCreds + "\n" +
		"    prefix: \"" + prefix + "\"\n" +
		"  topics:\n    mode: mirror\n    include: [\"" + t1 + "\", \"" + t2 + "\"]\n"
	m := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(m, []byte(manifest), 0600))

	defer migDestPoller.deleteLink(t, sourceClusterID, link)  // INBOUND on migration-dest
	defer migSrcPoller.deleteLink(t, destBasicClusterID, link) // OUTBOUND on migration-source

	// apply: BOTH link sides created (2) + ACTIVE, then both mirrors created on
	// the migration-dest.
	out, err := runKCP(t, m)
	require.NoError(t, err, out)
	require.Contains(t, out, "clusterLink: 2 created", out)
	require.Contains(t, out, "== mirrorTopics", out)
	require.Contains(t, out, "mirrorTopics: 2 created", out)
	migDestPoller.requireLinkActive(t, sourceClusterID, link)
	migSrcPoller.requireLinkActive(t, destBasicClusterID, link)

	// Mirror names are prefixed with the OUTBOUND (source-side) link prefix and
	// created on the migration-dest (restSource / sourceClusterID).
	wantMirrors := []string{prefix + t1, prefix + t2}
	migDestPoller.requireMirrorsPresent(t, sourceClusterID, link, wantMirrors)

	// re-apply: idempotent for both the link pair and the mirrors.
	out, err = runKCP(t, m)
	require.NoError(t, err, out)
	require.Contains(t, out, "clusterLink: 0 created, 2 already present", out)
	require.Contains(t, out, "mirrorTopics: 0 created, 2 already present", out)
}

// ---------------------------------------------------------------------------
// Test 3 — new topics (no cluster link)
// ---------------------------------------------------------------------------

// TestMigrateApply_Topics_New drives mode:new with no cluster link: KCP reads the
// source topics and creates plain topics on the target reproducing partition
// count + RF. It asserts the topic is created with the source's partition count,
// that re-apply is idempotent, and that a pre-existing target topic with a
// different partition count is reported as drift (report-only, not altered).
func TestMigrateApply_Topics_New(t *testing.T) {
	dir := t.TempDir()

	// spec.source bootstrap localhost:19092 is the source broker (restSource).
	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	// Target is restDest (destClusterID).
	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	// Topic created cleanly on the target (source has 3 partitions).
	createTopic := uniqueTopicName("new")
	srcPoller.createTopic(t, sourceClusterID, createTopic, 3)
	defer srcPoller.deleteTopic(t, sourceClusterID, createTopic)
	defer tgtPoller.deleteTopic(t, destClusterID, createTopic)

	// Drift topic: source has 6 partitions, target pre-created with 2 — drift.
	driftTopic := uniqueTopicName("new")
	srcPoller.createTopic(t, sourceClusterID, driftTopic, 6)
	tgtPoller.createTopic(t, destClusterID, driftTopic, 2)
	defer srcPoller.deleteTopic(t, sourceClusterID, driftTopic)
	defer tgtPoller.deleteTopic(t, destClusterID, driftTopic)

	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, kafkaAuth{authPlaintext, "localhost:19092"})
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", restDest)

	manifest := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\n" +
		"metadata:\n  name: mcl-new-" + runID + "\n" +
		"spec:\n  source:\n    type: apache-kafka\n    bootstrapServers: [\"localhost:19092\"]\n    credentials: " + srcCreds + "\n" +
		"  target:\n    type: confluent-platform\n    credentials: " + targetCreds + "\n" +
		"    kafka:\n      restEndpoint: " + restDest.baseURL + "\n" +
		"  topics:\n    mode: new\n    include: [\"" + createTopic + "\", \"" + driftTopic + "\"]\n"
	m := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(m, []byte(manifest), 0600))

	// apply: createTopic is created (1), driftTopic is reported drift (1), none
	// already present on first apply.
	out, err := runKCP(t, m)
	require.NoError(t, err, out)
	require.Contains(t, out, "== newTopics", out)
	require.Contains(t, out, "newTopics: 1 created, 0 already present, 1 drift", out)

	// Created topic reproduces the source partition count on the target.
	require.True(t, tgtPoller.topicExists(destClusterID, createTopic), "created topic must exist on target")
	require.Equal(t, 3, tgtPoller.topicPartitions(destClusterID, createTopic), "created topic partition count reproduced from source")
	// Drift topic was left UNALTERED (report-only): still 2 partitions on target.
	require.Equal(t, 2, tgtPoller.topicPartitions(destClusterID, driftTopic), "drift topic must not be altered")

	// re-apply: createTopic now already present; driftTopic still drift.
	out, err = runKCP(t, m)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 0 created, 1 already present, 1 drift", out)
	require.Equal(t, 2, tgtPoller.topicPartitions(destClusterID, driftTopic), "drift topic still unaltered after re-apply")
}
