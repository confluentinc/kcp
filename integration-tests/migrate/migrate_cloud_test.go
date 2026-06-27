//go:build integration

package migrate

import (
	"encoding/base64"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// cloudConfig is read from the environment; the live cloud suite skips entirely
// when any required value is absent, so `make test-migrate` (docker) is unaffected.
type cloudConfig struct {
	ccRestEndpoint    string
	ccClusterID       string
	ccKey, ccSecret   string
	mskScramBootstrap string
	mskScramUser      string
	mskScramPass      string
	mskIAMBootstrap   string
	mskRegion         string
}

// loadCloudConfig reads cloud creds from env and SKIPS the test when any are absent.
func loadCloudConfig(t *testing.T) cloudConfig {
	t.Helper()
	c := cloudConfig{
		ccRestEndpoint:    os.Getenv("CC_REST_ENDPOINT"),
		ccClusterID:       os.Getenv("CC_CLUSTER_ID"),
		ccKey:             os.Getenv("CC_KEY"),
		ccSecret:          os.Getenv("CC_SECRET"),
		mskScramBootstrap: os.Getenv("MSK_SCRAM_BOOTSTRAP"),
		mskScramUser:      os.Getenv("MSK_SCRAM_USER"),
		mskScramPass:      os.Getenv("MSK_SCRAM_PASS"),
		mskIAMBootstrap:   os.Getenv("MSK_IAM_BOOTSTRAP"),
		mskRegion:         os.Getenv("MSK_REGION"),
	}
	required := map[string]string{
		"CC_REST_ENDPOINT": c.ccRestEndpoint, "CC_CLUSTER_ID": c.ccClusterID,
		"CC_KEY": c.ccKey, "CC_SECRET": c.ccSecret,
		"MSK_SCRAM_BOOTSTRAP": c.mskScramBootstrap, "MSK_SCRAM_USER": c.mskScramUser,
		"MSK_SCRAM_PASS": c.mskScramPass, "MSK_IAM_BOOTSTRAP": c.mskIAMBootstrap,
		"MSK_REGION": c.mskRegion,
	}
	for k, v := range required {
		if v == "" {
			t.Skipf("cloud suite: %s not set; skipping live MSK→CC tests", k)
		}
	}
	return c
}

// ccRestClient builds a restClient for the CC cluster's REST API using API-key
// basic auth. (newRestClient's restBasic path hardcodes admin creds, so build
// the client directly here with the CC key/secret.)
func (c cloudConfig) ccRestClient() restClient {
	return restClient{
		base:   strings.TrimRight(c.ccRestEndpoint, "/"),
		client: http.DefaultClient,
		header: "Basic " + base64.StdEncoding.EncodeToString([]byte(c.ccKey+":"+c.ccSecret)),
	}
}

// splitCSV splits a comma-separated bootstrap string into a broker slice.
func splitCSV(s string) []string { return strings.Split(s, ",") }

// clusterReachable reports whether GET /kafka/v3/clusters/<clusterID> returns 200.
func (c restClient) clusterReachable(clusterID string) bool {
	resp, err := c.do(http.MethodGet, "/kafka/v3/clusters/"+clusterID)
	if err != nil {
		return false
	}
	defer func() { _ = resp.Body.Close() }()
	return resp.StatusCode == http.StatusOK
}

func TestCloud_CCReachable(t *testing.T) {
	cfg := loadCloudConfig(t)
	rc := cfg.ccRestClient()
	require.True(t, rc.clusterReachable(cfg.ccClusterID),
		"GET /kafka/v3/clusters/%s should return 200", cfg.ccClusterID)
}

// ---------------------------------------------------------------------------
// live MSK→CC report wiring (mirrors mirrorReporter from the docker matrix)
// ---------------------------------------------------------------------------

// readFileString reads a file's full content, failing the test on error. Used to
// embed the generated manifest in the evidence report.
func readFileString(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(b)
}

// cloudReporter accumulates a report section for a live cloud case, mirroring
// mirrorReporter. commit() sets pass from t.Failed() and adds it to the collector
// (a no-op when KCP_MATRIX_REPORT is unset, via collector.add's reportEnabled guard).
type cloudReporter struct {
	in sectionInput
}

func newCloudReporter(category, name, checks, manifest string) *cloudReporter {
	return &cloudReporter{in: sectionInput{
		seq:      nextReportSeq(),
		category: category,
		mode:     "destination",
		name:     name,
		checks:   checks,
		manifest: manifest,
		pass:     true,
	}}
}

func (r *cloudReporter) addRun(title, cmd, out string) { r.in.addRun(title, cmd, out) }

func (r *cloudReporter) commit(t *testing.T) {
	t.Helper()
	if t.Failed() {
		r.in.pass = false
	}
	collector.add(buildSection(r.in))
}

// ---------------------------------------------------------------------------
// cloud creds + manifest builders
// ---------------------------------------------------------------------------

// writeCloudCreds writes the CC target (api-key), the MSK SCRAM link creds, and
// the source-read creds into dir; returns their paths. readVia is "scram" or "iam".
func writeCloudCreds(t *testing.T, dir string, cfg cloudConfig, readVia string) (target, linkSource, sourceRead string) {
	t.Helper()
	// Block-style YAML with double-quoted scalars: secrets/passwords can contain
	// YAML-significant characters (e.g. the SCRAM password "Qj8O_v18M%VX?[V&" has a
	// '[' that would start a flow sequence in flow style and break parsing).
	q := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	target = filepath.Join(dir, "cc.yaml")
	require.NoError(t, os.WriteFile(target, []byte("api_key: "+q(cfg.ccKey)+"\napi_secret: "+q(cfg.ccSecret)+"\n"), 0600))
	linkSource = filepath.Join(dir, "msk-scram.yaml")
	require.NoError(t, os.WriteFile(linkSource, []byte(
		"sasl_scram:\n  username: "+q(cfg.mskScramUser)+"\n  password: "+q(cfg.mskScramPass)+"\n  mechanism: SHA512\n"), 0600))
	if readVia == "iam" {
		sourceRead = filepath.Join(dir, "msk-iam.yaml")
		require.NoError(t, os.WriteFile(sourceRead, []byte("iam:\n  region: "+q(cfg.mskRegion)+"\n"), 0600))
	} else {
		sourceRead = linkSource
	}
	return target, linkSource, sourceRead
}

// writeCloudManifest writes a destination-initiated MSK→CC manifest into dir and
// returns its path. sourceBootstrap is the read bootstrap (SCRAM :9196 or IAM :9198);
// the link always dials the SCRAM bootstrap. include!=nil adds a mirror topics block.
func writeCloudManifest(t *testing.T, dir, name string, sourceBootstrap []string, target, linkSource, sourceRead string, cfg cloudConfig, include []string) string {
	t.Helper()
	yamlList := func(ss []string) string { return "[\"" + strings.Join(ss, "\",\"") + "\"]" }
	b := "apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: " + name + "\nspec:\n" +
		"  source:\n    type: msk\n    bootstrapServers: " + yamlList(sourceBootstrap) + "\n    credentials: " + sourceRead + "\n" +
		"  target:\n    type: confluent-cloud\n    clusterId: " + cfg.ccClusterID + "\n    credentials: " + target + "\n    kafka:\n      restEndpoint: " + cfg.ccRestEndpoint + "\n" +
		"  clusterLink:\n    name: " + name + "\n    source:\n      bootstrapServers: " + yamlList(splitCSV(cfg.mskScramBootstrap)) + "\n      credentials: " + linkSource + "\n"
	if include != nil {
		b += "  topics:\n    mode: mirror\n    include: " + yamlList(include) + "\n"
	}
	mf := filepath.Join(dir, name+".yaml")
	require.NoError(t, os.WriteFile(mf, []byte(b), 0600))
	return mf
}

// ---------------------------------------------------------------------------
// Case 1 — destination-initiated cluster link (no mirror, no seeding)
// ---------------------------------------------------------------------------

func TestCloud_MSKtoCC_DestinationLink(t *testing.T) {
	cfg := loadCloudConfig(t)
	rc := cfg.ccRestClient()
	name := uniqueLinkName("msk-cc-link")
	dir := t.TempDir()
	target, linkSource, sourceRead := writeCloudCreds(t, dir, cfg, "scram")
	mf := writeCloudManifest(t, dir, name, splitCSV(cfg.mskScramBootstrap), target, linkSource, sourceRead, cfg, nil)
	defer rc.deleteLink(t, cfg.ccClusterID, name)

	rep := newCloudReporter(catClusterLink, "MSK→CC destination-initiated link", "Destination-initiated external cluster link from public MSK (SCRAM) to CC reaches ACTIVE.", readFileString(t, mf))
	defer rep.commit(t)

	out, err := runKCP(t, mf, "--dry-run")
	require.NoError(t, err, out)
	require.Contains(t, out, "Planned")
	rep.addRun("Dry run", applyDryRunCmd, out)

	out, err = runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "1 created")
	rep.addRun("Apply", applyCmd, out)
	rc.requireLinkActive(t, cfg.ccClusterID, name)

	out, err = runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "already present")
	rep.addRun("Idempotent re-apply", applyCmd, out)

}

