//go:build integration

package migrate

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the mode:new (plain, non-mirror topic creation) integration
// matrix. Each case seeds the standard topic catalog on the SOURCE broker
// (idempotent, shared fixture, never deleted), builds a new-mode manifest with NO
// cluster link, and asserts that KCP creates plain topics on the TARGET
// reproducing the source partition count and explicitly-set (non-default) configs.
// The source replication factor is passed through on create, but RF is not
// separately verified here: these are single-node brokers (everything is RF=1), so
// RF reproduction is not observable in this environment. Cases cover glob families,
// the ? wildcard, multi-include, exclude,
// internal-topic exclusion, empty match, special-char names, partition
// inheritance, non-default config pass-through, partition-count drift
// (report-only), dry-run, and a failure/continue-on-error note.
//
// Tests run SERIALLY (no t.Parallel). The new-mode TARGET is the dest broker
// (restDest / destClusterID) — the SAME broker the mirror matrix mirrors onto.
// Because new-mode reproduces the fixed catalog names (orders-1 etc.) directly
// onto the target, two cases creating the same name would collide. Each case
// therefore DELETES every target topic it created (defer), and serial execution
// guarantees a prior case's target topics are gone before the next runs. The
// SOURCE catalog is a shared fixture and is never deleted.

// ---------------------------------------------------------------------------
// shared new-mode manifest + report wiring
// ---------------------------------------------------------------------------

// newManifestOpts parameterises a no-cluster-link mode:new manifest.
type newManifestOpts struct {
	name    string // metadata.name suffix (unique per case)
	include []string
	exclude []string
}

// writeNewManifest writes the cred files + a mode:new manifest (apache-kafka
// source on the host PLAINTEXT listener, confluent-platform target on the no-auth
// dest REST, NO clusterLink) into dir and returns the manifest path.
func writeNewManifest(t *testing.T, dir string, o newManifestOpts) string {
	t.Helper()
	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, kafkaAuth{authPlaintext, srcDataBootstrap})
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", restDest)

	var b strings.Builder
	b.WriteString("apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\n")
	b.WriteString("metadata:\n  name: mcl-new-" + o.name + "\n")
	b.WriteString("spec:\n  source:\n    type: apache-kafka\n    bootstrapServers: [\"" + srcDataBootstrap + "\"]\n    credentials: " + srcCreds + "\n")
	b.WriteString("  target:\n    type: confluent-platform\n    credentials: " + targetCreds + "\n")
	b.WriteString("    kafka:\n      restEndpoint: " + restDest.baseURL + "\n")
	b.WriteString("  topics:\n    mode: new\n")
	b.WriteString("    include: [" + quoteList(o.include) + "]\n")
	if len(o.exclude) > 0 {
		b.WriteString("    exclude: [" + quoteList(o.exclude) + "]\n")
	}

	m := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(m, []byte(b.String()), 0600))
	return m
}

// newReporter accumulates one new-mode test case's evidence; all methods are
// cheap no-ops when reportEnabled is false. It captures source topics, the
// manifest, the commands, apply/reapply/dry-run output, and a live read of the
// created topics (name + partition count) on the target.
type newReporter struct {
	in        sectionInput
	targetIDs []string // target topic names to read for the "topics on target" block
}

// newNewReporter builds a reporter for a mode:new case. srcTopics is the relevant
// set of source topic names shown as evidence; targetNames is the set of target
// topic names whose live partition counts are captured at commit.
func newNewReporter(name, checks, manifest string, srcTopics, targetNames []string) *newReporter {
	r := &newReporter{targetIDs: targetNames}
	if !reportEnabled {
		return r
	}
	// commands are assembled at commit() to match the output actually captured.
	r.in = sectionInput{
		seq:      nextReportSeq(),
		category: catNew,
		mode:     "new",
		name:     name,
		checks:   checks,
		manifest: readFileForReport(manifest),
		results:  []resultBlock{topicListResult("source topics (catalog)", "", srcTopics)},
		pass:     true,
	}
	return r
}

