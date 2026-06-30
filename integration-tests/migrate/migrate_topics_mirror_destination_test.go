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

// This file is the destination-initiated mode:mirror integration matrix. Every
// case seeds the standard topic catalog on the SOURCE broker (idempotent),
// builds a destination-mode manifest with a topics:{mode:mirror} selection, and
// asserts the mirrors created on the target dest. Cases cover glob families,
// wildcards, multi-include, exclude, internal-topic exclusion, empty match, the
// no-prefix path, special-char names, incremental apply, continue-on-error,
// dry-run, partition inheritance, mirror status, and a real produce→mirror→
// consume data-flow proof.
//
// Tests run SERIALLY (no t.Parallel): they share the source/dest brokers, and
// concurrent load re-triggers the cluster-link propagation race that the
// destination auth sweep documents. Catalog fixtures persist for the run and are
// never deleted; links are deleted per-case to keep the concurrent link count low.

// ---------------------------------------------------------------------------
// shared destination mirror manifest + report wiring
// ---------------------------------------------------------------------------

const (
	// source DATA listener (host PLAINTEXT) — produce/consume + KCP source read.
	srcDataBootstrap = "localhost:19092"
	// source INTERNAL listener (docker) — the destination link dials this.
	srcDockerBootstrap = "source:29092"
	// dest DATA listener (host PLAINTEXT) — consume mirrored data from the target.
	destDataBootstrap = "localhost:29092"
)

// mirrorManifestOpts parameterises a destination-mode mirror manifest.
type mirrorManifestOpts struct {
	link    string
	prefix  string // "" => no prefix line (mirror names == source names)
	include []string
	exclude []string
}

// writeMirrorManifest writes the cred files + a destination-mode mirror manifest
// into dir and returns the manifest path. The source read is plaintext on the
// host DATA listener; the link dials the source INTERNAL listener (plaintext);
// the target is the no-auth dest REST.
func writeMirrorManifest(t *testing.T, dir string, o mirrorManifestOpts) string {
	t.Helper()
	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, kafkaAuth{authPlaintext, srcDataBootstrap})
	linkCreds := filepath.Join(dir, "link-source-creds.yaml")
	writeKafkaCreds(t, linkCreds, kafkaAuth{authPlaintext, srcDockerBootstrap})
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", restDest)

	var b strings.Builder
	b.WriteString("apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\n")
	b.WriteString("metadata:\n  name: mcl-" + o.link + "\n")
	b.WriteString("spec:\n  source:\n    type: apache-kafka\n    bootstrapServers: [\"" + srcDataBootstrap + "\"]\n    credentials: " + srcCreds + "\n")
	b.WriteString("  target:\n    type: confluent-platform\n    credentials: " + targetCreds + "\n")
	b.WriteString("    kafka:\n      restEndpoint: " + restDest.baseURL + "\n")
	b.WriteString("  clusterLink:\n    name: " + o.link + "\n    mode: destination\n")
	b.WriteString("    source:\n      bootstrapServers: [\"" + srcDockerBootstrap + "\"]\n      credentials: " + linkCreds + "\n")
	if o.prefix != "" {
		b.WriteString("    prefix: \"" + o.prefix + "\"\n")
	}
	b.WriteString("  topics:\n    mode: mirror\n")
	b.WriteString("    include: [" + quoteList(o.include) + "]\n")
	if len(o.exclude) > 0 {
		b.WriteString("    exclude: [" + quoteList(o.exclude) + "]\n")
	}

	m := filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(m, []byte(b.String()), 0600))
	return m
}

// uniqueMirrorPrefix derives a per-test cluster-link prefix from the unique link
// name. cp-server enforces that a cluster-link prefix is UNIQUE per cluster: two
// links on the same dest cannot share a prefix (40002 "prefix already exists").
// Every destination case here targets the same dest broker, so each must use a
// distinct prefix — and a link that still has live mirror topics cannot be
// deleted (403), so a stale link would otherwise block a later same-prefix link.
// Deriving the prefix from the link name guarantees uniqueness across cases and
// across re-runs. The prefix ends in "." (a legal topic-name character).
func uniqueMirrorPrefix(link string) string {
	return strings.ReplaceAll(link, "-", "_") + "."
}

