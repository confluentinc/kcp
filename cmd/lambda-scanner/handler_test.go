package main

import (
	"bytes"
	"compress/gzip"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/types"
)

const testArn = "arn:aws:kafka:us-east-1:123456789012:cluster/demo/abc-1"

// newTestHandler returns a handler whose "kcp" is a shell script that records its args and
// environment, and on `discover` writes a small state and credentials file into its cwd.
func newTestHandler(t *testing.T, failStep string) (*handler, string) {
	t.Helper()
	root := t.TempDir()
	rec := filepath.Join(root, "calls.txt")
	envRec := filepath.Join(root, "env.txt")
	script := `#!/bin/sh
echo "$*" >> '` + rec + `'
/usr/bin/env > '` + envRec + `'
echo "2025-01-01 WARN something odd" >> kcp.log
if [ "$1" = "discover" ]; then
  echo '{"schema_version":"x"}' > kcp-state.json
  printf 'regions:\n- name: us-east-1\n  clusters:\n  - name: demo\n    arn: ` + testArn + `\n' > msk-credentials.yaml
fi
if [ "$1 $2" = "` + failStep + `" ]; then echo boom; exit 3; fi
echo "ran $1"
`
	kcp := filepath.Join(root, "kcp")
	require.NoError(t, os.WriteFile(kcp, []byte(script), 0o755))
	tmp := filepath.Join(root, "tmp")
	require.NoError(t, os.Mkdir(tmp, 0o755))
	return &handler{kcpPath: kcp, tmpRoot: tmp, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}, root
}

func readCalls(t *testing.T, root string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(root, "calls.txt"))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	return strings.Split(strings.TrimSpace(string(b)), "\n")
}

func TestEventValidate(t *testing.T) {
	tests := []struct {
		name    string
		ev      event
		want    []string
		wantErr string
	}{
		{"missing arn", event{Region: "us-east-1"}, nil, "cluster_arn and region are required"},
		{"missing region", event{ClusterArn: testArn}, nil, "cluster_arn and region are required"},
		{"unknown step", event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"bogus"}}, nil, "unknown step bogus"},
		{"default all", event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1"}, allSteps, ""},
		{
			"fixed order",
			event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-msk-connectors", "discover"}},
			[]string{"discover", "scan-msk-connectors"}, "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.ev.validate()
			if tt.wantErr != "" {
				require.EqualError(t, err, tt.wantErr)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestHandleValidationError(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), event{Region: "us-east-1"})
	assert.Equal(t, "cluster_arn and region are required", res.Error)
	assert.Empty(t, readCalls(t, root))
}

func TestHandleStepOrderAndOutputs(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps:        []string{"scan-msk-connectors", "scan-clusters", "discover"},
		SessionToken: "tok", SkipMetrics: true,
	})
	require.Empty(t, res.Error)
	require.Len(t, res.Steps, 3)
	assert.Equal(t, []string{"discover", "scan-clusters", "scan-msk-connectors"},
		[]string{res.Steps[0].Step, res.Steps[1].Step, res.Steps[2].Step})

	calls := readCalls(t, root)
	require.Len(t, calls, 3)
	assert.Equal(t, "discover --cluster-arn "+testArn+" --skip-costs --skip-metrics", calls[0])
	assert.Contains(t, calls[2], "scan msk-connectors")
	assert.Contains(t, calls[2], "--metrics-granularity 1d")

	assert.Equal(t, 0, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "ran discover")
	assert.Equal(t, []string{"2025-01-01 WARN something odd"}, res.Steps[0].Warnings)

	assert.JSONEq(t, `{"schema_version":"x"}`, string(res.State))
	assert.Contains(t, res.CredentialsYAML, "us-east-1")

	// Only payload credentials; no function-role env leaks through.
	env, err := os.ReadFile(filepath.Join(root, "env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "AWS_ACCESS_KEY_ID=AKIA")
	assert.Contains(t, string(env), "AWS_EC2_METADATA_DISABLED=true")
	assert.NotContains(t, string(env), "AWS_LAMBDA")

	// Working dir is cleaned up.
	entries, err := os.ReadDir(h.tmpRoot)
	require.NoError(t, err)
	assert.Empty(t, entries)
}

func TestHandleConnectorGranularityNone(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-msk-connectors"},
		State: json.RawMessage(`{}`), ConnectorMetricsGranularity: "none",
	})
	require.Empty(t, res.Error)
	assert.NotContains(t, readCalls(t, root)[0], "--metrics-granularity")
}