func (r *newReporter) apply(out string) {
	if reportEnabled {
		r.in.addRun("Apply", applyCmd, out)
	}
}

func (r *newReporter) reapply(out string) {
	if reportEnabled {
		r.in.addRun("Idempotent re-apply", applyCmd, out)
	}
}

func (r *newReporter) dryRun(out string) {
	if reportEnabled {
		r.in.addRun("Dry run", applyDryRunCmd, out)
	}
}

// expected appends a one-line "expected" note to the report.
func (r *newReporter) expected(note string) {
	if reportEnabled {
		r.in.results = append(r.in.results, topicListResult("expected", "", note))
	}
}

// note appends an arbitrary structured note (used to record config pass-through
// evidence).
func (r *newReporter) note(label string, v any) {
	if reportEnabled {
		r.in.results = append(r.in.results, topicListResult(label, "", v))
	}
}

// cleanTargetTopics best-effort deletes the named topics on the target before a
// case runs, so an exact "N created" assertion starts from a known-absent state
// regardless of any topic a prior case left on the shared dest broker (e.g. the
// unprefixed mirror topics from the mirror NoPrefix case). After a delete the
// broker needs a moment to drop the topic from metadata; poll briefly for
// absence.
func cleanTargetTopics(t *testing.T, c restClient, clusterID string, names []string) {
	t.Helper()
	for _, n := range names {
		if !c.topicExists(clusterID, n) {
			continue
		}
		c.deleteTopic(t, clusterID, n)
	}
	deadline := time.Now().Add(20 * time.Second)
	for time.Now().Before(deadline) {
		anyPresent := false
		for _, n := range names {
			if c.topicExists(clusterID, n) {
				anyPresent = true
				break
			}
		}
		if !anyPresent {
			return
		}
		time.Sleep(time.Second)
	}
}

// commit captures the live target topics (name + partition count) and finalises
// the section, marking it FAIL when the test failed.
func (r *newReporter) commit(t *testing.T, poller restClient) {
	if !reportEnabled {
		return
	}
	r.in.addReadBlock(targetTopicsResult(poller, destClusterID, r.targetIDs))
	if t.Failed() {
		r.in.pass = false
	}
	collector.add(buildSection(r.in))
}

// ---------------------------------------------------------------------------
// Case 1 — family glob (orders-*) + idempotent re-apply + partition inheritance
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_FamilyGlob(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{name: "glob-" + runID, include: []string{"orders-*"}})

	// orders-1=3, orders-2=1, orders-3=2, orders-4=1 (see topicCatalog()).
	created := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	cleanTargetTopics(t, tgtPoller, destClusterID, created)
	for _, n := range created {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("FamilyGlob", "include:[orders-*] creates the four orders-* source topics as plain topics on the target, each reproducing the source partition count (orders-1=3, orders-2=1, orders-3=2, orders-4=1); re-apply is an idempotent no-op (4 already present).", m, created, created)
	rep.expected("orders-* → 4 created on target with partitions 3,1,2,1; re-apply: 0 created, 4 already present")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "== newTopics", out)
	require.Contains(t, out, "newTopics: 4 created, 0 already present, 0 drift, 0 failed", out)

	// Each target topic reproduces the source partition count.
	wantPartitions := map[string]int{"orders-1": 3, "orders-2": 1, "orders-3": 2, "orders-4": 1}
	for n, want := range wantPartitions {
		require.True(t, tgtPoller.topicExists(destClusterID, n), "target topic %q must exist", n)
		require.Equal(t, want, tgtPoller.topicPartitions(destClusterID, n), "target topic %q partition count reproduced from source", n)
	}

	// re-apply: all four now already present, none created.
	out, err = runKCP(t, m)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 0 created, 4 already present, 0 drift, 0 failed", out)
}