// quoteList renders a YAML flow-sequence body of double-quoted strings.
func quoteList(items []string) string {
	qs := make([]string, len(items))
	for i, s := range items {
		qs[i] = "\"" + s + "\""
	}
	return strings.Join(qs, ", ")
}

// mirrorReporter accumulates one mirror test case's evidence; all methods are
// cheap no-ops when reportEnabled is false. It captures source topics, the
// manifest, the commands, apply/reapply output, and a live read of the mirrors
// on the target.
type mirrorReporter struct {
	in       sectionInput
	manifest string
	link     string
}

// newMirrorReporter builds a reporter for a destination-mode mirror case.
// srcTopics is the relevant set of source topic names to show as evidence.
func newMirrorReporter(name, checks, manifest, link string, srcTopics []string) *mirrorReporter {
	r := &mirrorReporter{manifest: manifest, link: link}
	if !reportEnabled {
		return r
	}
	r.in = sectionInput{
		seq:      nextReportSeq(),
		category: catMirror,
		mode:     "destination",
		name:     name,
		checks:   checks,
		manifest: readFileForReport(manifest),
		results:  []resultBlock{topicListResult("source topics (catalog)", "", srcTopics)},
		pass:     true,
	}
	return r
}

func (r *mirrorReporter) apply(out string) {
	if reportEnabled {
		r.in.addRun("Apply", applyCmd, out)
	}
}

func (r *mirrorReporter) reapply(out string) {
	if reportEnabled {
		r.in.addRun("Idempotent re-apply", applyCmd, out)
	}
}

func (r *mirrorReporter) dryRun(out string) {
	if reportEnabled {
		r.in.addRun("Dry run", applyDryRunCmd, out)
	}
}

// expected appends a one-line "expected" note to the report (rendered as part of
// the source-topics evidence section header is awkward, so we fold it into the
// checks sentence at construction; this records a structured expectation block).
func (r *mirrorReporter) expected(note string) {
	if reportEnabled {
		r.in.results = append(r.in.results, topicListResult("expected", "", note))
	}
}

// commit captures the live mirrors on the target (best-effort) and finalises the
// section, marking it FAIL when the test failed.
func (r *mirrorReporter) commit(t *testing.T, poller restClient) {
	if !reportEnabled {
		return
	}
	r.in.addReadBlock(mirrorsResult(poller, destClusterID, r.link))
	if t.Failed() {
		r.in.pass = false
	}
	collector.add(buildSection(r.in))
}

// prefixAll returns prefix+name for each name.
func prefixAll(prefix string, names []string) []string {
	out := make([]string, len(names))
	for i, n := range names {
		out[i] = prefix + n
	}
	return out
}

// ---------------------------------------------------------------------------
// Case 1 — family glob (orders-*) + idempotent re-apply
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_FamilyGlob(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-glob")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-*"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	want := prefixAll(prefix, srcTopics)
	rep := newMirrorReporter(link, "include:[orders-*] with prefix mt. mirrors exactly the four orders-* source topics as mt.orders-1..4; re-apply is an idempotent no-op.", m, link, srcTopics)
	rep.expected("orders-* → 4 prefixed mirrors: mt.orders-1, mt.orders-2, mt.orders-3, mt.orders-4")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 4 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, want)
	require.Len(t, poller.listMirrorTopics(destClusterID, link), 4, "exactly 4 mirrors")

	out, err = runKCP(t, m)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 0 created, 4 unchanged", out)
}