func TestHandleStepsSkippedWithoutInputs(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-clusters", "scan-msk-connectors"}})
	require.Len(t, res.Steps, 2)
	for _, s := range res.Steps {
		assert.Equal(t, -1, s.ExitCode)
		assert.Contains(t, s.LogTail, "skipped")
	}
	assert.Empty(t, readCalls(t, root))
}

func TestHandleSelfManagedConnectorsSkippedWithoutURL(t *testing.T) {
	h, root := newTestHandler(t, "")

	// Default steps: silently omitted.
	res := h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", State: json.RawMessage(`{}`)})
	for _, s := range res.Steps {
		assert.NotEqual(t, "scan-self-managed-connectors", s.Step)
	}

	// Explicitly requested: reported as skipped, kcp not invoked.
	res = h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-self-managed-connectors"}})
	require.Len(t, res.Steps, 1)
	assert.Equal(t, -1, res.Steps[0].ExitCode)
	assert.Equal(t, "skipped: no connect_rest_url", res.Steps[0].LogTail)
	for _, c := range readCalls(t, root) {
		assert.NotContains(t, c, "self-managed-connectors")
	}
}

func TestHandleConnectPasswordNotOnArgv(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-self-managed-connectors"}, State: json.RawMessage(`{}`),
		ConnectRestURL: "http://connect:8083",
		ConnectAuth:    connectAuth{Type: "basic", Username: "u", Password: "hunter2"},
	})
	require.Len(t, res.Steps, 1)
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "hunter2")
	assert.NotContains(t, readCalls(t, root)[0], "hunter2")
	assert.Contains(t, readCalls(t, root)[0], "--use-basic-auth --username u")
	// The password reaches kcp only through its environment.
	env, err := os.ReadFile(filepath.Join(root, "env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "PASSWORD=hunter2")
}

func TestHandleNoSecretsInResult(t *testing.T) {
	h, root := newTestHandler(t, "")
	// Fake discover echoes caller-supplied secrets into its output, log and credentials file.
	script := `#!/bin/sh
echo "leak s3cret sekret tok3n" 
echo "2025-01-01 WARN leak s3cret" >> kcp.log
cat > msk-credentials.yaml <<'EOY'
` + sampleCredentials + `EOY
echo '{}' > kcp-state.json
`
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))
	_ = root
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sekret", SessionToken: "tok3n",
		Region: "us-east-1", Steps: []string{"discover"}, CredentialsYAML: sampleCredentials,
	})
	require.Empty(t, res.Error)
	out, err := json.Marshal(res)
	require.NoError(t, err)
	for _, secret := range []string{"s3cret", "sekret", "tok3n"} {
		assert.NotContains(t, string(out), secret)
	}
	assert.NotContains(t, res.CredentialsYAML, "admin")
	assert.Contains(t, res.CredentialsYAML, "sasl_scram")
}

func TestHandleDiscoverFailureSkipsRest(t *testing.T) {
	h, root := newTestHandler(t, "discover --cluster-arn")
	res := h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1"})
	require.Len(t, res.Steps, 3)
	assert.Equal(t, 3, res.Steps[0].ExitCode)
	for _, s := range res.Steps[1:] {
		assert.Equal(t, -1, s.ExitCode)
		assert.Contains(t, s.LogTail, "discover failed")
	}
	assert.Len(t, readCalls(t, root), 1)
}

func TestHandleForceAuthFailureFailsStep(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-clusters"}, State: json.RawMessage(`{}`),
		CredentialsYAML: sampleCredentials, AuthMethod: "tls",
	})
	require.Len(t, res.Steps, 1)
	assert.Equal(t, 1, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "auth_method:")
	assert.Empty(t, readCalls(t, root), "kcp must not run")
}

