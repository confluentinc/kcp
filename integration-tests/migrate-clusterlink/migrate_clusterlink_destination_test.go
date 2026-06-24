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

// destCase is one destination-mode permutation. Exactly one of the D1/D2/D3
// surfaces varies per sweep; the rest are fixed to plaintext / no-auth.
type destCase struct {
	name string
	// D1: KCP's read of the source cluster id (host listener).
	d1 kafkaAuth
	// D2: the link→source connection (docker listener).
	d2 kafkaAuth
	// D3: the target REST where the link is created.
	target restEndpoint
}

// writeDestManifest writes the manifest + cred files for a destination test
// case into dir and returns the manifest path and the link name.
func writeDestManifest(t *testing.T, dir string, c destCase) (manifestPath, linkName string) {
	t.Helper()
	linkName = uniqueLinkName("dest")

	srcCreds := filepath.Join(dir, "source-creds.yaml")
	writeKafkaCreds(t, srcCreds, "source", c.d1)

	linkSrcCreds := filepath.Join(dir, "link-source-creds.yaml")
	writeKafkaCreds(t, linkSrcCreds, "source", c.d2)

	targetCreds := writeRestCreds(t, dir, "target-creds.yaml", c.target)

	manifest := "apiVersion: kcp.confluent.io/v1alpha1\n" +
		"kind: Migration\n" +
		"metadata:\n" +
		"  name: mcl-" + linkName + "\n" +
		"spec:\n" +
		"  source:\n" +
		"    type: apache-kafka\n" +
		"    credentials: " + srcCreds + "\n" +
		"  target:\n" +
		"    type: confluent-platform\n" +
		"    credentials: " + targetCreds + "\n" +
		"    kafka:\n" +
		"      restEndpoint: " + c.target.baseURL + "\n" +
		"  clusterLink:\n" +
		"    name: " + linkName + "\n" +
		"    mode: destination\n" +
		"    sourceCredentials: " + linkSrcCreds + "\n"

	manifestPath = filepath.Join(dir, "migration.yaml")
	require.NoError(t, os.WriteFile(manifestPath, []byte(manifest), 0600))
	return manifestPath, linkName
}

// TestMigrateApply_ClusterLink_Destination sweeps the destination-mode auth
// surfaces (D1 source-read, D2 link→source, D3 target REST), one surface at a
// time. Each test case creates ONE link on the chosen dest dialing the source.
func TestMigrateApply_ClusterLink_Destination(t *testing.T) {
	// Fixed defaults for the surfaces a given sweep does NOT vary.
	const (
		srcHostPlaintext = "localhost:19092" // D1 plaintext (source HOST listener)
		srcDockerPlain   = "source:29092"    // D2 plaintext (source INTERNAL listener)
	)
	d1Plaintext := kafkaAuth{authPlaintext, srcHostPlaintext}
	d2Plaintext := kafkaAuth{authPlaintext, srcDockerPlain}

	cases := []destCase{
		// --- D1 sweep: vary source-read auth; D2=plaintext, D3=dest(none). ---
		{"D1=plaintext", kafkaAuth{authPlaintext, "localhost:19092"}, d2Plaintext, restDest},
		{"D1=scram256", kafkaAuth{authScram256, "localhost:19093"}, d2Plaintext, restDest},
		{"D1=scram512", kafkaAuth{authScram512, "localhost:19093"}, d2Plaintext, restDest},
		{"D1=plain", kafkaAuth{authPlain, "localhost:19095"}, d2Plaintext, restDest},
		{"D1=tls", kafkaAuth{authTLS, "localhost:19094"}, d2Plaintext, restDest},
		{"D1=mtls", kafkaAuth{authMTLS, "localhost:19094"}, d2Plaintext, restDest},

		// --- D2 sweep: vary link→source auth; D1=plaintext, D3=dest(none). ---
		{"D2=plaintext", d1Plaintext, kafkaAuth{authPlaintext, "source:29092"}, restDest},
		{"D2=scram256", d1Plaintext, kafkaAuth{authScram256, "source:29094"}, restDest},
		{"D2=scram512", d1Plaintext, kafkaAuth{authScram512, "source:29094"}, restDest},
		{"D2=plain", d1Plaintext, kafkaAuth{authPlain, "source:29096"}, restDest},
		{"D2=tls", d1Plaintext, kafkaAuth{authTLS, "source:29095"}, restDest},
		{"D2=mtls", d1Plaintext, kafkaAuth{authMTLS, "source:29095"}, restDest},

		// --- D3 sweep: vary target REST; D1=plaintext, D2=plaintext. ---
		{"D3=none", d1Plaintext, d2Plaintext, restDest},
		{"D3=basic", d1Plaintext, d2Plaintext, restDestBasic},
		{"D3=mtls", d1Plaintext, d2Plaintext, restDestMTLS},
		{"D3=bearer", d1Plaintext, d2Plaintext, restDestBearer},
	}

	// Source HOST REST ready => source broker up (all D1/D2 test cases dial it).
	newRestClient(t, restSource).waitForClusterID(t)

	// Test cases run serially (not t.Parallel): a parallel destination sweep
	// overlaps with the serial source sweep (Go starts the next top-level test
	// while parked parallel subtests wait), and that concurrent load on the
	// shared source broker re-triggers the source-mode INBOUND-link propagation
	// race. The full matrix is fast enough to run sequentially and
	// deterministically.
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			manifest, linkName := writeDestManifest(t, dir, c)

			poller := newRestClient(t, c.target)
			poller.waitForClusterID(t)
			destID := c.target.clusterID

			// Report capture (no-op + zero extra work when reportEnabled is false).
			// commit() runs via defer so a test case that fails mid-flight still
			// emits what it captured, marked FAIL.
			rep := newDestReporter(c, dir, manifest, linkName, destID)
			defer rep.commit(t, poller)

			// dry-run previews a create and changes nothing.
			out, err := runKCP(t, manifest, "--dry-run")
			rep.dryRun(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "Planned")
			require.Empty(t, poller.linkState(destID, linkName), "dry-run must not create the link")

			// apply creates the link, which reaches ACTIVE.
			out, err = runKCP(t, manifest)
			rep.apply(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "1 created", out)
			poller.requireLinkActive(t, destID, linkName)

			// Capture the live link state before deletion.
			rep.result(poller)

			// re-apply is an idempotent no-op.
			out, err = runKCP(t, manifest)
			rep.reapply(out)
			require.NoError(t, err, out)
			require.Contains(t, out, "1 already present", out)

			poller.deleteLink(t, destID, linkName)
		})
	}
}