// ---------------------------------------------------------------------------
// Case 1b — drift: a mirror is created ACTIVE, then paused out-of-band; re-apply
// must REPORT it as drift (present but not ACTIVE) and never alter/recreate it.
//
// This is the realistic "user tampered with a mirror" case. The source-name
// mismatch we first considered is structurally impossible: cp-server enforces
// mirror_topic_name == prefix + source_topic_name (error 40035), so a mirror
// named prefix+S always mirrors S. A paused/stopped/failed mirror, however, is
// constructible and worth catching.
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_DriftOnInactiveMirror(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-drift")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-1"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	mirror := prefix + "orders-1"
	rep := newMirrorReporter(link, "a mirror is created ACTIVE, then paused out-of-band (operator tamper); re-apply reports it as drift (present but status PAUSED) and never alters or recreates it.", m, link, []string{"orders-1"})
	rep.expected("after pause, re-apply reports 0 created, 0 unchanged, 1 drift, 0 failed; the mirror stays PAUSED (drift is report-only)")
	defer rep.commit(t, poller)

	// 1. Apply → mirror created and ACTIVE.
	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 1 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorStatus(t, destClusterID, link, mirror, "ACTIVE")

	// 2. Simulate an operator pausing the mirror out-of-band.
	poller.pauseMirror(t, destClusterID, link, mirror)
	poller.requireMirrorStatus(t, destClusterID, link, mirror, "PAUSED")

	// 3. Re-apply → drift reported; the mirror is left exactly as it was.
	out, err = runKCP(t, m)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 0 created, 0 unchanged, 1 drift, 0 failed", out)
	require.Contains(t, out, `present but mirror status is "PAUSED"`, out)
	require.Len(t, poller.listMirrorTopics(destClusterID, link), 1, "drift must not create or remove the mirror")
	require.Equal(t, "PAUSED", poller.mirrorStatuses(destClusterID, link)[mirror], "drift is report-only; the mirror stays paused")
}

// ---------------------------------------------------------------------------
// Case 2 — single-char ? wildcard
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_QuestionWildcard(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-q")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-?"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	want := prefixAll(prefix, srcTopics)
	rep := newMirrorReporter(link, "include:[orders-?] (single-char wildcard) with prefix mt. mirrors the four single-digit orders source topics.", m, link, srcTopics)
	rep.expected("orders-? → 4 mirrors (single-char match)")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 4 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, want)
	require.Len(t, poller.listMirrorTopics(destClusterID, link), 4, "exactly 4 mirrors")
}

// ---------------------------------------------------------------------------
// Case 3 — multi-include (products-* + transactions-*)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_MultiInclude(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-multi")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"products-*", "transactions-*"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	srcTopics := []string{
		"products-1", "products-2", "products-3", "products-4",
		"transactions-1", "transactions-2", "transactions-3", "transactions-4",
	}
	want := prefixAll(prefix, srcTopics)
	rep := newMirrorReporter(link, "include:[products-*, transactions-*] mirrors both whole families: 8 prefixed mirrors.", m, link, srcTopics)
	rep.expected("products-* (4) + transactions-* (4) → 8 mirrors")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 8 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, want)
	require.Len(t, poller.listMirrorTopics(destClusterID, link), 8, "exactly 8 mirrors")
}

// ---------------------------------------------------------------------------
// Case 4 — exclude
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_Exclude(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-excl")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{
		link: link, prefix: prefix,
		include: []string{"*"},
		exclude: []string{"transactions-*", "latency_ms"},
	})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	rep := newMirrorReporter(link, "include:[*] exclude:[transactions-*, latency_ms]: orders/products families ARE mirrored, no transactions-* mirror, no mt.latency_ms (underscore name), but mt.metrics.cpu (dotted, distinct) IS present.", m, link, catalogUserTopics())
	rep.expected("orders/products mirrored; transactions-* and mt.latency_ms absent; mt.metrics.cpu present")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "== mirrorTopics", out)
	poller.requireLinkActive(t, destClusterID, link)

	// Subset assertions: orders/products families present, the dotted metrics
	// topic present, transactions family and the underscore metrics topic absent.
	mustPresent := prefixAll(prefix, []string{
		"orders-1", "orders-2", "orders-3", "orders-4",
		"products-1", "products-2", "products-3", "products-4",
		"metrics.cpu",
	})
	poller.requireMirrorsPresent(t, destClusterID, link, mustPresent)

	have := map[string]struct{}{}
	for _, n := range poller.listMirrorTopics(destClusterID, link) {
		have[n] = struct{}{}
	}
	for _, n := range []string{"transactions-1", "transactions-2", "transactions-3", "transactions-4"} {
		_, ok := have[prefix+n]
		require.False(t, ok, "excluded transactions topic %q must not be mirrored", prefix+n)
	}
	_, ok := have[prefix+"latency_ms"]
	require.False(t, ok, "excluded mt.latency_ms (underscore name) must not be mirrored")
	_, ok = have[prefix+"metrics.cpu"]
	require.True(t, ok, "mt.metrics.cpu (dotted, distinct) must be mirrored")
}

