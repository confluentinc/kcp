//go:build integration

package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// This file is the SOURCE-INITIATED (push) mode:mirror integration coverage. The
// topology is the inverse of the destination matrix (see
// migrate_clusterlink_source_test.go for the broker/clusterID/port mapping):
//
//   - migration-SOURCE  = dest-basic broker (REST :28091, data PLAINTEXT
//     localhost:29192, clusterID destBasicClusterID). The OUTBOUND link carrying
//     the prefix lives here; topics to mirror are seeded here.
//   - migration-DEST    = source broker (REST :18090, data PLAINTEXT
//     localhost:19092, clusterID sourceClusterID). The INBOUND link lives here and
//     the prefixed mirror topics are created here.
//
// Source mode tests TOPOLOGY + one representative glob + end-to-end DATA FLOW,
// NOT the full selection matrix. Topic selection (globs, ?, exclude, internal
// exclusion, special chars, empty match) is mode-INDEPENDENT — both modes run the
// same SelectTopics + mirrorTopics reconciler, differing only in which side the
// prefix is read from and where the mirrors land. The destination matrix
// (migrate_topics_mirror_destination_test.go) exercises every selection edge case;
// repeating it here would add no coverage. What IS source-specific — that the
// prefix is read off the OUTBOUND (source-side) link and applied to mirrors
// created on the migration-dest, and that data flows in this topology — is what
// these cases prove.
//
// NOTE on dry-run: a from-scratch source-initiated `apply --dry-run` that creates
// BOTH the link pair AND the mirrors PLANS correctly and creates nothing. The
// mirrorTopics reconciler prefers the prefix off the LIVE OUTBOUND link but falls
// back to the manifest's clusterLink.prefix when the link does not yet exist (the
// T6.5 manifest-prefix fallback in mirrortopics.readLinkPrefix) — exactly the
// from-scratch dry-run case, in BOTH modes. TestMigrateApply_TopicsMirrorSource_DryRun
// proves this for source mode (the destination matrix proves it for destination mode).
//
// Tests run SERIALLY (no t.Parallel): they share the source/dest-basic brokers.

const (
	// migration-source (dest-basic) DATA listener — produce + KCP source read.
	migSrcDataBootstrap = "localhost:29192"
	// migration-source (dest-basic) INTERNAL docker listener — the OUTBOUND link
	// dials the migration-dest, but the migration-dest dials BACK here for the
	// INBOUND validation; KCP's source-read uses the host listener above.
	// migration-dest (source) DATA listener — consume the mirrored data.
	migDestDataBootstrap = "localhost:19092"
	// source broker INTERNAL docker listener — the OUTBOUND link's destination
	// connection dials this (the migration-dest's docker-network address).
	migDestDockerBootstrap = "source:29092"
)

// writeSourceMirrorManifest writes the cred files + a source-mode mirror manifest
// into dir and returns the manifest path. It mirrors the source-initiated topology
// of TestMigrateApply_Topics_MirrorSourceInitiated: the source read + OUTBOUND link
// live on the migration-source (dest-basic), the INBOUND link + mirrors land on the
// migration-dest (source).
func writeSourceMirrorManifest(t *testing.T, dir, link, prefix string, include []string) string {
	t.Helper()
	// D1: read the migration-source cluster id over its host PLAINTEXT listener.
	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, kafkaAuth{authPlaintext, migSrcDataBootstrap})
	// D4: migration-source REST creds (where the OUTBOUND link is created).
	srcRestCreds := writeRestCreds(t, dir, "source-rest-creds.yaml", restDestBasic)
	// D3: migration-dest (target) REST creds (where the INBOUND link is created).
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", restSource)
	// D5: source→destination connection creds (OUTBOUND link dials migration-dest).
	destConnCreds := filepath.Join(dir, "dest-conn-creds.yaml")
	writeKafkaCreds(t, destConnCreds, kafkaAuth{authPlaintext, migDestDockerBootstrap})

	var b strings.Builder
	b.WriteString("apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\n")
	b.WriteString("metadata:\n  name: mcl-" + link + "\n")
	b.WriteString("spec:\n  source:\n    type: confluent-platform\n    bootstrapServers: [\"" + migSrcDataBootstrap + "\"]\n    credentials: " + srcCreds + "\n")
	b.WriteString("  target:\n    type: confluent-platform\n    credentials: " + targetCreds + "\n")
	b.WriteString("    kafka:\n      restEndpoint: " + restSource.baseURL + "\n")
	b.WriteString("  clusterLink:\n    name: " + link + "\n    mode: source\n")
	b.WriteString("    sourceRest:\n      endpoint: " + restDestBasic.baseURL + "\n      credentials: " + srcRestCreds + "\n")
	b.WriteString("    destination:\n      bootstrapServers: [\"" + migDestDockerBootstrap + "\"]\n      credentials: " + destConnCreds + "\n")
	if prefix != "" {
		b.WriteString("    prefix: \"" + prefix + "\"\n")
	}
	b.WriteString("  topics:\n    mode: mirror\n")
	b.WriteString("    include: [" + quoteList(include) + "]\n")

	m := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(m, []byte(b.String()), 0600))
	return m
}