// ---------------------------------------------------------------------------
// Case 2 — mirror topics + out-of-band drift (seed catalog + grant ACLs)
// ---------------------------------------------------------------------------

func TestCloud_MSKtoCC_MirrorTopics(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()

	prefix := "kcp-" + runID + "-"
	seeded := seedMSKCatalog(t, admin, prefix)
	grantUserMirrorACLs(t, admin, cfg.mskScramUser, seeded)
	defer cleanupMSK(t, admin, cfg.mskScramUser, seeded)

	rc := cfg.ccRestClient()
	name := uniqueLinkName("msk-cc-mirror")
	dir := t.TempDir()
	target, linkSource, sourceRead := writeCloudCreds(t, dir, cfg, "scram")
	include := []string{prefix + "orders-1", prefix + "products-1"} // no link prefix → mirror name == source name
	mf := writeCloudManifest(t, dir, name, splitCSV(cfg.mskScramBootstrap), target, linkSource, sourceRead, cfg, include)
	defer func() {
		rc.deleteLink(t, cfg.ccClusterID, name)
		for _, mt := range include {
			rc.deleteTopic(t, cfg.ccClusterID, mt)
		}
	}()

	rep := newCloudReporter(catMirror, "MSK→CC mirror topics", "Mirror topics created on CC from public MSK over the SCRAM link; out-of-band pause is reported as drift.", readFileString(t, mf))
	defer rep.commit(t)

	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "mirrorTopics:")
	rep.addRun("Apply (link + mirrors)", applyCmd, out)

	rc.requireLinkActive(t, cfg.ccClusterID, name)
	for _, mt := range include {
		rc.requireMirrorStatus(t, cfg.ccClusterID, name, mt, "ACTIVE")
	}

	// drift: pause a mirror out-of-band, re-apply → drift reported (not mutated).
	rc.pauseMirror(t, cfg.ccClusterID, name, include[0])
	rc.requireMirrorStatus(t, cfg.ccClusterID, name, include[0], "PAUSED")
	out, err = runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "drift")
	rep.addRun("Re-apply after out-of-band pause (drift)", applyCmd, out)

}

// ---------------------------------------------------------------------------
// Case 3 — IAM source-read (read via IAM, link still via SCRAM)
// ---------------------------------------------------------------------------

func TestCloud_MSKtoCC_IAMSourceRead(t *testing.T) {
	cfg := loadCloudConfig(t)
	rc := cfg.ccRestClient()
	name := uniqueLinkName("msk-cc-iam")
	dir := t.TempDir()
	target, linkSource, sourceRead := writeCloudCreds(t, dir, cfg, "iam") // read via IAM; link still SCRAM
	mf := writeCloudManifest(t, dir, name, splitCSV(cfg.mskIAMBootstrap), target, linkSource, sourceRead, cfg, nil)
	defer rc.deleteLink(t, cfg.ccClusterID, name)

	rep := newCloudReporter(catClusterLink, "MSK→CC with IAM source-read", "spec.source reads MSK via IAM (SigV4); the destination-initiated link dials MSK via SCRAM and reaches ACTIVE.", readFileString(t, mf))
	defer rep.commit(t)

	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "1 created")
	rep.addRun("Apply (IAM read, SCRAM link)", applyCmd, out)
	rc.requireLinkActive(t, cfg.ccClusterID, name)

}