// ---------------------------------------------------------------------------
// Case 5 — internal-topic exclusion (include:[*], no exclude)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_InternalExclusion(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-all")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"*"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	rep := newMirrorReporter(link, "include:[*] with no exclude mirrors every user catalog topic (prefixed) and ZERO internal (leading-underscore) topic — __consumer_offsets, _confluent*, _schemas are never mirrored.", m, link, catalogUserTopics())
	rep.expected("all catalogUserTopics() mirrored as mt.*; no mirror derived from a leading-underscore source topic")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "== mirrorTopics", out)
	poller.requireLinkActive(t, destClusterID, link)

	want := prefixAll(prefix, catalogUserTopics())
	poller.requireMirrorsPresent(t, destClusterID, link, want)

	// No mirror may derive from an internal (leading-underscore) source topic.
	// Mirror names are prefix+source; source names like __consumer_offsets and
	// _schemas would yield "mt._..." — i.e. the prefix immediately followed by an
	// underscore. Assert none of the live mirrors look like that.
	for _, name := range poller.listMirrorTopics(destClusterID, link) {
		stripped := strings.TrimPrefix(name, prefix)
		require.False(t, strings.HasPrefix(stripped, "_"),
			"internal source topic must never be mirrored, but found mirror %q", name)
	}
}

// ---------------------------------------------------------------------------
// Case 5b — internal-topic opt-in (Option A): an underscore-leading include
// pattern admits a specific internal topic; "*" still excludes the rest.
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_InternalOptIn(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-optin")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)
	// A real leading-underscore source topic to opt in (kept out of the shared
	// catalog so the InternalExclusion case stays valid).
	srcPoller.createTopic(t, sourceClusterID, "_optin", 1)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"*", "_optin"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	optinAll := append(catalogUserTopics(), "_optin")
	rep := newMirrorReporter(link, "include:[*, _optin]: all user topics mirrored AND the opted-in internal topic _optin (as <prefix>_optin); other internal topics (__consumer_offsets, _confluent*) still excluded.", m, link, optinAll)
	rep.expected("catalogUserTopics() + _optin mirrored as <prefix>*; no OTHER leading-underscore source topic mirrored")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "== mirrorTopics", out)
	poller.requireLinkActive(t, destClusterID, link)

	// User topics AND the opted-in internal topic mirrored.
	poller.requireMirrorsPresent(t, destClusterID, link, prefixAll(prefix, optinAll))

	// The ONLY leading-underscore mirror may be the opted-in _optin; broker
	// internals (__consumer_offsets, _confluent*) are NOT opted in by "*".
	for _, name := range poller.listMirrorTopics(destClusterID, link) {
		stripped := strings.TrimPrefix(name, prefix)
		if strings.HasPrefix(stripped, "_") {
			require.Equal(t, "_optin", stripped,
				"only the opted-in _optin internal topic may be mirrored, but found mirror %q", name)
		}
	}
}

// ---------------------------------------------------------------------------
// Case 6 — empty match
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_EmptyMatch(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-empty")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"nope-*"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	rep := newMirrorReporter(link, "include:[nope-*] matches no source topic: the link is created but mirrorTopics reports 0 created and no mirror exists.", m, link, catalogUserTopics())
	rep.expected("nope-* → 0 mirrors, no error")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 0 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	require.Empty(t, poller.listMirrorTopics(destClusterID, link), "no mirrors created on empty match")
}