// sourceMirrorReporter accumulates one source-mode mirror case's evidence; all
// methods are cheap no-ops when reportEnabled is false. Unlike the destination
// mirrorReporter (which reads mirrors off restDest/destClusterID), this captures
// the mirrors on the migration-DEST (restSource/sourceClusterID), where source-mode
// mirrors land.
type sourceMirrorReporter struct {
	in   sectionInput
	link string
}

// newSourceMirrorReporter builds a reporter for a source-mode mirror case.
// srcTopics is the relevant set of migration-source topic names to show as evidence.
func newSourceMirrorReporter(name, checks, manifest, link string, srcTopics []string) *sourceMirrorReporter {
	r := &sourceMirrorReporter{link: link}
	if !reportEnabled {
		return r
	}
	r.in = sectionInput{
		seq:      nextReportSeq(),
		category: catMirror,
		mode:     "source",
		name:     name,
		checks:   checks,
		manifest: readFileForReport(manifest),
		results:  []resultBlock{topicListResult("source topics (on migration-source)", "", srcTopics)},
		pass:     true,
	}
	return r
}

func (r *sourceMirrorReporter) apply(out string) {
	if reportEnabled {
		r.in.addRun("Apply", applyCmd, out)
	}
}

func (r *sourceMirrorReporter) reapply(out string) {
	if reportEnabled {
		r.in.addRun("Idempotent re-apply", applyCmd, out)
	}
}

func (r *sourceMirrorReporter) dryRun(out string) {
	if reportEnabled {
		r.in.addRun("Dry run", applyDryRunCmd, out)
	}
}

func (r *sourceMirrorReporter) expected(note string) {
	if reportEnabled {
		r.in.results = append(r.in.results, topicListResult("expected", "", note))
	}
}

// commit captures the live mirrors on the migration-dest (best-effort) and
// finalises the section, marking it FAIL when the test failed.
func (r *sourceMirrorReporter) commit(t *testing.T, migDestPoller restClient) {
	if !reportEnabled {
		return
	}
	r.in.addReadBlock(mirrorsResult(migDestPoller, sourceClusterID, r.link))
	if t.Failed() {
		r.in.pass = false
	}
	collector.add(buildSection(r.in))
}

// ---------------------------------------------------------------------------
// Case 1 — source-initiated family glob (orders-*) + idempotent re-apply
// ---------------------------------------------------------------------------

