//go:build integration

package migrate

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file holds the topic REST helpers shared by the spec.topics integration
// suites (mirror + new mode). The mode:mirror matrices live in
// migrate_topics_mirror_{destination,source}_test.go; the mode:new matrix lives
// in migrate_topics_new_test.go.

// ---------------------------------------------------------------------------
// topic helpers on restClient
// ---------------------------------------------------------------------------

// uniqueTopicName makes topic names unique per test case (and per run), mirroring
// uniqueLinkName, so a re-run never collides with topics left by a prior run.
func uniqueTopicName(prefix string) string {
	return fmt.Sprintf("%s-%s-%d", prefix, runID, <-linkSeqCh)
}

// createTopic creates a plain topic on the cluster. Single-node brokers require
// replication_factor 1. 200/201 are success; an "already exists" response is
// tolerated so re-runs and pre-seeding are idempotent.
//
// The cp-server embedded REST API does NOT return 409 for an existing topic — it
// returns 400 with error_code 40002 and a "Topic '<name>' already exists."
// message. We treat that specific 400 as success; any other 400 (e.g. a topic
// name collision, "collides with existing topic") is a real failure.
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
	case http.StatusBadRequest:
		// cp-server returns 400/40002 for an already-existing topic; tolerate
		// that (idempotent), but fail on any other 400.
		var b struct {
			Message string `json:"message"`
		}
		_ = json.NewDecoder(resp.Body).Decode(&b)
		if strings.Contains(b.Message, "already exists") {
			return
		}
		t.Fatalf("create topic %q on %s: status 400: %s", name, clusterID, b.Message)
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
	// 404 = already absent, which is exactly the cleanup's goal — not an error.
	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusNotFound {
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

// setTopicConfig sets a single topic config key via the cp-server REST v3
// alter-configs endpoint (PUT .../topics/{topic}/configs/{key} with {"value":…}).
// Used to give a source topic a non-default config so the new-mode reconciler has
// something to reproduce. Fatal on a non-2xx response.
func (c restClient) setTopicConfig(t *testing.T, clusterID, topic, key, value string) {
	t.Helper()
	body, err := json.Marshal(map[string]any{"value": value})
	require.NoError(t, err)
	path := "/kafka/v3/clusters/" + clusterID + "/topics/" + url.PathEscape(topic) + "/configs/" + url.PathEscape(key)
	req, err := http.NewRequest(http.MethodPut, c.base+path, bytes.NewReader(body))
	require.NoError(t, err)
	if c.header != "" {
		req.Header.Set("Authorization", c.header)
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.client.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("set config %s=%s on topic %q (%s): status %d: %s", key, value, topic, clusterID, resp.StatusCode, string(b))
	}
}

// topicConfig reads a single topic config value via GET
// .../topics/{topic}/configs/{key}, returning ("", false) if absent/unreadable.
func (c restClient) topicConfig(clusterID, topic, key string) (string, bool) {
	path := "/kafka/v3/clusters/" + clusterID + "/topics/" + url.PathEscape(topic) + "/configs/" + url.PathEscape(key)
	resp, err := c.do(http.MethodGet, path)
	if err != nil {
		return "", false
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return "", false
	}
	var body struct {
		Value     *string `json:"value"`
		IsDefault bool    `json:"is_default"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil || body.Value == nil {
		return "", false
	}
	return *body.Value, true
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

// mirrorStatuses returns a map of mirror_topic_name → mirror_status for the named
// link (e.g. "ACTIVE", "PENDING_MIRROR", "FAILED"). Empty on any error.
func (c restClient) mirrorStatuses(clusterID, link string) map[string]string {
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
			MirrorStatus    string `json:"mirror_status"`
		} `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		return nil
	}
	out := make(map[string]string, len(body.Data))
	for _, m := range body.Data {
		out[m.MirrorTopicName] = m.MirrorStatus
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

// The destination- and source-initiated mode:mirror integration tests live in
// migrate_topics_mirror_{destination,source}_test.go (full selection matrix +
// idempotency, incremental, continue-on-error, dry-run, status, and data-flow
// cases). The earlier TestMigrateApply_Topics_Mirror{Destination,SourceInitiated}
// tests that lived here were strict subsets of those matrices and have been
// removed; only the shared helpers above remain. mode:new (no cluster link)
// coverage lives in migrate_topics_new_test.go.