// ---------------------------------------------------------------------------
// Case 7 — no prefix (mirror names == source names) — the 40035 risk path
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_NoPrefix(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-noprefix")

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	// No prefix line in the manifest.
	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: "", include: []string{"orders-*"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	// This case is unique in landing UNPREFIXED mirror names (orders-1..4) on the
	// shared dest broker — the same fixed catalog names the mode:new matrix
	// reproduces as plain topics. Deleting the link alone leaves those mirror
	// topics behind (a link with live mirrors cannot be deleted — 403), which would
	// make a later new-mode "4 created" assertion see them as unchanged.
	// Delete the mirror topics first (defers run LIFO, so these run BEFORE the link
	// delete) so the dest is clean for subsequent cases.
	defer poller.deleteLink(t, destClusterID, link)
	for _, n := range srcTopics {
		defer poller.deleteTopic(t, destClusterID, n)
	}

	rep := newMirrorReporter(link, "clusterLink with NO prefix: mirror names equal the source names (orders-1..4, unprefixed); created cleanly and idempotent (the 40035 'topic already exists' risk path).", m, link, srcTopics)
	rep.expected("no prefix → mirror names == source names: orders-1, orders-2, orders-3, orders-4")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 4 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, srcTopics)
	require.Len(t, poller.listMirrorTopics(destClusterID, link), 4, "exactly 4 unprefixed mirrors")

	out, err = runKCP(t, m)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 0 created, 4 unchanged", out)
}

// ---------------------------------------------------------------------------
// Case 8 — special-char names (dot, underscore, mixed)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_SpecialCharNames(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-special")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{
		link: link, prefix: prefix,
		include: []string{"orders.created.*", "inventory_*", "events-2026.*"},
	})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	srcTopics := []string{"orders.created.v2", "inventory_snapshot", "events-2026.q1"}
	want := prefixAll(prefix, srcTopics)
	rep := newMirrorReporter(link, "include globs over dotted/underscored/mixed source names create mirrors mt.orders.created.v2, mt.inventory_snapshot, mt.events-2026.q1 — exercises REST URL-escaping of special characters.", m, link, srcTopics)
	rep.expected("orders.created.* / inventory_* / events-2026.* → 3 special-char mirrors")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 3 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, want)
}

// ---------------------------------------------------------------------------
// Case 9 — partial / incremental apply
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_Incremental(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-incr")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m1 := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-1"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	rep := newMirrorReporter(link, "apply include:[orders-1] (1 created), then widen to include:[orders-*]: the three new orders topics are created and orders-1 is reported unchanged.", m1, link, srcTopics)
	rep.expected("first apply: orders-1 (1 created); second apply orders-*: 3 created, 1 unchanged")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m1)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 1 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, []string{prefix + "orders-1"})

	// Widen the selection on the SAME link.
	m2 := filepath.Join(dir, "migration2.yaml")
	wide := strings.Replace(readFileForReport(m1), `include: ["orders-1"]`, `include: ["orders-*"]`, 1)
	require.NoError(t, os.WriteFile(m2, []byte(wide), 0600))

	out, err = runKCP(t, m2)
	rep.reapply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 3 created, 1 unchanged", out)
	poller.requireMirrorsPresent(t, destClusterID, link, prefixAll(prefix, srcTopics))
}

