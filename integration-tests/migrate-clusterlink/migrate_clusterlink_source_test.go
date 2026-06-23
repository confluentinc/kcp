//go:build integration

package migrateclusterlink

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// applySourcePair applies a source-initiated manifest until it succeeds. KCP
// creates the INBOUND link on the destination then the OUTBOUND link on the
// source in a single apply; the OUTBOUND create validates by connecting to the
// destination and confirming the INBOUND link is present. cp-server propagates
// the freshly-created INBOUND link asynchronously, so the OUTBOUND validation
// can momentarily race it (HTTP 400 "the destination cluster does not have a
// link named X"), most often right after broker startup while the link
// coordinator warms up. A re-apply is the correct recovery: the INBOUND side is
// already present (idempotent no-op) and only the OUTBOUND side is retried, by
// which time the INBOUND link has propagated. The combined created/present
// count across the final successful apply is always 2 sides.
//
// On the FIRST successful apply (no race) the summary is "2 created". After a
// retry it is "1 created, 1 already present" (INBOUND already there). Either way
// both sides exist; the caller then asserts both reach ACTIVE.
func applySourcePair(t *testing.T, manifest string) {
	t.Helper()
	const propagationRace = "the destination cluster does not have a link named"
	deadline := time.Now().Add(90 * time.Second)
	for attempt := 1; ; attempt++ {
		out, err := runKCP(t, manifest)
		if err == nil {
			require.Equal(t, 2, sidesAccountedFor(t, out),
				"both source-link sides must be created or already present\n%s", out)
			return
		}
		if !strings.Contains(out, propagationRace) || time.Now().After(deadline) {
			require.NoError(t, err, out) // unexpected error or out of retries
		}
		t.Logf("source-link INBOUND propagation race (attempt %d), retrying", attempt)
		time.Sleep(3 * time.Second)
	}
}

// sidesAccountedFor sums the created + already-present counts from a reconcile
// summary line "N created, M already present, K drift".
func sidesAccountedFor(t *testing.T, out string) int {
	t.Helper()
	for _, line := range strings.Split(out, "\n") {
		ci := strings.Index(line, " created,")
		pi := strings.Index(line, " already present")
		if ci < 0 || pi < 0 {
			continue
		}
		created := lastIntBefore(t, line[:ci], line)
		present := lastIntBefore(t, line[strings.LastIndex(line[:pi], ",")+1:pi], line)
		return created + present
	}
	t.Fatalf("no reconcile summary line in output:\n%s", out)
	return 0
}

func lastIntBefore(t *testing.T, s, full string) int {
	t.Helper()
	fields := strings.Fields(s)
	n, err := strconv.Atoi(fields[len(fields)-1])
	require.NoError(t, err, full)
	return n
}

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

			// dry-run previews two creates and changes nothing.
			out, err := runKCP(t, manifest, "--dry-run")
			require.NoError(t, err, out)
			require.Contains(t, out, "Planned")
			require.Empty(t, destPoller.linkState(destID, linkName), "dry-run must not create the INBOUND link")
			require.Empty(t, srcPoller.linkState(srcID, linkName), "dry-run must not create the OUTBOUND link")

			// apply creates BOTH links; both reach ACTIVE.
			applySourcePair(t, manifest)
			destPoller.requireLinkActive(t, destID, linkName)
			srcPoller.requireLinkActive(t, srcID, linkName)

			// re-apply is an idempotent no-op for both sides.
			out, err = runKCP(t, manifest)
			require.NoError(t, err, out)
			require.Contains(t, out, "2 already present", out)

			// Delete both sides (OUTBOUND first, then INBOUND).
			srcPoller.deleteLink(t, srcID, linkName)
			destPoller.deleteLink(t, destID, linkName)
		})
	}
}
