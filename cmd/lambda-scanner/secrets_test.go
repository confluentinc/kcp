package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aws/aws-sdk-go-v2/service/secretsmanager"
	"github.com/goccy/go-yaml"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/types"
)

const (
	saslSecretArn    = "arn:aws:secretsmanager:us-east-1:123456789012:secret:AmazonMSK_demo-AbCdEf"
	connectSecretArn = "arn:aws:secretsmanager:us-east-1:123456789012:secret:connect-AbCdEf"
	srSecretArn      = "arn:aws:secretsmanager:eu-west-1:123456789012:secret:sr-AbCdEf"
)

// fakeSecrets serves secrets by ARN and counts reads.
type fakeSecrets struct {
	values map[string]string
	errs   map[string]error
	calls  map[string]int
	mu     sync.Mutex
}

func (f *fakeSecrets) GetSecretValue(_ context.Context, in *secretsmanager.GetSecretValueInput, _ ...func(*secretsmanager.Options)) (*secretsmanager.GetSecretValueOutput, error) {
	f.mu.Lock()
	f.calls[*in.SecretId]++
	f.mu.Unlock()
	if err := f.errs[*in.SecretId]; err != nil {
		return nil, err
	}
	v := f.values[*in.SecretId]
	return &secretsmanager.GetSecretValueOutput{SecretString: &v}, nil
}

func useFakeSecrets(h *handler, values map[string]string, errs map[string]error) *fakeSecrets {
	f := &fakeSecrets{values: values, errs: errs, calls: map[string]int{}}
	h.secretsClient = func(context.Context, string) (secretsAPI, error) { return f, nil }
	return f
}

// credsEchoScript makes the fake kcp copy the credentials file it is given and print its own
// environment and the file, so the test can check that secret values are scrubbed from the output.
func credsEchoScript(t *testing.T, h *handler, root string) {
	t.Helper()
	script := `#!/bin/sh
echo "$*" >> '` + filepath.Join(root, "calls.txt") + `'
for a in "$@"; do last="$a"; done
if [ "$1 $2" = "scan clusters" ]; then cp "$last" '` + filepath.Join(root, "creds-seen.yaml") + `'; cat "$last"; fi
/usr/bin/env > '` + filepath.Join(root, "env.txt") + `'
echo "PASSWORD is $PASSWORD"
echo "USERNAME is $USERNAME"
`
	require.NoError(t, os.WriteFile(h.kcpPath, []byte(script), 0o755))
}

func baseEvent() event {
	return event{ClusterArn: testArn, AccessKeyID: "AKIA", SecretAccessKey: "sek", Region: "us-east-1", State: json.RawMessage(`{}`)}
}

func TestSASLSecretArnSuccess(t *testing.T) {
	h, root := newTestHandler(t, "")
	credsEchoScript(t, h, root)
	f := useFakeSecrets(h, map[string]string{saslSecretArn: `{"username":"scanner","password":"pw-from-sm"}`}, nil)

	ev := baseEvent()
	ev.Steps, ev.CredentialsYAML, ev.SASLSecretArn = []string{"scan-clusters"}, sampleCredentials, saslSecretArn
	res := h.handle(context.Background(), ev)
	require.Empty(t, res.Error)
	require.Equal(t, 0, res.Steps[0].ExitCode, res.Steps[0].LogTail)
	assert.Equal(t, 1, f.calls[saslSecretArn])

	b, err := os.ReadFile(filepath.Join(root, "creds-seen.yaml"))
	require.NoError(t, err)
	var creds types.Credentials
	require.NoError(t, yaml.Unmarshal(b, &creds))
	am := creds.Regions[0].Clusters[0].AuthMethod
	assert.True(t, am.SASLScram.Use) // auth_method empty: forced to sasl_scram
	assert.False(t, am.IAM.Use)
	assert.Equal(t, "scanner", am.SASLScram.Username)
	assert.Equal(t, "pw-from-sm", am.SASLScram.Password)

	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "pw-from-sm")
	assert.Contains(t, res.Steps[0].LogTail, redacted) // the echoed file was scrubbed
}

func TestSASLSecretArnPlain(t *testing.T) {
	h, root := newTestHandler(t, "")
	credsEchoScript(t, h, root)
	useFakeSecrets(h, map[string]string{saslSecretArn: `{"username":"scanner","password":"pw-from-sm"}`}, nil)

	creds := `regions:
- name: us-east-1
  clusters:
  - name: demo
    arn: ` + testArn + `
    auth_method:
      sasl_scram:
        use: true
        username: x
        password: y
`
	ev := baseEvent()
	ev.Steps, ev.CredentialsYAML, ev.SASLSecretArn, ev.AuthMethod = []string{"scan-clusters"}, creds, saslSecretArn, "sasl_plain"
	res := h.handle(context.Background(), ev)
	require.Equal(t, 0, res.Steps[0].ExitCode, res.Steps[0].LogTail)

	b, err := os.ReadFile(filepath.Join(root, "creds-seen.yaml"))
	require.NoError(t, err)
	var got types.Credentials
	require.NoError(t, yaml.Unmarshal(b, &got))
	am := got.Regions[0].Clusters[0].AuthMethod
	assert.True(t, am.SASLPlain.Use)
	assert.False(t, am.SASLScram.Use)
	assert.Equal(t, "pw-from-sm", am.SASLPlain.Password)
}

