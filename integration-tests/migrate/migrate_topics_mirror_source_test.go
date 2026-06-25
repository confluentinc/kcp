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
// BOTH the link pair AND the mirrors fails at planning, because the mirrorTopics
// reconciler reads the prefix from the LIVE OUTBOUND link (which dry-run never
// creates). That product limitation is handled separately; dry-run of mirrorTopics
// is covered against an existing link in destination mode, so it is intentionally
// not repeated here.
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
	in     sectionInput
	link   string
	verify string // the GET command appended to the assembled command list
}

// newSourceMirrorReporter builds a reporter for a source-mode mirror case.
// srcTopics is the relevant set of migration-source topic names to show as evidence.
func newSourceMirrorReporter(name, checks, manifest, link string, srcTopics []string) *sourceMirrorReporter {
	r := &sourceMirrorReporter{link: link}
	if !reportEnabled {
		return r
	}
	mirrorsURL := restSource.baseURL + "/kafka/v3/clusters/" + sourceClusterID + "/links/" + link + "/mirrors"
	r.verify = "GET " + mirrorsURL + "   # mirrors on the migration-dest (INBOUND side)"
	// commands are assembled at commit() to match the output actually captured.
	r.in = sectionInput{
		seq:      nextReportSeq(),
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
		r.in.apply = out
	}
}

func (r *sourceMirrorReporter) reapply(out string) {
	if reportEnabled {
		r.in.reapply = out
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
	r.in.commands = applyCommands(r.in, r.verify)
	r.in.results = append(r.in.results, mirrorsResult(migDestPoller, sourceClusterID, r.link))
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
// Case 2 — source-initiated DATA FLOW: produce → mirror → consume
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