func TestHandleDeadlineSkipsRemainingAndKeepsState(t *testing.T) {
	h, root := newTestHandler(t, "")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	res := h.handle(ctx, event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-msk-connectors"}, State: json.RawMessage(`{"a":1}`),
	})
	assert.Contains(t, res.Error, "deadline")
	require.Len(t, res.Steps, 1)
	assert.Equal(t, -1, res.Steps[0].ExitCode)
	assert.JSONEq(t, `{"a":1}`, string(res.State))
	assert.Empty(t, readCalls(t, root))
}

func TestHandleCleansStaleDirs(t *testing.T) {
	h, _ := newTestHandler(t, "")
	stale := filepath.Join(h.tmpRoot, "kcp-stale")
	require.NoError(t, os.Mkdir(stale, 0o700))
	h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-msk-connectors"}})
	assert.NoDirExists(t, stale)
}

func TestHandleLogTailCapped(t *testing.T) {
	h, _ := newTestHandler(t, "")
	script := "#!/bin/sh\nawk 'BEGIN{for(i=0;i<20000;i++) print \"xxxxxxxxxxxxxxxxxxxx\"}'\n"
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-msk-connectors"}, State: json.RawMessage(`{}`), LogTailBytes: 10_000_000,
	})
	require.Len(t, res.Steps, 1)
	assert.LessOrEqual(t, len(res.Steps[0].LogTail), maxLogTailBytes+3)
}

func TestValidateRegionAndCredentials(t *testing.T) {
	base := event{ClusterArn: testArn, Region: "us-east-1", AccessKeyID: "AKIA", SecretAccessKey: "sek"}
	_, err := base.validate()
	require.NoError(t, err)

	ev := base
	ev.Region = "eu-west-1"
	_, err = ev.validate()
	require.EqualError(t, err, "region does not match the region in cluster_arn")

	ev = base
	ev.AccessKeyID = ""
	_, err = ev.validate()
	require.ErrorContains(t, err, "aws_access_key_id")

	ev = base
	ev.SecretAccessKey = ""
	_, err = ev.validate()
	require.ErrorContains(t, err, "aws_secret_access_key")
}

func TestHandleValidationErrorIsFatal(t *testing.T) {
	h, _ := newTestHandler(t, "")
	assert.True(t, h.handle(context.Background(), event{Region: "us-east-1"}).fatal)
}

func TestFitResponse(t *testing.T) {
	small := result{Steps: []stepResult{{Step: "discover", LogTail: "ok"}}, State: json.RawMessage(`{}`)}
	assert.Contains(t, string(fitResponse(small)), `"state":{}`)

	big := result{
		Steps:        []stepResult{{Step: "discover", ExitCode: 0, LogTail: "tail"}},
		StateGzipB64: strings.Repeat("a", maxResponseBytes+1),
	}
	var got result
	require.NoError(t, json.Unmarshal(fitResponse(big), &got))
	assert.Contains(t, got.Error, "response too large")
	assert.Empty(t, got.StateGzipB64)
	assert.Empty(t, got.Steps[0].LogTail)
}

func TestHandleNonZeroExit(t *testing.T) {
	h, _ := newTestHandler(t, "discover --cluster-arn")
	res := h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"discover"}})
	require.Len(t, res.Steps, 1)
	assert.Equal(t, 3, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "boom")
}

func TestHandleLargeStateIsGzipped(t *testing.T) {
	h, _ := newTestHandler(t, "")
	big := `{"pad":"` + strings.Repeat("a", rawStateLimit+1) + `"}`
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-msk-connectors"},
		State: json.RawMessage(big),
	})
	require.Empty(t, res.Error)
	assert.Empty(t, res.State)
	assert.Equal(t, len(big), res.StateBytes)

	raw, err := base64.StdEncoding.DecodeString(res.StateGzipB64)
	require.NoError(t, err)
	zr, err := gzip.NewReader(bytes.NewReader(raw))
	require.NoError(t, err)
	got, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, big, string(got))
}

