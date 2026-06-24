//go:build integration

package migrateclusterlink

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// A source-initiated apply creates both sides in one call: KCP creates the
// INBOUND link on the destination then the OUTBOUND link on the source; the
// OUTBOUND create races the INBOUND link's asynchronous propagation to the
// destination, which KCP itself retries (see reconciler.createClusterLink), so a
// single `apply` is reliable and yields "2 created" — no test-level retry needed
// (that would mask the product fix).

// sourceCell is one source-initiated permutation. Two links share one name: the
// INBOUND link on the migration-dest (target REST, D3) and the OUTBOUND link on
// the migration-source REST (D4); the OUTBOUND link dials the migration-dest
// using destinationCredentials (D5). spec.source.credentials (D1) reads the
// migration-source cluster id over a plaintext HOST listener.
type sourceCell struct {
	name string
	// D1: read the migration-source cluster id (plaintext HOST listener).
	d1 kafkaAuth
	// migrationSourceREST: where the OUTBOUND link is created (D4).
	migrationSourceREST restEndpoint
	// migrationDestREST: where the INBOUND link is created (D3 target).
	migrationDestREST restEndpoint
	// d5: the source→destination connection the OUTBOUND link dials (D5).
	d5 kafkaAuth
}

// writeSourceManifest writes the manifest + cred files for a source cell and
// returns the manifest path and link name.
func writeSourceManifest(t *testing.T, dir string, c sourceCell) (manifestPath, linkName string) {
	t.Helper()
	linkName = uniqueLinkName("source")

	// D1: migration-source cluster-id read.
	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, "migration-source", c.d1)

	// D4: migration-source REST creds (where the OUTBOUND link is created).
	srcRestCreds := writeRestCreds(t, dir, "source-rest-creds.yaml", c.migrationSourceREST)

	// D3: migration-dest (target) REST creds (where the INBOUND link is created).
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", c.migrationDestREST)

	// D5: source→destination connection creds.
	destConnCreds := filepath.Join(dir, "dest-conn-creds.yaml")
	writeKafkaCreds(t, destConnCreds, "migration-dest", c.d5)

	manifest := "apiVersion: kcp.confluent.io/v1alpha1\n" +
		"kind: Migration\n" +
		"metadata:\n" +
		"  name: mcl-" + linkName + "\n" +
		"spec:\n" +
		"  source:\n" +
		"    type: confluent-platform\n" +
		"    credentials: " + srcCreds + "\n" +
		"  target:\n" +
		"    type: confluent-platform\n" +
		"    credentials: " + targetCreds + "\n" +
		"    kafka:\n" +
		"      restEndpoint: " + c.migrationDestREST.baseURL + "\n" +
		"  clusterLink:\n" +
		"    name: " + linkName + "\n" +
		"    mode: source\n" +
		"    sourceRest:\n" +
		"      endpoint: " + c.migrationSourceREST.baseURL + "\n" +
		"      credentials: " + srcRestCreds + "\n" +
		"    destinationCredentials: " + destConnCreds + "\n"

	manifestPath = filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0600))
	return manifestPath, linkName
}