// ---------------------------------------------------------------------------
// Case 2 — single-char ? wildcard
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_QuestionWildcard(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{name: "q-" + runID, include: []string{"orders-?"}})

	created := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	cleanTargetTopics(t, tgtPoller, destClusterID, created)
	for _, n := range created {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("QuestionWildcard", "include:[orders-?] (single-char wildcard) creates the four single-digit orders topics on the target.", m, created, created)
	rep.expected("orders-? → 4 created (single-char match)")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 4 created, 0 already present, 0 drift, 0 failed", out)
	for _, n := range created {
		require.True(t, tgtPoller.topicExists(destClusterID, n), "target topic %q must exist", n)
	}
}

// ---------------------------------------------------------------------------
// Case 3 — multi-include (products-* + transactions-*) + partition inheritance
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_MultiInclude(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{name: "multi-" + runID, include: []string{"products-*", "transactions-*"}})

	created := []string{
		"products-1", "products-2", "products-3", "products-4",
		"transactions-1", "transactions-2", "transactions-3", "transactions-4",
	}
	cleanTargetTopics(t, tgtPoller, destClusterID, created)
	for _, n := range created {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("MultiInclude", "include:[products-*, transactions-*] creates both whole families (8 topics) on the target; transactions-1 (4 partitions on the source) reproduces 4 partitions on the target — an explicit partition-inheritance assertion.", m, created, created)
	rep.expected("products-* (4) + transactions-* (4) → 8 created; transactions-1 has 4 partitions on target")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 8 created, 0 already present, 0 drift, 0 failed", out)
	for _, n := range created {
		require.True(t, tgtPoller.topicExists(destClusterID, n), "target topic %q must exist", n)
	}
	// Explicit partition inheritance: transactions-1 has 4 partitions on the source.
	require.Equal(t, 4, srcPoller.topicPartitions(sourceClusterID, "transactions-1"), "catalog transactions-1 must have 4 partitions on source")
	require.Equal(t, 4, tgtPoller.topicPartitions(destClusterID, "transactions-1"), "transactions-1 must reproduce 4 partitions on target")
}

// ---------------------------------------------------------------------------
// Case 4 — exclude
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_Exclude(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{
		name:    "excl-" + runID,
		include: []string{"*"},
		exclude: []string{"transactions-*", "latency_ms"},
	})

	// Clean up every catalog user topic that could be created on the target.
	for _, n := range catalogUserTopics() {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("Exclude", "include:[*] exclude:[transactions-*, latency_ms]: orders/products families ARE created on the target, no transactions-* topic, no latency_ms (underscore name), but metrics.cpu (dotted, distinct) IS created.", m, catalogUserTopics(), catalogUserTopics())
	rep.expected("orders/products created; transactions-* and latency_ms absent; metrics.cpu present")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "== newTopics", out)

	mustPresent := []string{
		"orders-1", "orders-2", "orders-3", "orders-4",
		"products-1", "products-2", "products-3", "products-4",
		"metrics.cpu",
	}
	for _, n := range mustPresent {
		require.True(t, tgtPoller.topicExists(destClusterID, n), "included topic %q must be created on target", n)
	}
	for _, n := range []string{"transactions-1", "transactions-2", "transactions-3", "transactions-4"} {
		require.False(t, tgtPoller.topicExists(destClusterID, n), "excluded transactions topic %q must not be created on target", n)
	}
	require.False(t, tgtPoller.topicExists(destClusterID, "latency_ms"), "excluded latency_ms (underscore name) must not be created on target")
}