func TestHandleAuthMethodRewritesCredentials(t *testing.T) {
	h, root := newTestHandler(t, "")
	// Fake kcp copies the credentials it was given so the test can inspect them.
	script := `#!/bin/sh
for a in "$@"; do last="$a"; done
cp "$last" '` + filepath.Join(root, "creds-seen.yaml") + `'
`
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))

	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-clusters"},
		State: json.RawMessage(`{}`), CredentialsYAML: sampleCredentials, AuthMethod: "sasl_scram",
	})
	require.Empty(t, res.Error)
	require.Equal(t, 0, res.Steps[0].ExitCode, res.Steps[0].LogTail)

	b, err := os.ReadFile(filepath.Join(root, "creds-seen.yaml"))
	require.NoError(t, err)
	var creds types.Credentials
	require.NoError(t, yaml.Unmarshal(b, &creds))
	am := creds.Regions[0].Clusters[0].AuthMethod
	assert.True(t, am.SASLScram.Use)
	assert.False(t, am.IAM.Use)
	assert.False(t, am.UnauthenticatedPlaintext.Use)
}

func TestHandleAuthMethodErrorIsReported(t *testing.T) {
	h, _ := newTestHandler(t, "")
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", Steps: []string{"scan-clusters"},
		State: json.RawMessage(`{}`), CredentialsYAML: sampleCredentials, AuthMethod: "tls",
	})
	require.Len(t, res.Steps, 1)
	assert.Contains(t, res.Steps[0].LogTail, `auth_method: method "tls" not in credentials file`)
}

func srEvent(regs ...schemaRegistry) event {
	return event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-schema-registry"}, State: json.RawMessage(`{}`), SchemaRegistries: regs,
	}
}

func TestSchemaRegistryConfluentBasicAuth(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), srEvent(
		schemaRegistry{Type: "confluent", URL: "https://sr:8081", Auth: connectAuth{Type: "basic", Username: "u", Password: "srpw123"}, InsecureSkipTLSVerify: true},
		schemaRegistry{Type: "confluent", URL: "https://sr2:8081"},
	))
	require.Empty(t, res.Error)
	require.Len(t, res.Steps, 2)
	assert.Equal(t, "https://sr:8081", res.Steps[0].Target)
	assert.Equal(t, "https://sr2:8081", res.Steps[1].Target)
	calls := readCalls(t, root)
	assert.Equal(t, "scan schema-registry --state-file "+calls[0][strings.Index(calls[0], "--state-file ")+13:strings.Index(calls[0], " --sr-type")]+
		" --sr-type confluent --url https://sr:8081 --use-basic-auth --username u --insecure-skip-tls-verify", calls[0])
	assert.Contains(t, calls[1], "--use-unauthenticated")
	assert.NotContains(t, calls[0], "srpw123")
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "srpw123")
}