// ---------------------------------------------------------------------------
// Case 10 — failure / continue-on-error
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_ContinueOnError(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-fail")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{
		link: link, prefix: prefix,
		include: []string{"orders-1", "orders-2"},
	})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	// Force ONE mirror to fail at the broker while the other succeeds: pre-create
	// a PLAIN topic on the dest occupying the second mirror's target name. cp-server
	// then rejects that mirror create (the topic already exists) while orders-1
	// mirrors cleanly. A non-existent source topic would instead be silently
	// dropped by topic selection (never planned), so it cannot exercise a failure.
	blocker := prefix + "orders-2"
	poller.createTopic(t, destClusterID, blocker, 1)
	defer poller.deleteTopic(t, destClusterID, blocker)

	srcTopics := []string{"orders-1", "orders-2"}
	rep := newMirrorReporter(link, "include:[orders-1, orders-2] where the target name for orders-2 is pre-occupied by a plain dest topic: orders-1 mirrors successfully while the orders-2 mirror fails — apply reports 1 created + 1 failed, prints a ✖ line, and kcp exits non-zero; the good mirror survives the failure.", m, link, srcTopics)
	rep.expected("orders-1 mirror created; orders-2 mirror fails (name pre-occupied); output shows '1 created' and '1 failed' and '✖'; exit non-zero")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.Error(t, err, "kcp must exit non-zero when a mirror fails:\n%s", out)
	// Scope to the mirrorTopics outcome line. A bare "1 created"/"1 failed" would
	// also match the engine's `clusterLink: 1 created, …` line, so the test could
	// pass even if mirrorTopics created nothing — assert the full rendered line.
	require.Contains(t, out, "mirrorTopics: 1 created, 0 unchanged, 0 drift, 1 failed", out)
	require.Contains(t, out, "✖", out)

	// Despite the failure, the good mirror was created.
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, []string{prefix + "orders-1"})
}

// ---------------------------------------------------------------------------
// Case 11 — dry-run
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_DryRun(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-dry")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	// FROM-SCRATCH dry-run: a manifest that creates BOTH the cluster link and the
	// mirrors. The link does not exist yet, so the mirrorTopics reconciler cannot
	// read cluster.link.prefix off a live link during Plan — it falls back to the
	// manifest's clusterLink.prefix (exactly the value the link will be created
	// with). Dry-run must therefore PLAN the four PREFIXED mirrors (proving the
	// fallback drove planning) while creating NOTHING — neither the link nor any
	// mirror.
	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-*"}})
	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	rep := newMirrorReporter(link, "from-scratch --dry-run with include:[orders-*] on a NOT-YET-CREATED link PLANS the four prefixed mirrors (output shows 'Planned' + the prefixed mirror-topic plan lines, e.g. "+prefix+"orders-1) via the manifest-prefix fallback — but creates nothing: neither the link nor any mirror.", m, link, srcTopics)
	rep.expected("dry-run: plans 4 prefixed mirrors, creates 0; link absent and /mirrors empty afterward")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m, "--dry-run")
	rep.dryRun(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "Planned", out)
	// The planned mirror names are PREFIXED — proof the manifest-prefix fallback
	// drove planning rather than a (non-existent) live link read.
	require.Contains(t, out, prefix+"orders-1", out)

	// Dry-run created nothing: the link itself was never created, so the dest has
	// no link state and no mirrors.
	require.Empty(t, poller.linkState(destClusterID, link), "dry-run must not create the cluster link")
	require.Empty(t, poller.listMirrorTopics(destClusterID, link), "dry-run must not create any mirror")
}