// TestMigrateApply_TopicsMirrorSource_FamilyGlob drives a representative glob in
// the source-initiated topology. It seeds the catalog on the migration-source
// (dest-basic), applies a source-mode manifest with include:[orders-*], and proves:
//   - BOTH link sides are created (2) and reach ACTIVE;
//   - the four orders-* mirrors are created on the migration-dest (sourceClusterID)
//     with the prefix read off the OUTBOUND (source-side) link;
//   - re-apply is an idempotent no-op for both the link pair and the mirrors.
func TestMigrateApply_TopicsMirrorSource_FamilyGlob(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mts-glob")
	prefix := uniqueMirrorPrefix(link)

	// migration-source = dest-basic broker: seed the catalog + create the OUTBOUND link there.
	migSrcPoller := newRestClient(t, restDestBasic)
	migSrcPoller.waitForClusterID(t)
	seedTopicCatalog(t, migSrcPoller, destBasicClusterID)

	// migration-dest = source broker: mirrors land here (INBOUND link).
	migDestPoller := newRestClient(t, restSource)
	migDestPoller.waitForClusterID(t)

	m := writeSourceMirrorManifest(t, dir, link, prefix, []string{"orders-*"})

	defer migDestPoller.deleteLink(t, sourceClusterID, link)   // INBOUND on migration-dest
	defer migSrcPoller.deleteLink(t, destBasicClusterID, link) // OUTBOUND on migration-source

	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	want := prefixAll(prefix, srcTopics)
	rep := newSourceMirrorReporter(link,
		"source-initiated include:[orders-*]: KCP creates both link sides, reads the prefix off the OUTBOUND (source-side) link, and creates the four orders-* mirrors on the migration-dest as "+prefix+"orders-1..4; re-apply is an idempotent no-op for both the link pair and the mirrors.",
		m, link, srcTopics)
	rep.expected("orders-* → 4 prefixed mirrors on the migration-dest: " + strings.Join(want, ", "))
	defer rep.commit(t, migDestPoller)

	// apply: BOTH link sides created + ACTIVE, then the four mirrors created on the migration-dest.
	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "clusterLink: 2 created", out)
	require.Contains(t, out, "== mirrorTopics", out)
	require.Contains(t, out, "mirrorTopics: 4 created", out)
	migDestPoller.requireLinkActive(t, sourceClusterID, link)
	migSrcPoller.requireLinkActive(t, destBasicClusterID, link)

	// Mirror names are prefixed with the OUTBOUND (source-side) link prefix and
	// created on the migration-dest (restSource / sourceClusterID).
	migDestPoller.requireMirrorsPresent(t, sourceClusterID, link, want)
	require.Len(t, migDestPoller.listMirrorTopics(sourceClusterID, link), 4, "exactly 4 mirrors on the migration-dest")

	// re-apply: idempotent for both the link pair and the mirrors.
	out, err = runKCP(t, m)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "clusterLink: 0 created, 2 already present", out)
	require.Contains(t, out, "mirrorTopics: 0 created, 4 already present", out)
}

// ---------------------------------------------------------------------------
// Case 2 — source-initiated from-scratch dry-run (manifest-prefix fallback)
// ---------------------------------------------------------------------------

// TestMigrateApply_TopicsMirrorSource_DryRun proves a from-scratch source-initiated
// `apply --dry-run` PLANS the prefixed mirrors and creates NOTHING. The link pair
// does not exist yet, so the mirrorTopics reconciler cannot read cluster.link.prefix
// off the live OUTBOUND link during Plan — it falls back to the manifest's
// clusterLink.prefix (the T6.5 manifest-prefix fallback). Dry-run must therefore
// PLAN the four PREFIXED mirrors (proving the fallback drove planning) while
// creating neither link side nor any mirror. This is the source-mode counterpart
// to TestMigrateApply_TopicsMirror_DryRun (destination mode).
func TestMigrateApply_TopicsMirrorSource_DryRun(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mts-dry")
	prefix := uniqueMirrorPrefix(link)

	// migration-source = dest-basic broker: seed the catalog there (the OUTBOUND
	// link + the prefix would live here, but dry-run creates neither).
	migSrcPoller := newRestClient(t, restDestBasic)
	migSrcPoller.waitForClusterID(t)
	seedTopicCatalog(t, migSrcPoller, destBasicClusterID)

	// migration-dest = source broker: mirrors would land here (INBOUND link).
	migDestPoller := newRestClient(t, restSource)
	migDestPoller.waitForClusterID(t)

	m := writeSourceMirrorManifest(t, dir, link, prefix, []string{"orders-*"})

	// Defensive cleanup in case a bug created either link side during dry-run.
	defer migDestPoller.deleteLink(t, sourceClusterID, link)
	defer migSrcPoller.deleteLink(t, destBasicClusterID, link)

	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	rep := newSourceMirrorReporter(link,
		"from-scratch source-initiated --dry-run with include:[orders-*] on a NOT-YET-CREATED link pair PLANS the four prefixed mirrors (output shows 'Planned' + the prefixed mirror-topic plan lines, e.g. "+prefix+"orders-1) via the manifest-prefix fallback — but creates nothing: neither link side nor any mirror.",
		m, link, srcTopics)
	rep.expected("dry-run: plans 4 prefixed mirrors, creates 0; neither link side exists and no mirror afterward")
	defer rep.commit(t, migDestPoller)

	out, err := runKCP(t, m, "--dry-run")
	rep.dryRun(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "Planned", out)
	// The planned mirror names are PREFIXED — proof the manifest-prefix fallback
	// drove planning rather than a (non-existent) live OUTBOUND-link read.
	require.Contains(t, out, `+ mirror topic "`+prefix+`orders-1"`, out)

	// Dry-run created nothing: neither link side exists, and no mirror was created.
	require.Empty(t, migDestPoller.linkState(sourceClusterID, link), "dry-run must not create the INBOUND link")
	require.Empty(t, migSrcPoller.linkState(destBasicClusterID, link), "dry-run must not create the OUTBOUND link")
	require.Empty(t, migDestPoller.listMirrorTopics(sourceClusterID, link), "dry-run must not create any mirror")
}