func TestSASLSecretArnDiscoverResultHasNoPassword(t *testing.T) {
	h, _ := newTestHandler(t, "")
	useFakeSecrets(h, map[string]string{saslSecretArn: `{"username":"scanner","password":"pw-from-sm"}`}, nil)
	ev := baseEvent()
	ev.SASLSecretArn = saslSecretArn
	res := h.handle(context.Background(), ev)
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "pw-from-sm")
}

func TestSecretReadFailureFailsStepOnly(t *testing.T) {
	h, root := newTestHandler(t, "")
	useFakeSecrets(h, nil, map[string]error{saslSecretArn: errors.New("AccessDeniedException: not allowed")})

	ev := baseEvent()
	ev.Steps, ev.CredentialsYAML, ev.SASLSecretArn = []string{"scan-clusters", "scan-msk-connectors"}, sampleCredentials, saslSecretArn
	res := h.handle(context.Background(), ev)
	require.Len(t, res.Steps, 2)
	assert.Empty(t, res.Error)
	assert.Equal(t, 1, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "sasl_secret_arn: failed to read secret "+saslSecretArn)
	assert.Contains(t, res.Steps[0].LogTail, "AccessDeniedException")
	assert.Equal(t, 0, res.Steps[1].ExitCode)
	require.Len(t, readCalls(t, root), 1) // kcp ran only for the second step
	assert.Contains(t, readCalls(t, root)[0], "scan msk-connectors")
}

func TestSecretJSONMissingFields(t *testing.T) {
	for name, body := range map[string]string{
		"no password": `{"username":"u"}`,
		"no username": `{"password":"leaked-pw"}`,
		"not json":    `leaked-pw`,
	} {
		t.Run(name, func(t *testing.T) {
			h, _ := newTestHandler(t, "")
			useFakeSecrets(h, map[string]string{saslSecretArn: body}, nil)
			ev := baseEvent()
			ev.Steps, ev.CredentialsYAML, ev.SASLSecretArn = []string{"scan-clusters"}, sampleCredentials, saslSecretArn
			res := h.handle(context.Background(), ev)
			require.Equal(t, 1, res.Steps[0].ExitCode)
			assert.Contains(t, res.Steps[0].LogTail, saslSecretArn)
			assert.NotContains(t, res.Steps[0].LogTail, "leaked-pw")
		})
	}
}

func TestConnectAndSRSecretArn(t *testing.T) {
	h, root := newTestHandler(t, "")
	credsEchoScript(t, h, root)
	f := useFakeSecrets(h, map[string]string{
		connectSecretArn: `{"username":"cu","password":"connect-pw"}`,
		srSecretArn:      `{"username":"su","password":"sr-pw"}`,
	}, nil)

	ev := baseEvent()
	ev.Steps = []string{"scan-self-managed-connectors", "scan-schema-registry"}
	ev.ConnectRestURL = "http://connect:8083"
	ev.ConnectAuth = connectAuth{Type: "basic", SecretArn: connectSecretArn}
	ev.SchemaRegistries = []schemaRegistry{
		{Type: "confluent", URL: "https://sr1:8081", Auth: connectAuth{Type: "basic", SecretArn: srSecretArn}},
		{Type: "confluent", URL: "https://sr2:8081", Auth: connectAuth{Type: "basic", SecretArn: srSecretArn}},
	}
	res := h.handle(context.Background(), ev)
	require.Len(t, res.Steps, 3)
	for _, s := range res.Steps {
		assert.Equal(t, 0, s.ExitCode, s.LogTail)
	}
	assert.Equal(t, 1, f.calls[srSecretArn]) // read once per invoke
	assert.Equal(t, 1, f.calls[connectSecretArn])

	calls := readCalls(t, root)
	// Secret-sourced usernames go via env (kcp binds --username to USERNAME), not argv.
	assert.NotContains(t, calls[0], "--username")
	assert.NotContains(t, calls[1], "--username")
	out, err := json.Marshal(res)
	require.NoError(t, err)
	for _, v := range []string{"connect-pw", "sr-pw"} {
		assert.NotContains(t, string(out), v)
	}
	assert.Contains(t, res.Steps[0].LogTail, "PASSWORD is ***")
	assert.Contains(t, res.Steps[0].LogTail, "USERNAME is ***")
	assert.NotContains(t, res.Steps[0].LogTail, "cu")
}

func TestConnectSecretReadFailure(t *testing.T) {
	h, _ := newTestHandler(t, "")
	useFakeSecrets(h, nil, map[string]error{connectSecretArn: errors.New("ResourceNotFoundException: gone")})
	ev := baseEvent()
	ev.Steps, ev.ConnectRestURL = []string{"scan-self-managed-connectors"}, "http://connect:8083"
	ev.ConnectAuth = connectAuth{Type: "basic", SecretArn: connectSecretArn}
	res := h.handle(context.Background(), ev)
	require.Len(t, res.Steps, 1)
	assert.Equal(t, 1, res.Steps[0].ExitCode)
	assert.Contains(t, res.Steps[0].LogTail, "ResourceNotFoundException")
}

