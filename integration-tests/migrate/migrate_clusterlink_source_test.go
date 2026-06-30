//go:build integration

package migrate

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

// sourceCase is one source-initiated permutation. Two links share one name: the
// INBOUND link on the migration-dest (target REST, D3) and the OUTBOUND link on
// the migration-source REST (D4); the OUTBOUND link dials the migration-dest
// using destinationCredentials (D5). spec.source.credentials (D1) reads the
// migration-source cluster id over a plaintext HOST listener.
type sourceCase struct {
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

// writeSourceManifest writes the manifest + cred files for a source test case
// and returns the manifest path and link name.
func writeSourceManifest(t *testing.T, dir string, c sourceCase) (manifestPath, linkName string) {
	t.Helper()
	linkName = uniqueLinkName("source")

	// D1: migration-source cluster-id read.
	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, c.d1)

	// D4: migration-source REST creds (where the OUTBOUND link is created).
	srcRestCreds := writeRestCreds(t, dir, "source-rest-creds.yaml", c.migrationSourceREST)

	// D3: migration-dest (target) REST creds (where the INBOUND link is created).
	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", c.migrationDestREST)

	// D5: source→destination connection creds.
	destConnCreds := filepath.Join(dir, "dest-conn-creds.yaml")
	writeKafkaCreds(t, destConnCreds, c.d5)

	manifest := "apiVersion: kcp.confluent.io/v1alpha1\n" +
		"kind: Migration\n" +
		"metadata:\n" +
		"  name: mcl-" + linkName + "\n" +
		"spec:\n" +
		"  source:\n" +
		"    type: confluent-platform\n" +
		"    bootstrapServers: [\"" + c.d1.bootstrap + "\"]\n" +
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
		"    destination:\n" +
		"      bootstrapServers: [\"" + c.d5.bootstrap + "\"]\n" +
		"      credentials: " + destConnCreds + "\n"

	manifestPath = filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0600))
	return manifestPath, linkName
}

// TestMigrateApply_ClusterLink_Source sweeps the source-initiated auth surfaces
// (D3 migration-dest REST, D4 migration-source REST, D5 source→dest connection),
// one surface at a time. Each test case creates TWO links (INBOUND on the
// migration-dest, OUTBOUND on the migration-source), both reaching ACTIVE.
func TestMigrateApply_ClusterLink_Source(t *testing.T) {
	// D1 reads the migration-source over a plaintext HOST listener. Per
	// migration-source broker:
	//   dest-basic  → localhost:29192
	//   dest-mtls   → localhost:29292
	//   dest-bearer → localhost:29392
	//   source      → localhost:19092
	d1DestBasic := kafkaAuth{authPlaintext, "localhost:29192"}

	cases := []sourceCase{
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
		{"D5=plain-tls", d1DestBasic, restDestBasic, restSource, kafkaAuth{authPlainTLS, "source:29094"}},
		{"D5=tls", d1DestBasic, restDestBasic, restSource, kafkaAuth{authTLS, "source:29095"}},
		{"D5=mtls", d1DestBasic, restDestBasic, restSource, kafkaAuth{authMTLS, "source:29095"}},
	}

	// Source test cases are NOT run in parallel. Each creates a pair of links
	// (INBOUND then OUTBOUND); the OUTBOUND create validates against the
	// destination by connecting and confirming the INBOUND link is present.
	// cp-server propagates the freshly-created INBOUND link asynchronously, so
	// concurrent test cases hammering the shared brokers can make a test case's
	// own INBOUND link not-yet-visible when its OUTBOUND link validates ("the
	// destination cluster does not have a link named X"). Running serially
	// removes that cross-test-case pressure.
	for _, c := range cases {
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
			// commit() runs via defer so a test case that fails mid-flight still
			// emits what it captured, marked FAIL.
			rep := newSourceReporter(c, dir, manifest, linkName, destID, srcID)
			defer rep.commit(t, destPoller, srcPoller)

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

			// Capture both live link states (INBOUND on dest, OUTBOUND on
			// source) before deletion.
			rep.result(destPoller, srcPoller)

			// re-apply is an idempotent no-op for both sides.
			out, err = runKCP(t, manifest)
			rep.reapply(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "2 unchanged", out)

			// Delete both sides (OUTBOUND first, then INBOUND).
			srcPoller.deleteLink(t, srcID, linkName)
			destPoller.deleteLink(t, destID, linkName)
		})
	}
}

// ---------------------------------------------------------------------------
// source-test-case report capture
// ---------------------------------------------------------------------------