// ---------------------------------------------------------------------------
// destination-test-case report capture
// ---------------------------------------------------------------------------

// destChecks maps a destination test case to its one-sentence "what it checks" statement.
func destChecks(c destCase) string {
	switch {
	case strings.HasPrefix(c.name, "D1="):
		return fmt.Sprintf("KCP reads the source cluster id over a %s connection that a real broker accepts (spec.source.credentials); the destination-initiated link still reaches ACTIVE.", c.d1.kind)
	case strings.HasPrefix(c.name, "D2="):
		return fmt.Sprintf("KCP builds a %s link→source connection (spec.clusterLink.sourceCredentials) that a real broker accepts; the destination-initiated link reaches ACTIVE.", c.d2.kind)
	case strings.HasPrefix(c.name, "D3="):
		return fmt.Sprintf("KCP authenticates to the target Kafka REST with %s auth (spec.target.credentials) and creates the link there; the link reaches ACTIVE.", c.target.kind)
	}
	return "destination-initiated cluster link reaches ACTIVE."
}

// destReporter accumulates one destination test case's evidence. All methods are
// cheap no-ops when reportEnabled is false.
type destReporter struct {
	in     sectionInput
	dir    string
	link   string
	destID string
	target restEndpoint
}

func newDestReporter(c destCase, dir, manifest, linkName, destID string) *destReporter {
	r := &destReporter{dir: dir, link: linkName, destID: destID, target: c.target}
	if !reportEnabled {
		return r
	}
	r.in = sectionInput{
		seq:      nextReportSeq(),
		mode:     "destination",
		name:     c.name,
		checks:   destChecks(c),
		manifest: readFileForReport(manifest),
		creds: []fencedFile{
			{"D1 source-read", "source-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "source-creds.yaml"))},
			{"D2 link→source", "link-source-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "link-source-creds.yaml"))},
			{"D3 target REST", "target-creds.yaml", "yaml", readFileForReport(filepath.Join(dir, "target-creds.yaml"))},
		},
		commands: []string{
			"kcp migrate apply -f migration.yaml --dry-run",
			"kcp migrate apply -f migration.yaml",
			"GET " + linkURL(c.target.baseURL, destID, linkName),
		},
		pass: true,
	}
	return r
}

func (r *destReporter) dryRun(out string) {
	if reportEnabled {
		r.in.dryRun = out
	}
}

func (r *destReporter) apply(out string) {
	if reportEnabled {
		r.in.apply = out
	}
}

func (r *destReporter) result(poller restClient) {
	if reportEnabled {
		r.in.results = []resultBlock{{
			label: "link on destination",
			url:   linkURL(r.target.baseURL, r.destID, r.link),
			json:  poller.linkJSON(r.destID, r.link),
		}}
	}
}

func (r *destReporter) reapply(out string) {
	if reportEnabled {
		r.in.reapply = out
	}
}

// commit finalises the section. poller is used for a best-effort live link GET
// when the test case failed (the link never reached ACTIVE, so result() was
// never called) so the failure section still shows the observed link state and
// why.
func (r *destReporter) commit(t *testing.T, poller restClient) {
	if !reportEnabled {
		return
	}
	if t.Failed() {
		r.in.pass = false
		r.captureFailureState(poller)
	}
	collector.add(buildSection(r.in))
}

// captureFailureState does a best-effort live GET of the link so a failed test
// case shows the observed state + link_error. Never panics or fails the commit.
func (r *destReporter) captureFailureState(poller restClient) {
	state, linkErr := poller.link(r.destID, r.link)
	if state == "" {
		r.in.failMsg = fmt.Sprintf("at failure: link %q on %s was not present", r.link, r.destID)
		r.in.results = []resultBlock{{
			label: "link on destination (not found)",
			url:   linkURL(r.target.baseURL, r.destID, r.link),
			json:  fmt.Sprintf("<link %q not present on %s>", r.link, r.destID),
		}}
		return
	}
	r.in.failMsg = fmt.Sprintf("at failure: link %q on %s was in state %q (link_error: %q)", r.link, r.destID, state, linkErr)
	r.in.results = []resultBlock{{
		label: "link on destination",
		url:   linkURL(r.target.baseURL, r.destID, r.link),
		json:  poller.linkJSON(r.destID, r.link),
	}}
}