// ---------------------------------------------------------------------------
// Case 3 — source-initiated failure / continue-on-error
// ---------------------------------------------------------------------------

// TestMigrateApply_TopicsMirrorSource_ContinueOnError forces ONE source-initiated
// mirror to fail while another succeeds, using the same clean trigger as the
// destination ContinueOnError case: pre-create a PLAIN topic on the migration-dest
// (source broker, sourceClusterID) occupying the second mirror's target name. The
// INBOUND link then rejects that mirror create (the topic already exists) while the
// other mirror is created cleanly. Apply reports `mirrorTopics: 1 created, …,
// 1 failed`, prints a ✖ line, exits non-zero — and the good mirror survives.
func TestMigrateApply_TopicsMirrorSource_ContinueOnError(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mts-fail")
	prefix := uniqueMirrorPrefix(link)

	// migration-source = dest-basic broker: seed the catalog + OUTBOUND link there.
	migSrcPoller := newRestClient(t, restDestBasic)
	migSrcPoller.waitForClusterID(t)
	seedTopicCatalog(t, migSrcPoller, destBasicClusterID)

	// migration-dest = source broker: mirrors land here (INBOUND link).
	migDestPoller := newRestClient(t, restSource)
	migDestPoller.waitForClusterID(t)

	m := writeSourceMirrorManifest(t, dir, link, prefix, []string{"orders-1", "orders-2"})

	defer migDestPoller.deleteLink(t, sourceClusterID, link)   // INBOUND on migration-dest
	defer migSrcPoller.deleteLink(t, destBasicClusterID, link) // OUTBOUND on migration-source

	// Pre-create a PLAIN topic on the migration-dest occupying the orders-2 mirror's
	// target name so that mirror create fails while orders-1 mirrors cleanly.
	blocker := prefix + "orders-2"
	migDestPoller.createTopic(t, sourceClusterID, blocker, 1)
	defer migDestPoller.deleteTopic(t, sourceClusterID, blocker)

	srcTopics := []string{"orders-1", "orders-2"}
	rep := newSourceMirrorReporter(link,
		"source-initiated include:[orders-1, orders-2] where the migration-dest target name for orders-2 is pre-occupied by a plain topic: orders-1 mirrors successfully while the orders-2 mirror fails — apply reports 1 created + 1 failed, prints a ✖ line, and kcp exits non-zero; the good mirror survives.",
		m, link, srcTopics)
	rep.expected("orders-1 mirror created on migration-dest; orders-2 mirror fails (name pre-occupied); output shows 'mirrorTopics: 1 created, …, 1 failed' and '✖'; exit non-zero")
	defer rep.commit(t, migDestPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.Error(t, err, "kcp must exit non-zero when a source-mode mirror fails:\n%s", out)
	// Scope to the mirrorTopics outcome line: a bare "1 failed" would also match the
	// clusterLink line. The full rendered line proves the mirror create failed.
	require.Contains(t, out, "mirrorTopics: 1 created, 0 already present, 0 drift, 1 failed", out)
	require.Contains(t, out, "✖", out)

	// Despite the failure, the good mirror was created on the migration-dest.
	migDestPoller.requireLinkActive(t, sourceClusterID, link)
	migSrcPoller.requireLinkActive(t, destBasicClusterID, link)
	migDestPoller.requireMirrorsPresent(t, sourceClusterID, link, []string{prefix + "orders-1"})
}

// ---------------------------------------------------------------------------
// Case 4 — source-initiated DATA FLOW: produce → mirror → consume
// ---------------------------------------------------------------------------

// TestMigrateApply_TopicsMirrorSource_DataFlow proves end-to-end replication in
// the source-initiated topology: produce 10 records to a dedicated single-partition
// topic on the migration-source (dest-basic data listener localhost:29192), mirror
// it, then consume the prefixed mirror from the migration-dest (source data listener
// localhost:19092) — asserting all 10 record VALUES (not just the count) arrive.
func TestMigrateApply_TopicsMirrorSource_DataFlow(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mts-flow")
	prefix := uniqueMirrorPrefix(link)
	const nRecords = 10

	// migration-source = dest-basic broker: create + populate the topic + OUTBOUND link.
	migSrcPoller := newRestClient(t, restDestBasic)
	migSrcPoller.waitForClusterID(t)

	// Dedicated single-partition source topic so consumeRecords (partition 0) sees
	// every record. Created + populated BEFORE the mirror is established.
	dataTopic := uniqueTopicName("sdataflow")
	migSrcPoller.createTopic(t, destBasicClusterID, dataTopic, 1)
	defer migSrcPoller.deleteTopic(t, destBasicClusterID, dataTopic)

	produceRecords(t, migSrcDataBootstrap, dataTopic, nRecords)

	// migration-dest = source broker: the mirror lands here (INBOUND link).
	migDestPoller := newRestClient(t, restSource)
	migDestPoller.waitForClusterID(t)

	m := writeSourceMirrorManifest(t, dir, link, prefix, []string{dataTopic})

	defer migDestPoller.deleteLink(t, sourceClusterID, link)   // INBOUND on migration-dest
	defer migSrcPoller.deleteLink(t, destBasicClusterID, link) // OUTBOUND on migration-source

	mirror := prefix + dataTopic
	rep := newSourceMirrorReporter(link,
		fmt.Sprintf("source-initiated data flow: produce %d records to a dedicated single-partition topic on the migration-source (dest-basic, %s), mirror it, then consume the prefixed mirror %s from the migration-dest (source, %s) — all %d record values replicate end-to-end.", nRecords, migSrcDataBootstrap, mirror, migDestDataBootstrap, nRecords),
		m, link, []string{dataTopic})
	rep.expected(fmt.Sprintf("%d records produced to %s on the migration-source replicate to mirror %s on the migration-dest", nRecords, dataTopic, mirror))
	defer rep.commit(t, migDestPoller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "clusterLink: 2 created", out)
	require.Contains(t, out, "mirrorTopics: 1 created", out)
	migDestPoller.requireLinkActive(t, sourceClusterID, link)
	migSrcPoller.requireLinkActive(t, destBasicClusterID, link)
	migDestPoller.requireMirrorsPresent(t, sourceClusterID, link, []string{mirror})

	// Mirroring is async: give it a generous window to materialise + replicate.
	got := consumeRecords(t, migDestDataBootstrap, mirror, nRecords, 90*time.Second)
	require.Len(t, got, nRecords, "all %d records must replicate to the migration-dest mirror %q", nRecords, mirror)

	want := map[string]struct{}{}
	for i := 0; i < nRecords; i++ {
		want[fmt.Sprintf("msg-%d", i)] = struct{}{}
	}
	for _, v := range got {
		delete(want, string(v))
	}
	require.Empty(t, want, "every produced record value must arrive on the migration-dest mirror; missing: %v", want)
}