// ---------------------------------------------------------------------------
// Case 12 — partition inheritance (orders-1 seeded with 3 partitions)
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_PartitionInheritance(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-part")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)
	require.Equal(t, 3, srcPoller.topicPartitions(sourceClusterID, "orders-1"), "catalog orders-1 must have 3 partitions")

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-1"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	mirror := prefix + "orders-1"
	rep := newMirrorReporter(link, "mirroring orders-1 (3 partitions on the source) yields a dest mirror that inherits the source partition count (3).", m, link, []string{"orders-1"})
	rep.expected("mt.orders-1 reports 3 partitions on the target (inherited from source)")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 1 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, []string{mirror})

	// Partition count appears once the mirror topic is materialised; poll briefly.
	deadline := time.Now().Add(30 * time.Second)
	var got int
	for time.Now().Before(deadline) {
		got = poller.topicPartitions(destClusterID, mirror)
		if got == 3 {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.Equal(t, 3, got, "mirror %q must inherit the source partition count (3)", mirror)
}

// ---------------------------------------------------------------------------
// Case 13 — mirror status not FAILED
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_StatusNotFailed(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-status")
	prefix := uniqueMirrorPrefix(link)

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)
	seedTopicCatalog(t, srcPoller, sourceClusterID)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{"orders-*"}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	srcTopics := []string{"orders-1", "orders-2", "orders-3", "orders-4"}
	want := prefixAll(prefix, srcTopics)
	rep := newMirrorReporter(link, "after creating the orders-* mirrors, every mirror's mirror_status reaches ACTIVE within 60s — the link is healthily replicating, not stuck or erroring (ACTIVE implies non-FAILED and non-stuck).", m, link, srcTopics)
	rep.expected("each mirror's mirror_status reaches ACTIVE (never FAILED, never stuck PENDING)")
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 4 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, want)

	// Poll until every mirror reaches ACTIVE. ACTIVE is the healthy steady state:
	// it implies non-FAILED and proves the mirror is not stuck in a transitional
	// state (e.g. PENDING) forever. Fail on timeout with the last observed statuses.
	deadline := time.Now().Add(60 * time.Second)
	var statuses map[string]string
	for time.Now().Before(deadline) {
		statuses = poller.mirrorStatuses(destClusterID, link)
		// Guard on FAILED immediately — a FAILED mirror will never become ACTIVE.
		for name, s := range statuses {
			require.NotEqual(t, "FAILED", s, "mirror %q must not be FAILED", name)
		}
		allActive := len(statuses) >= len(want)
		for _, s := range statuses {
			if s != "ACTIVE" {
				allActive = false
			}
		}
		if allActive {
			break
		}
		time.Sleep(2 * time.Second)
	}
	require.NotEmpty(t, statuses, "mirror statuses must be readable")
	require.GreaterOrEqual(t, len(statuses), len(want), "all mirror statuses must be present; observed %v", statuses)
	for name, s := range statuses {
		require.Equal(t, "ACTIVE", s, "mirror %q must reach ACTIVE within the window (last statuses: %v)", name, statuses)
	}
}

// ---------------------------------------------------------------------------
// Case 14 — DATA FLOW: produce → mirror → consume
// ---------------------------------------------------------------------------

func TestMigrateApply_TopicsMirror_DataFlow(t *testing.T) {
	dir := t.TempDir()
	link := uniqueLinkName("mt-flow")
	prefix := uniqueMirrorPrefix(link)
	const nRecords = 10

	srcPoller := newRestClient(t, restSource)
	srcPoller.waitForClusterID(t)

	// Dedicated single-partition source topic so consumeRecords (partition 0)
	// sees every record. Created + populated BEFORE the mirror is established.
	dataTopic := uniqueTopicName("dataflow")
	srcPoller.createTopic(t, sourceClusterID, dataTopic, 1)
	defer srcPoller.deleteTopic(t, sourceClusterID, dataTopic)

	produceRecords(t, srcDataBootstrap, dataTopic, nRecords)

	m := writeMirrorManifest(t, dir, mirrorManifestOpts{link: link, prefix: prefix, include: []string{dataTopic}})

	poller := newRestClient(t, restDest)
	poller.waitForClusterID(t)
	defer poller.deleteLink(t, destClusterID, link)

	mirror := prefix + dataTopic
	rep := newMirrorReporter(link, "produce 10 records to a dedicated single-partition source topic, mirror it, then consume from the dest mirror over the dest DATA listener — all 10 record values replicate end-to-end.", m, link, []string{dataTopic})
	rep.expected(fmt.Sprintf("%d records produced to %s replicate to dest mirror %s", nRecords, dataTopic, mirror))
	defer rep.commit(t, poller)

	out, err := runKCP(t, m)
	rep.apply(out)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics: 1 created", out)
	poller.requireLinkActive(t, destClusterID, link)
	poller.requireMirrorsPresent(t, destClusterID, link, []string{mirror})

	// Mirroring is async: give it a generous window to materialise + replicate.
	got := consumeRecords(t, destDataBootstrap, mirror, nRecords, 90*time.Second)
	require.Len(t, got, nRecords, "all %d records must replicate to the dest mirror %q", nRecords, mirror)

	want := map[string]struct{}{}
	for i := 0; i < nRecords; i++ {
		want[fmt.Sprintf("msg-%d", i)] = struct{}{}
	}
	for _, v := range got {
		delete(want, string(v))
	}
	require.Empty(t, want, "every produced record value must arrive on the dest mirror; missing: %v", want)
}
