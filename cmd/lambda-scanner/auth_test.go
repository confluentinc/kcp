package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/types"
)

// sampleCredentials mirrors the msk-credentials.yaml written by `kcp discover`.
const sampleCredentials = `regions:
- name: us-east-1
  clusters:
  - name: demo
    arn: arn:aws:kafka:us-east-1:123456789012:cluster/demo/abc-1
    auth_method:
      iam:
        use: true
      sasl_scram:
        use: false
        username: admin
        password: s3cret
        mechanism: SHA512
      unauthenticated_plaintext:
        use: true
  - name: other
    arn: arn:aws:kafka:us-east-1:123456789012:cluster/other/def-2
    auth_method:
      unauthenticated_plaintext:
        use: true
`

func writeCreds(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "msk-credentials.yaml")
	require.NoError(t, os.WriteFile(p, []byte(content), 0o600))
	return p
}

func loadCreds(t *testing.T, p string) types.Credentials {
	t.Helper()
	b, err := os.ReadFile(p)
	require.NoError(t, err)
	var c types.Credentials
	require.NoError(t, yaml.Unmarshal(b, &c))
	return c
}

func TestForceAuth(t *testing.T) {
	p := writeCreds(t, sampleCredentials)
	require.NoError(t, forceAuth(p, testArn, "sasl_scram"))

	c := loadCreds(t, p)
	am := c.Regions[0].Clusters[0].AuthMethod
	assert.True(t, am.SASLScram.Use)
	assert.False(t, am.IAM.Use)
	assert.False(t, am.UnauthenticatedPlaintext.Use)
	// Other settings survive the round trip.
	assert.Equal(t, "admin", am.SASLScram.Username)
	assert.Equal(t, "s3cret", am.SASLScram.Password)
	assert.Equal(t, "SHA512", am.SASLScram.Mechanism)

	// Other clusters are untouched.
	other := c.Regions[0].Clusters[1].AuthMethod
	assert.True(t, other.UnauthenticatedPlaintext.Use)

	selected, err := c.Regions[0].Clusters[0].GetSelectedAuthType()
	require.NoError(t, err)
	assert.Equal(t, types.AuthTypeSASLSCRAM, selected)
}

func TestForceAuthErrors(t *testing.T) {
	tests := []struct {
		name, arn, method, wantErr string
	}{
		{"method absent", testArn, "tls", `method "tls" not in credentials file`},
		{"cluster absent", "arn:nope", "iam", `cluster "arn:nope" not found`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := writeCreds(t, sampleCredentials)
			err := forceAuth(p, tt.arn, tt.method)
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
			// File unchanged on error.
			b, _ := os.ReadFile(p)
			assert.Equal(t, sampleCredentials, string(b))
		})
	}
	t.Run("missing file", func(t *testing.T) {
		require.Error(t, forceAuth(filepath.Join(t.TempDir(), "none.yaml"), testArn, "iam"))
	})
}

func TestRedactCredentials(t *testing.T) {
	out, err := redactCredentials([]byte(sampleCredentials))
	require.NoError(t, err)
	assert.NotContains(t, string(out), "s3cret")
	assert.NotContains(t, string(out), "admin")
	assert.Contains(t, string(out), "SHA512")
	assert.Equal(t, []string{"s3cret"}, credentialSecrets([]byte(sampleCredentials)))
}

func TestYAMLErrorsDoNotLeakSecrets(t *testing.T) {
	bad := "regions:\n- name: x\n  clusters: [\n    password: S3cretPW\n"
	_, err := redactCredentials([]byte(bad))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "S3cretPW")

	err = forceAuth(writeCreds(t, bad), testArn, "iam")
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "S3cretPW")
}

func TestCredentialSecretsIncludeTLSKey(t *testing.T) {
	data := "regions:\n- name: r\n  clusters:\n  - name: c\n    arn: a\n    auth_method:\n" +
		"      tls:\n        use: true\n        client_key: /tmp/KEYPATH\n" +
		"      sasl_plain:\n        use: false\n        username: plainuser\n        password: plainpw\n"
	got := credentialSecrets([]byte(data))
	assert.ElementsMatch(t, []string{"/tmp/KEYPATH", "plainpw"}, got)
}