func TestSchemaRegistryPasswordEnv(t *testing.T) {
	h, root := newTestHandler(t, "")
	h.handle(context.Background(), srEvent(
		schemaRegistry{Type: "confluent", URL: "https://sr", Auth: connectAuth{Type: "basic", Username: "u", Password: "srpw123"}}))
	env, err := os.ReadFile(filepath.Join(root, "env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "PASSWORD=srpw123")
}

func TestSchemaRegistryGlue(t *testing.T) {
	h, root := newTestHandler(t, "")
	res := h.handle(context.Background(), srEvent(
		schemaRegistry{Type: "glue", RegistryName: "reg1"},
		schemaRegistry{Type: "glue", RegistryName: "reg2", Region: "eu-west-1"},
	))
	require.Len(t, res.Steps, 2)
	calls := readCalls(t, root)
	assert.Contains(t, calls[0], "--sr-type glue --region us-east-1 --registry-name reg1")
	assert.Contains(t, calls[1], "--region eu-west-1 --registry-name reg2")
	assert.Equal(t, "glue:reg1", res.Steps[0].Target)
}

func TestSchemaRegistryOrderLast(t *testing.T) {
	ev := event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-schema-registry", "discover", "scan-self-managed-connectors"}}
	got, err := ev.validate()
	require.NoError(t, err)
	assert.Equal(t, []string{"discover", "scan-self-managed-connectors", "scan-schema-registry"}, got)
}

func TestSchemaRegistryValidation(t *testing.T) {
	tests := []struct {
		name string
		sr   schemaRegistry
		want string
	}{
		{"unknown type", schemaRegistry{Type: "x"}, "unknown type x"},
		{"missing url", schemaRegistry{Type: "confluent"}, "url is required"},
		{"missing registry name", schemaRegistry{Type: "glue"}, "registry_name is required"},
		{"mtls", schemaRegistry{Type: "confluent", URL: "u", UseMTLS: true}, "not supported"},
		{"ca", schemaRegistry{Type: "confluent", URL: "u", TLSCACert: "/x"}, "not supported"},
		{"bad auth", schemaRegistry{Type: "confluent", URL: "u", Auth: connectAuth{Type: "kerberos"}}, "unknown auth type"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := srEvent(tt.sr).validate()
			require.ErrorContains(t, err, tt.want)
			require.ErrorContains(t, err, "schema_registries[0]")
		})
	}
}

func TestSchemaRegistrySkips(t *testing.T) {
	h, root := newTestHandler(t, "")

	// Explicit but empty: skipped with a reason.
	res := h.handle(context.Background(), srEvent())
	require.Len(t, res.Steps, 1)
	assert.Equal(t, -1, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "no schema_registries")

	// Not requested and empty: omitted.
	res = h.handle(context.Background(), event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", State: json.RawMessage(`{}`)})
	for _, s := range res.Steps {
		assert.NotEqual(t, "scan-schema-registry", s.Step)
	}

	// No state: skipped, kcp not run for it.
	before := len(readCalls(t, root))
	ev := srEvent(schemaRegistry{Type: "glue", RegistryName: "r"})
	ev.State = nil
	res = h.handle(context.Background(), ev)
	require.Len(t, res.Steps, 1)
	assert.Equal(t, -1, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "no kcp-state.json")
	assert.Len(t, readCalls(t, root), before)
}

func TestValidateRejectsInvalidCredentialsYAML(t *testing.T) {
	ev := event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		CredentialsYAML: "regions: [\n  password: S3cretPW\n  : : bad"}
	_, err := ev.validate()
	require.EqualError(t, err, "credentials_yaml is not valid YAML")
	assert.NotContains(t, err.Error(), "S3cretPW")
}

func TestValidateRejectsURLUserinfo(t *testing.T) {
	base := event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1"}

	ev := base
	ev.ConnectRestURL = "http://user:pw@connect:8083"
	_, err := ev.validate()
	require.ErrorContains(t, err, "connect_rest_url: must not contain credentials")
	assert.NotContains(t, err.Error(), "pw@")

	ev = base
	ev.SchemaRegistries = []schemaRegistry{{Type: "confluent", URL: "https://user:pw@sr:8081"}}
	_, err = ev.validate()
	require.ErrorContains(t, err, "schema_registries[0]: url: must not contain credentials")

	ev = base
	ev.ConnectRestURL = "http://connect:8083"
	ev.SchemaRegistries = []schemaRegistry{{Type: "confluent", URL: "https://sr:8081"}}
	_, err = ev.validate()
	require.NoError(t, err)
}

func TestHandleScrubsBeforeTruncation(t *testing.T) {
	h, _ := newTestHandler(t, "")
	// The secret straddles the tail cut: unscrubbed truncation would leave a fragment of it.
	script := `#!/bin/sh
echo "S3CRETVALUE0123456789 tail-padding-tail-padding"
echo "2025-01-01 WARN S3CRETVALUE0123456789" >> kcp.log
echo '{}' > kcp-state.json
`
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))
	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "S3CRETVALUE0123456789",
		Region: "us-east-1", Steps: []string{"discover"}, LogTailBytes: 40,
	})
	require.Empty(t, res.Error)
	assert.NotContains(t, res.Steps[0].LogTail, "0123456789")
	assert.NotContains(t, res.Steps[0].Warnings[0], "S3CRETVALUE")
}