// ---------------------------------------------------------------------------
// Case 5 — internal-topic exclusion (include:[*], no exclude)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_InternalExclusion(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{name: "all-" + runID, include: []string{"*"}})

	for _, n := range catalogUserTopics() {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("InternalExclusion", "include:[*] with no exclude creates every user catalog topic on the target and ZERO internal (leading-underscore) topic — __consumer_offsets, _confluent*, _schemas are never selected, so KCP never tries to create them on the target.", m, catalogUserTopics(), catalogUserTopics())
	rep.expected("all catalogUserTopics() created on target; no leading-underscore internal topic created by KCP")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "== newTopics", out)

	// Every user catalog topic was created on the target.
	for _, n := range catalogUserTopics() {
		require.True(t, tgtPoller.topicExists(destClusterID, n), "user catalog topic %q must be created on target", n)
	}
	// KCP must never select (and so never report creating) a broker-internal,
	// leading-underscore topic. The apply output enumerates each created topic as
	// `+ topic "<name>"`; assert no such line names a leading-underscore topic.
	for _, line := range strings.Split(out, "\n") {
		idx := strings.Index(line, `topic "`)
		if idx < 0 {
			continue
		}
		name := strings.TrimPrefix(line[idx:], `topic "`)
		if end := strings.Index(name, `"`); end >= 0 {
			name = name[:end]
		}
		require.False(t, strings.HasPrefix(name, "_"),
			"KCP must never create an internal (leading-underscore) topic, but output referenced %q", name)
	}
}

// ---------------------------------------------------------------------------
// Case 6 — empty match
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_EmptyMatch(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{name: "empty-" + runID, include: []string{"nope-*"}})

	rep := newNewReporter("EmptyMatch", "include:[nope-*] matches no source topic: newTopics reports 0 created, no target topic is created, and apply exits cleanly.", m, catalogUserTopics(), []string{"nope-1"})
	rep.expected("nope-* → 0 created, no error, no target topic")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 0 created, 0 already present, 0 drift, 0 failed", out)
	require.False(t, tgtPoller.topicExists(destClusterID, "nope-1"), "no target topic created on empty match")
}

// ---------------------------------------------------------------------------
// Case 7 — special-char names (dot, underscore, mixed)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_SpecialCharNames(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{
		name:    "special-" + runID,
		include: []string{"orders.created.*", "inventory_*", "events-2026.*"},
	})

	created := []string{"orders.created.v2", "inventory_snapshot", "events-2026.q1"}
	cleanTargetTopics(t, tgtPoller, destClusterID, created)
	for _, n := range created {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("SpecialCharNames", "include globs over dotted/underscored/mixed source names create plain target topics orders.created.v2, inventory_snapshot, events-2026.q1 — exercises REST URL-escaping of special characters on topic create.", m, created, created)
	rep.expected("orders.created.* / inventory_* / events-2026.* → 3 special-char topics created on target")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 3 created, 0 already present, 0 drift, 0 failed", out)
	for _, n := range created {
		require.True(t, tgtPoller.topicExists(destClusterID, n), "special-char target topic %q must exist", n)
	}
	// Partition inheritance on a special-char name: orders.created.v2 = 3 on source.
	require.Equal(t, 3, tgtPoller.topicPartitions(destClusterID, "orders.created.v2"), "orders.created.v2 must reproduce 3 partitions on target")
}