// TestMigrateApply_ClusterLink_Source sweeps the source-initiated auth surfaces
// (D3 migration-dest REST, D4 migration-source REST, D5 source→dest connection),
// one surface at a time. Each cell creates TWO links (INBOUND on the
// migration-dest, OUTBOUND on the migration-source), both reaching ACTIVE.
func TestMigrateApply_ClusterLink_Source(t *testing.T) {
	// D1 reads the migration-source over a plaintext HOST listener. Per
	// migration-source broker:
	//   dest-basic  → localhost:29192
	//   dest-mtls   → localhost:29292
	//   dest-bearer → localhost:29392
	//   source      → localhost:19092
	d1DestBasic := kafkaAuth{authPlaintext, "localhost:29192"}

	cells := []sourceCell{
		// --- baseline: migration-source=dest-basic, migration-dest=source. ---
		{"baseline", d1DestBasic, restDestBasic, restSource, kafkaAuth{authPlaintext, "source:29092"}},

		// --- D4 sweep: vary migration-source REST auth; migration-dest=source(none). ---
		{"D4=basic", d1DestBasic, restDestBasic, restSource, kafkaAuth{authPlaintext, "source:29092"}},
		{"D4=mtls", kafkaAuth{authPlaintext, "localhost:29292"}, restDestMTLS, restSource, kafkaAuth{authPlaintext, "source:29092"}},
		{"D4=bearer", kafkaAuth{authPlaintext, "localhost:29392"}, restDestBearer, restSource, kafkaAuth{authPlaintext, "source:29092"}},

		// --- D3 sweep: migration-source=source; vary migration-dest REST. D5
		// dials the chosen migration-dest's INTERNAL plaintext listener. ---
		{"D3=none", kafkaAuth{authPlaintext, "localhost:19092"}, restSource, restDest, kafkaAuth{authPlaintext, "dest:39092"}},
		{"D3=basic", kafkaAuth{authPlaintext, "localhost:19092"}, restSource, restDestBasic, kafkaAuth{authPlaintext, "dest-basic:39092"}},
		{"D3=mtls", kafkaAuth{authPlaintext, "localhost:19092"}, restSource, restDestMTLS, kafkaAuth{authPlaintext, "dest-mtls:39092"}},
		{"D3=bearer", kafkaAuth{authPlaintext, "localhost:19092"}, restSource, restDestBearer, kafkaAuth{authPlaintext, "dest-bearer:39092"}},

		// --- D5 sweep: vary source→dest connection auth dialing the source
		// broker's docker listeners. migration-source=dest-basic,
		// migration-dest=source. ---
		{"D5=plaintext", d1DestBasic, restDestBasic, restSource, kafkaAuth{authPlaintext, "source:29092"}},
		{"D5=scram256", d1DestBasic, restDestBasic, restSource, kafkaAuth{authScram256, "source:29094"}},
		{"D5=scram512", d1DestBasic, restDestBasic, restSource, kafkaAuth{authScram512, "source:29094"}},
		{"D5=plain", d1DestBasic, restDestBasic, restSource, kafkaAuth{authPlain, "source:29096"}},
		{"D5=tls", d1DestBasic, restDestBasic, restSource, kafkaAuth{authTLS, "source:29095"}},
		{"D5=mtls", d1DestBasic, restDestBasic, restSource, kafkaAuth{authMTLS, "source:29095"}},
	}

	// Source cells are NOT run in parallel. Each creates a pair of links
	// (INBOUND then OUTBOUND); the OUTBOUND create validates against the
	// destination by connecting and confirming the INBOUND link is present.
	// cp-server propagates the freshly-created INBOUND link asynchronously, so
	// concurrent cells hammering the shared brokers can make a cell's own
	// INBOUND link not-yet-visible when its OUTBOUND link validates ("the
	// destination cluster does not have a link named X"). Running serially
	// removes that cross-cell pressure.
	for _, c := range cells {
		c := c
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			manifest, linkName := writeSourceManifest(t, dir, c)

			// INBOUND link lives on the migration-dest; OUTBOUND on the
			// migration-source.
			destPoller := newRestClient(t, c.migrationDestREST)
			srcPoller := newRestClient(t, c.migrationSourceREST)
			destPoller.waitForClusterID(t)
			srcPoller.waitForClusterID(t)
			destID := c.migrationDestREST.clusterID
			srcID := c.migrationSourceREST.clusterID

			// Report capture (no-op + zero extra work when reportEnabled is false).
			// commit() runs via defer so a cell that fails mid-flight still emits
			// what it captured, marked FAIL.
			rep := newSourceReporter(c, dir, manifest, linkName, destID, srcID)
			defer rep.commit(t)

			// dry-run previews two creates and changes nothing.
			out, err := runKCP(t, manifest, "--dry-run")
			rep.dryRun(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "Planned")
			require.Empty(t, destPoller.linkState(destID, linkName), "dry-run must not create the INBOUND link")
			require.Empty(t, srcPoller.linkState(srcID, linkName), "dry-run must not create the OUTBOUND link")

			// apply creates BOTH links; both reach ACTIVE.
			out, err = runKCP(t, manifest)
			rep.apply(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "2 created", out)
			destPoller.requireLinkActive(t, destID, linkName)
			srcPoller.requireLinkActive(t, srcID, linkName)

			// Capture both live ACTIVE proofs (INBOUND on dest, OUTBOUND on
			// source) before deletion.
			rep.proof(destPoller, srcPoller)

			// re-apply is an idempotent no-op for both sides.
			out, err = runKCP(t, manifest)
			rep.reapply(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "2 already present", out)

			// Delete both sides (OUTBOUND first, then INBOUND).
			srcPoller.deleteLink(t, srcID, linkName)
			destPoller.deleteLink(t, destID, linkName)
		})
	}
}