func TestSecretArnValidation(t *testing.T) {
	tests := []struct {
		name    string
		mod     func(*event)
		wantErr string
	}{
		{"connect password and secret_arn", func(e *event) {
			e.ConnectAuth = connectAuth{Type: "basic", Password: "p", SecretArn: connectSecretArn}
		}, "connect_auth: secret_arn and password are mutually exclusive"},
		{"sr password and secret_arn", func(e *event) {
			e.SchemaRegistries = []schemaRegistry{{Type: "confluent", URL: "https://sr", Auth: connectAuth{Type: "basic", Password: "p", SecretArn: srSecretArn}}}
		}, "schema_registries[0]: auth: secret_arn and password are mutually exclusive"},
		{"connect bad arn", func(e *event) {
			e.ConnectAuth = connectAuth{Type: "basic", SecretArn: "arn:aws:s3:::bucket"}
		}, "connect_auth: secret_arn: secret ARN must look like"},
		{"sr bad arn", func(e *event) {
			e.SchemaRegistries = []schemaRegistry{{Type: "confluent", URL: "https://sr", Auth: connectAuth{Type: "basic", SecretArn: "not-an-arn"}}}
		}, "schema_registries[0]: auth: secret_arn: secret ARN must look like"},
		{"connect secret_arn without basic", func(e *event) {
			e.ConnectAuth = connectAuth{SecretArn: connectSecretArn}
		}, "secret_arn requires type basic"},
		{"sasl bad arn", func(e *event) { e.SASLSecretArn = "arn:aws:secretsmanager:us-east-1::secret:x" }, "sasl_secret_arn: secret ARN must look like"},
		{"sasl with iam", func(e *event) { e.SASLSecretArn, e.AuthMethod = saslSecretArn, "iam" }, "sasl_secret_arn requires auth_method"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := baseEvent()
			tt.mod(&ev)
			_, err := ev.validate()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantErr)
		})
	}
}

func TestSecretUsernameNotInResult(t *testing.T) {
	h, root := newTestHandler(t, "")
	credsEchoScript(t, h, root)
	useFakeSecrets(h, map[string]string{connectSecretArn: `{"username":"uniq-user-77","password":"uniq-pw-88"}`}, nil)
	ev := baseEvent()
	ev.Steps = []string{"scan-self-managed-connectors"}
	ev.ConnectRestURL = "http://connect:8083"
	ev.ConnectAuth = connectAuth{Type: "basic", SecretArn: connectSecretArn}
	res := h.handle(context.Background(), ev)
	require.Equal(t, 0, res.Steps[0].ExitCode, res.Steps[0].LogTail)
	out, err := json.Marshal(res)
	require.NoError(t, err)
	assert.NotContains(t, string(out), "uniq-user-77")
	assert.NotContains(t, string(out), "uniq-pw-88")
	env, err := os.ReadFile(filepath.Join(root, "env.txt"))
	require.NoError(t, err)
	assert.Contains(t, string(env), "USERNAME=uniq-user-77")
}

func TestSASLSecretDoesNotChangeReturnedCredentials(t *testing.T) {
	for _, method := range []string{"", "sasl_plain"} {
		h, _ := newTestHandler(t, "")
		useFakeSecrets(h, map[string]string{saslSecretArn: `{"username":"scanner","password":"pw-from-sm"}`}, nil)
		ev := baseEvent()
		ev.SASLSecretArn, ev.AuthMethod = saslSecretArn, method
		res := h.handle(context.Background(), ev)
		require.Equal(t, 0, res.Steps[1].ExitCode, res.Steps[1].LogTail)
		var got types.Credentials
		require.NoError(t, yaml.Unmarshal([]byte(res.CredentialsYAML), &got))
		am := got.Regions[0].Clusters[0].AuthMethod
		assert.Nil(t, am.SASLScram, method)
		assert.Nil(t, am.SASLPlain, method)
	}
}

func TestReadSecretsClientPerRegion(t *testing.T) {
	h := &handler{}
	f := &fakeSecrets{
		values: map[string]string{
			saslSecretArn: `{"username":"a","password":"b"}`,
			srSecretArn:   `{"username":"c","password":"d"}`,
			"arn:aws:secretsmanager:eu-west-1:123456789012:secret:other": `{"username":"e","password":"f"}`,
		},
		calls: map[string]int{},
	}
	var mu sync.Mutex
	made := map[string]int{}
	h.secretsClient = func(_ context.Context, region string) (secretsAPI, error) {
		mu.Lock()
		defer mu.Unlock()
		made[region]++
		return f, nil
	}
	out := h.readSecrets(context.Background(), []string{saslSecretArn, srSecretArn, "arn:aws:secretsmanager:eu-west-1:123456789012:secret:other"})
	assert.Equal(t, map[string]int{"us-east-1": 1, "eu-west-1": 1}, made)
	for _, sc := range out {
		assert.NoError(t, sc.err)
	}
	assert.Len(t, out, 3)
}