// ---------------------------------------------------------------------------
// Case 8 — non-default config pass-through
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_ConfigPassThrough(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	// Dedicated source topic with TWO non-default, settable configs. The source
	// reader returns only explicitly-set (non-default) configs, so both
	// retention.ms=604800000 (7d) and max.message.bytes=2097152 (2MiB) must be
	// reproduced on the target topic.
	const (
		retention = "604800000"
		maxBytes  = "2097152"
	)
	srcTopic := uniqueTopicName("cfg")
	srcPoller.createTopic(t, sourceClusterID, srcTopic, 2)
	srcPoller.setTopicConfig(t, sourceClusterID, srcTopic, "retention.ms", retention)
	srcPoller.setTopicConfig(t, sourceClusterID, srcTopic, "max.message.bytes", maxBytes)
	defer srcPoller.deleteTopic(t, sourceClusterID, srcTopic)
	defer tgtPoller.deleteTopic(t, destClusterID, srcTopic)

	// Verify the source carries both non-default values before the apply.
	srcVal, ok := srcPoller.topicConfig(sourceClusterID, srcTopic, "retention.ms")
	require.True(t, ok, "source topic must carry retention.ms")
	require.Equal(t, retention, srcVal, "source retention.ms must be the non-default value")
	srcMaxBytes, ok := srcPoller.topicConfig(sourceClusterID, srcTopic, "max.message.bytes")
	require.True(t, ok, "source topic must carry max.message.bytes")
	require.Equal(t, maxBytes, srcMaxBytes, "source max.message.bytes must be the non-default value")

	m := writeNewManifest(t, dir, newManifestOpts{name: "cfg-" + runID, include: []string{srcTopic}})

	rep := newNewReporter("ConfigPassThrough", "a source topic with TWO non-default configs (retention.ms=604800000, max.message.bytes=2097152) is reproduced on the target: the new-mode reconciler forwards all explicitly-set source configs on create, so the target topic carries BOTH values.", m, []string{srcTopic}, []string{srcTopic})
	rep.expected("target topic carries retention.ms=604800000 AND max.message.bytes=2097152 (both non-default configs reproduced from source)")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 1 created, 0 already present, 0 drift, 0 failed", out)
	require.True(t, tgtPoller.topicExists(destClusterID, srcTopic), "target topic must exist")

	tgtVal, ok := tgtPoller.topicConfig(destClusterID, srcTopic, "retention.ms")
	require.True(t, ok, "target topic must carry retention.ms")
	require.Equal(t, retention, tgtVal, "target retention.ms must match the source non-default value")
	tgtMaxBytes, ok := tgtPoller.topicConfig(destClusterID, srcTopic, "max.message.bytes")
	require.True(t, ok, "target topic must carry max.message.bytes")
	require.Equal(t, maxBytes, tgtMaxBytes, "target max.message.bytes must match the source non-default value")
	rep.note("config pass-through (live)", map[string]string{
		"source.retention.ms":      srcVal,
		"target.retention.ms":      tgtVal,
		"source.max.message.bytes": srcMaxBytes,
		"target.max.message.bytes": tgtMaxBytes,
	})

	// Target-rejection path (per-topic Failed + aggregated error): NOT exercised
	// live. On this single-node cp-server there is no config that is cleanly
	// settable on the source yet rejected by the target on create — the two brokers
	// are identical embedded cp-server instances, and managed/tier configs cannot
	// even be set on the source topic in the first place. Rather than fake an
	// assertion, the create-failure / continue-on-error path is covered
	// deterministically by the unit test newtopics/reconciler_test.go
	// TestApply_ContinueOnError.
}

// ---------------------------------------------------------------------------
// Case 9 — partition-count drift (report-only, unaltered)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_Drift(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)
	require.Equal(t, 3, srcPoller.topicPartitions(sourceClusterID, "orders-1"), "catalog orders-1 must have 3 partitions on source")

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	// Pre-create the target orders-1 with a DIFFERENT partition count (2 vs source
	// 3). Clean any leftover orders-1 first so the pre-created topic definitively
	// has 2 partitions (createTopic tolerates already-exists, so a stale orders-1
	// would otherwise keep its old partition count).
	cleanTargetTopics(t, tgtPoller, destClusterID, []string{"orders-1"})
	tgtPoller.createTopic(t, destClusterID, "orders-1", 2)
	defer tgtPoller.deleteTopic(t, destClusterID, "orders-1")

	m := writeNewManifest(t, dir, newManifestOpts{name: "drift-" + runID, include: []string{"orders-1"}})

	rep := newNewReporter("Drift", "the target orders-1 pre-exists with 2 partitions while the source has 3: new-mode reports 1 drift (report-only), leaves the target at 2 partitions (never altered), and re-apply is stable (still 1 drift, still 2 partitions).", m, []string{"orders-1"}, []string{"orders-1"})
	rep.expected("orders-1 → 1 drift; target stays at 2 partitions after apply and re-apply (unaltered)")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 0 created, 0 already present, 1 drift, 0 failed", out)
	require.Equal(t, 2, tgtPoller.topicPartitions(destClusterID, "orders-1"), "drift topic must not be altered (still 2 partitions)")

	out, err = runKCP(t, m)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "newTopics: 0 created, 0 already present, 1 drift, 0 failed", out)
	require.Equal(t, 2, tgtPoller.topicPartitions(destClusterID, "orders-1"), "drift topic still unaltered after re-apply")
}