func TestValidateReturnsCopyOfAllSteps(t *testing.T) {
	ev := event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1"}
	got, err := ev.validate()
	require.NoError(t, err)
	got[0] = "mutated"
	assert.Equal(t, stepDiscover, allSteps[0])
}

func TestResultErrorsAreJoined(t *testing.T) {
	var r result
	r.addError("a")
	r.addError("b")
	assert.Equal(t, "a; b", r.Error)
}

func TestFitResponseDropsLogsBeforeState(t *testing.T) {
	res := result{
		Steps: []stepResult{{Step: "discover", LogTail: strings.Repeat("x", maxResponseBytes), Warnings: []string{"w"}}},
		State: json.RawMessage(`{"a":1}`),
		Error: "earlier",
	}
	var got result
	require.NoError(t, json.Unmarshal(fitResponse(res), &got))
	assert.JSONEq(t, `{"a":1}`, string(got.State))
	assert.Empty(t, got.Steps[0].LogTail)
	assert.Empty(t, got.Steps[0].Warnings)
	assert.True(t, strings.HasPrefix(got.Error, "earlier; response too large"), got.Error)
	assert.Contains(t, got.Error, "logs dropped")
}

func TestFitResponseMarshalError(t *testing.T) {
	var got result
	require.NoError(t, json.Unmarshal(fitResponse(result{State: json.RawMessage(`{bad`)}), &got))
	assert.Contains(t, got.Error, "failed to marshal response")
	assert.NotContains(t, got.Error, "too large")
}

func TestValidateRejectsSchemelessURLUserinfo(t *testing.T) {
	base := event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1"}

	ev := base
	ev.ConnectRestURL = "alice:pw@connect:8083"
	_, err := ev.validate()
	require.ErrorContains(t, err, "connect_rest_url: must not contain credentials")
	assert.NotContains(t, err.Error(), "pw")

	ev = base
	ev.SchemaRegistries = []schemaRegistry{{Type: "confluent", URL: "alice:pw@sr:8081"}}
	_, err = ev.validate()
	require.ErrorContains(t, err, "schema_registries[0]: url: must not contain credentials")

	ev = base
	ev.ConnectRestURL = "connect:8083"
	_, err = ev.validate()
	require.NoError(t, err)

	ev = base
	ev.ConnectRestURL = "http://bad host:pw@x/%zz"
	_, err = ev.validate()
	require.ErrorContains(t, err, "not a valid URL")
	assert.NotContains(t, err.Error(), "pw")
}

func TestHandleDeadlineInterruptedStepIsFailure(t *testing.T) {
	h, _ := newTestHandler(t, "")
	require.NoError(t, os.WriteFile(h.kcpPath, []byte("#!/bin/sh\nexec sleep 30\n"), 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	res := h.handle(ctx, event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-msk-connectors"}, State: json.RawMessage(`{}`),
	})
	require.Len(t, res.Steps, 1)
	assert.NotEqual(t, -1, res.Steps[0].ExitCode)
	assert.NotEqual(t, 0, res.Steps[0].ExitCode)
	assert.Contains(t, res.Error, "deadline")
}

func TestHandleScanClustersOnlySeesRequestedCluster(t *testing.T) {
	h, root := newTestHandler(t, "")
	script := `#!/bin/sh
for a in "$@"; do last="$a"; done
cp "$last" '` + filepath.Join(root, "creds-seen.yaml") + `'
`
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))
	other := "arn:aws:kafka:us-east-1:123456789012:cluster/other/def-2"
	creds := &types.Credentials{Regions: []types.RegionAuth{{Name: "us-east-1", Clusters: []types.ClusterAuth{
		{Name: "other", Arn: other}, {Name: "demo", Arn: testArn},
	}}}}
	y, err := creds.ToYaml()
	require.NoError(t, err)

	res := h.handle(context.Background(), event{
		ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1",
		Steps: []string{"scan-clusters", "discover"}, State: json.RawMessage(`{}`), CredentialsYAML: string(y),
	})
	require.Equal(t, 0, res.Steps[len(res.Steps)-1].ExitCode, res.Error)

	var seen types.Credentials
	b, err := os.ReadFile(filepath.Join(root, "creds-seen.yaml"))
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(b, &seen))
	require.Len(t, seen.Regions, 1)
	require.Len(t, seen.Regions[0].Clusters, 1)
	assert.Equal(t, testArn, seen.Regions[0].Clusters[0].Arn)
}

