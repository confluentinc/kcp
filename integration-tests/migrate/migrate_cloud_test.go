//go:build integration

package migrate

import (
	"encoding/base64"
	"net/http"
	"os"
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