// ---------------------------------------------------------------------------
// Case 10 — dry-run (plans, creates nothing)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsNew_DryRun(t *testing.T) {
	dir := t.TempDir()

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	tgtPoller := newRestClient(t, restDest)
	tgtPoller.waitForClusterID(t)

	m := writeNewManifest(t, dir, newManifestOpts{name: "dry-" + runID, include: []string{"orders-*"}})

	created := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	// Start from a known-absent state so the "nothing created" assertion is about
	// THIS dry-run, not a topic a prior case left on the shared dest. Also defer a
	// cleanup in case a bug created topics during dry-run.
	cleanTargetTopics(t, tgtPoller, destClusterID, created)
	for _, n := range created {
		defer tgtPoller.deleteTopic(t, destClusterID, n)
	}

	rep := newNewReporter("DryRun", "from-scratch --dry-run with include:[orders-*] PLANS the four orders topics (output shows 'Planned' and a '+ topic \"orders-1\"' plan line) while creating NOTHING — no orders-* topic exists on the target afterward (new-mode has no cluster link, so no prefix-read issue).", m, created, created)
	rep.expected("dry-run: plans 4 topics ('+ topic \"orders-1\"'), creates 0; no orders-* on target afterward")
	defer rep.commit(t, tgtPoller)

	out, err := runKCP(t, m, "--dry-run")
	rep.dryRun(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "Planned", out)
	require.Contains(t, out, `+ topic "orders-1"`, out)

	// Dry-run created nothing on the target.
	for _, n := range created {
		require.False(t, tgtPoller.topicExists(destClusterID, n), "dry-run must not create target topic %q", n)
	}
}

// ---------------------------------------------------------------------------
// Case 11 — failure / continue-on-error
// ---------------------------------------------------------------------------
//
// A clean live create failure for ONE topic while ANOTHER succeeds is not
// achievable against this single-node cp-server broker:
//
//   - An already-present target topic is reported Present (not a create failure),
//     so it cannot exercise the Failed path.
//   - An over-large partition request is rejected at PLAN time only for some
//     brokers; on this embedded REST it either succeeds or fails the whole apply
//     non-deterministically, which would be a flaky test.
//   - A name that collides with a different-cased/compacted topic depends on
//     broker-specific InvalidTopic behaviour and is not reliably one-of-N.
//
// The Apply continue-on-error semantics (Created N, Failed M, returns error) are
// deterministically covered by the unit test
// newtopics/reconciler_test.go TestApply_ContinueOnError. We DEFER the live
// failure case rather than force a flaky/invalid setup. (The mirror matrix DOES
// have a live failure case because a pre-occupied mirror target name is a clean,
// deterministic one-of-N failure there; no equivalent exists for plain creates.)

// TestMigrateApply_TopicsNew_FailureDeferred documents the deferral so the report
// (and a reader of this suite) records the decision; it performs no live apply.
func TestMigrateApply_TopicsNew_FailureDeferred(t *testing.T) {
	if !reportEnabled {
		t.Skip("documentation-only case; covered by unit test TestApply_ContinueOnError")
	}
	in := sectionInput{
		seq:      nextReportSeq(),
		category: catNew,
		mode:     "new",
		name:     "FailureDeferred",
		checks:   "continue-on-error (Created N, Failed M, non-zero exit) has no clean live trigger on the single-node cp-server broker for plain topic creates; it is deterministically covered by the unit test newtopics/reconciler_test.go TestApply_ContinueOnError and DEFERRED here rather than forced into a flaky setup.",
		results: []resultBlock{topicListResult("deferral reason", "",
			"no deterministic one-of-N create failure exists for plain topics on the single-node broker; unit-covered instead")},
		pass:     true,
		deferred: true,
	}
	in.addRun("Deferred — no live apply", "# covered by unit test newtopics/reconciler_test.go TestApply_ContinueOnError", "")
	collector.add(buildSection(in))
}