// ---------------------------------------------------------------------------
// source-cell report capture
// ---------------------------------------------------------------------------

// sourceProves maps a source cell to its one-sentence proof statement.
func sourceProves(c sourceCell) string {
	switch {
	case c.name == "baseline":
		return "Source-initiated migration creates both the INBOUND link (on the migration-dest, target REST) and the OUTBOUND link (on the migration-source REST); both reach ACTIVE."
	case strings.HasPrefix(c.name, "D4="):
		return fmt.Sprintf("KCP authenticates to the migration-source Kafka REST with %s auth (spec.clusterLink.sourceRest) and creates the OUTBOUND link there; both links reach ACTIVE.", c.migrationSourceREST.kind)
	case strings.HasPrefix(c.name, "D3="):
		return fmt.Sprintf("KCP authenticates to the migration-dest Kafka REST with %s auth (spec.target.credentials) and creates the INBOUND link there; both links reach ACTIVE.", c.migrationDestREST.kind)
	case strings.HasPrefix(c.name, "D5="):
		return fmt.Sprintf("KCP builds a %s source→destination connection (spec.clusterLink.destinationCredentials) that the OUTBOUND link uses to reach the migration-dest; both links reach ACTIVE.", c.d5.kind)
	}
	return "source-initiated cluster link pair reaches ACTIVE."
}

// sourceReporter accumulates one source cell's evidence. All methods are cheap
// no-ops when reportEnabled is false.
type sourceReporter struct {
	in       sectionInput
	link     string
	destID   string
	srcID    string
	destREST restEndpoint
	srcREST  restEndpoint
}

func newSourceReporter(c sourceCell, dir, manifest, linkName, destID, srcID string) *sourceReporter {
	r := &sourceReporter{
		link: linkName, destID: destID, srcID: srcID,
		destREST: c.migrationDestREST, srcREST: c.migrationSourceREST,
	}
	if !reportEnabled {
		return r
	}
	r.in = sectionInput{
		seq:      nextReportSeq(),
		mode:     "source",
		cell:     c.name,
		proves:   sourceProves(c),
		manifest: readFileForReport(manifest),
		creds: []fencedFile{
			{"D1 source-read", "source-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "source-creds.yaml"))},
			{"D4 migration-source REST", "source-rest-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "source-rest-creds.yaml"))},
			{"D3 migration-dest REST", "target-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "target-creds.yaml"))},
			{"D5 source→dest connection", "dest-conn-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "dest-conn-creds.yaml"))},
		},
		commands: []string{
			"kcp migrate apply -f migration.yaml --dry-run",
			"kcp migrate apply -f migration.yaml",
			"GET " + linkURL(c.migrationDestREST.baseURL, destID, linkName) + "   # INBOUND on migration-dest",
			"GET " + linkURL(c.migrationSourceREST.baseURL, srcID, linkName) + "   # OUTBOUND on migration-source",
		},
		pass: true,
	}
	return r
}

func (r *sourceReporter) dryRun(out string) {
	if reportEnabled {
		r.in.dryRun = out
	}
}

func (r *sourceReporter) apply(out string) {
	if reportEnabled {
		r.in.apply = out
	}
}

func (r *sourceReporter) proof(destPoller, srcPoller restClient) {
	if reportEnabled {
		r.in.proofs = []proofBlock{
			{
				label: "INBOUND link on migration-dest",
				url:   linkURL(r.destREST.baseURL, r.destID, r.link),
				json:  destPoller.linkJSON(r.destID, r.link),
			},
			{
				label: "OUTBOUND link on migration-source",
				url:   linkURL(r.srcREST.baseURL, r.srcID, r.link),
				json:  srcPoller.linkJSON(r.srcID, r.link),
			},
		}
	}
}

func (r *sourceReporter) reapply(out string) {
	if reportEnabled {
		r.in.reapply = out
	}
}

func (r *sourceReporter) commit(t *testing.T) {
	if !reportEnabled {
		return
	}
	if t.Failed() {
		r.in.pass = false
		if r.in.failMsg == "" {
			r.in.failMsg = "cell failed; see test output. Captured evidence up to the point of failure is shown below."
		}
	}
	collector.add(buildSection(r.in))
}