// sourceChecks maps a source test case to its one-sentence "what it checks" statement.
func sourceChecks(c sourceCase) string {
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

// sourceReporter accumulates one source test case's evidence. All methods are
// cheap no-ops when reportEnabled is false.
type sourceReporter struct {
	in       sectionInput
	link     string
	destID   string
	srcID    string
	destREST restEndpoint
	srcREST  restEndpoint
}

func newSourceReporter(c sourceCase, dir, manifest, linkName, destID, srcID string) *sourceReporter {
	r := &sourceReporter{
		link: linkName, destID: destID, srcID: srcID,
		destREST: c.migrationDestREST, srcREST: c.migrationSourceREST,
	}
	if !reportEnabled {
		return r
	}
	r.in = sectionInput{
		seq:      nextReportSeq(),
		category: catClusterLink,
		mode:     "source",
		name:     c.name,
		checks:   sourceChecks(c),
		manifest: readFileForReport(manifest),
		creds: []fencedFile{
			{"D1 source-read", "source-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "source-creds.yaml"))},
			{"D4 migration-source REST", "source-rest-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "source-rest-creds.yaml"))},
			{"D3 migration-dest REST", "target-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "target-creds.yaml"))},
			{"D5 source→dest connection", "dest-conn-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "dest-conn-creds.yaml"))},
		},
		pass: true,
	}
	return r
}

func (r *sourceReporter) dryRun(out string) {
	if reportEnabled {
		r.in.addRun("Dry run", applyDryRunCmd, out)
	}
}

func (r *sourceReporter) apply(out string) {
	if reportEnabled {
		r.in.addRun("Apply", applyCmd, out)
	}
}

func (r *sourceReporter) result(destPoller, srcPoller restClient) {
	if reportEnabled {
		r.in.addReadBlock(resultBlock{
			label: "INBOUND link on migration-dest",
			url:   linkURL(r.destREST.baseURL, r.destID, r.link),
			json:  destPoller.linkJSON(r.destID, r.link),
		})
		r.in.addReadBlock(resultBlock{
			label: "OUTBOUND link on migration-source",
			url:   linkURL(r.srcREST.baseURL, r.srcID, r.link),
			json:  srcPoller.linkJSON(r.srcID, r.link),
		})
	}
}

func (r *sourceReporter) reapply(out string) {
	if reportEnabled {
		r.in.addRun("Idempotent re-apply", applyCmd, out)
	}
}

// commit finalises the section. The pollers are used for a best-effort live GET
// of each link when the test case failed (neither link reached ACTIVE, so
// result() was never called) so the failure section still shows the observed
// link states and why.
func (r *sourceReporter) commit(t *testing.T, destPoller, srcPoller restClient) {
	if !reportEnabled {
		return
	}
	if t.Failed() {
		r.in.pass = false
		r.captureFailureState(destPoller, srcPoller)
	}
	collector.add(buildSection(r.in))
}

// captureFailureState does a best-effort live GET of both links so a failed test
// case shows the observed state + link_error of each. Never panics or fails the commit.
func (r *sourceReporter) captureFailureState(destPoller, srcPoller restClient) {
	inbound := failureResultBlock("INBOUND link on migration-dest", destPoller, r.destREST.baseURL, r.destID, r.link)
	outbound := failureResultBlock("OUTBOUND link on migration-source", srcPoller, r.srcREST.baseURL, r.srcID, r.link)
	r.in.addReadBlock(inbound.block)
	r.in.addReadBlock(outbound.block)
	r.in.failMsg = inbound.msg + "; " + outbound.msg
}

// failureResultBlock GETs one link and returns a result block plus a one-line
// description of its observed state for the failure message.
type failureResult struct {
	block resultBlock
	msg   string
}

func failureResultBlock(label string, poller restClient, baseURL, clusterID, name string) failureResult {
	url := linkURL(baseURL, clusterID, name)
	state, linkErr := poller.link(clusterID, name)
	if state == "" {
		return failureResult{
			block: resultBlock{label: label + " (not found)", url: url, json: fmt.Sprintf("<link %q not present on %s>", name, clusterID)},
			msg:   fmt.Sprintf("at failure: link %q on %s was not present", name, clusterID),
		}
	}
	return failureResult{
		block: resultBlock{label: label, url: url, json: poller.linkJSON(clusterID, name)},
		msg:   fmt.Sprintf("at failure: link %q on %s was in state %q (link_error: %q)", name, clusterID, state, linkErr),
	}
}