func TestScrubberLongestFirst(t *testing.T) {
	var sc scrubber
	sc.add("shared")
	sc.add("shared-registry-password")
	sc.add("shared")
	sc.add("")
	out := sc.apply("x shared-registry-password y shared z")
	assert.NotContains(t, out, "registry")
	assert.NotContains(t, out, "password")
	assert.NotContains(t, out, "shared")
}

func TestValidateAuthMethodAndGranularity(t *testing.T) {
	ev := baseEvent()
	ev.AuthMethod = "sasl_scarm"
	_, err := ev.validate()
	require.ErrorContains(t, err, "unknown auth_method")

	ev = baseEvent()
	ev.ConnectorMetricsGranularity = "2h"
	_, err = ev.validate()
	require.ErrorContains(t, err, "connector_metrics_granularity")

	for _, g := range []string{"", "none", "60s", "5m", "1h", "1d"} {
		ev = baseEvent()
		ev.ConnectorMetricsGranularity = g
		_, err = ev.validate()
		require.NoError(t, err, g)
	}
	for _, m := range authMethods {
		ev = baseEvent()
		ev.AuthMethod = m
		_, err = ev.validate()
		require.NoError(t, err, m)
	}
}

func TestStateGzipB64RoundTrip(t *testing.T) {
	h, _ := newTestHandler(t, "")
	gz, err := gzipBase64([]byte(`{"a":1}`))
	require.NoError(t, err)
	ev := baseEvent()
	ev.State, ev.StateGzipB64, ev.Steps = nil, gz, []string{"scan-msk-connectors"}
	res := h.handle(context.Background(), ev)
	require.Empty(t, res.Error)
	assert.JSONEq(t, `{"a":1}`, string(res.State))

	// mutually exclusive with state; bad input is rejected
	ev.State = json.RawMessage(`{}`)
	assert.True(t, h.handle(context.Background(), ev).fatal)
	ev.State, ev.StateGzipB64 = nil, "!!!"
	assert.True(t, h.handle(context.Background(), ev).fatal)
}

func TestWarnLinesResetsOnRotation(t *testing.T) {
	p := filepath.Join(t.TempDir(), "kcp.log")
	require.NoError(t, os.WriteFile(p, []byte("2025 WARN fresh\n"), 0o600))
	got := warnLines(p, 1<<20, 10, scrubber{})
	require.Len(t, got, 1)
	assert.Contains(t, got[0], "fresh")
}

func TestSchemaRegistrySkipsRemainingAfterDeadline(t *testing.T) {
	h, root := newTestHandler(t, "")
	script := "#!/bin/sh\necho \"$*\" >> '" + filepath.Join(root, "calls.txt") + "'\nexec sleep 10\n"
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	ev := srEvent(
		schemaRegistry{Type: "confluent", URL: "https://sr1:8081"},
		schemaRegistry{Type: "confluent", URL: "https://sr2:8081"},
		schemaRegistry{Type: "glue", RegistryName: "g"},
	)
	res := h.handle(ctx, ev)
	require.Len(t, res.Steps, 3)
	assert.Equal(t, 1, res.Steps[0].ExitCode)
	assert.Equal(t, -1, res.Steps[1].ExitCode)
	assert.Equal(t, "https://sr2:8081", res.Steps[1].Target)
	assert.Equal(t, -1, res.Steps[2].ExitCode)
	assert.Len(t, readCalls(t, root), 1)
	assert.Equal(t, 1, strings.Count(res.Error, "deadline reached during"))
}
