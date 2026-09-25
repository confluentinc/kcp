package execute

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/confluentinc/kcp/internal/types"
)

// sourceClientInputs is everything the static branch's source leg hands the
// shared client code: client.AdminOptionForAuthMethod(authType, authMethod,
// insecure) and client.NewKafkaClient(brokers, region, …).
type sourceClientInputs struct {
	authType   types.AuthType
	authMethod types.AuthMethodConfig
	region     string
	brokers    []string
	insecure   bool
}

// oldStaticSourceInputs is the static branch's current path: flatten the
// manifest credentials into StaticMigrationExecutorOpts (applySourceAuth), then
// rebuild an AuthMethod from the flat fields (sourceClusterAuth).
func oldStaticSourceInputs(bootstrap []string, creds types.MigrateClusterCredentials) sourceClientInputs {
	opts := StaticMigrationExecutorOpts{
		SourceBootstrap:             strings.Join(bootstrap, ","),
		SourceInsecureSkipTLSVerify: creds.InsecureSkipTLSVerify,
	}
	applySourceAuth(&opts, creds)
	return sourceClientInputs{
		authType:   opts.AuthType,
		authMethod: sourceClusterAuth(opts).AuthMethod,
		region:     opts.AWSRegion,
		brokers:    strings.Split(opts.SourceBootstrap, ","),
		insecure:   opts.SourceInsecureSkipTLSVerify,
	}
}

// passThroughSourceInputs is the dynamic branch's path: the manifest
// credentials' own AuthMethod via types.MigrateConn, region from IAM only.
func passThroughSourceInputs(t *testing.T, bootstrap []string, creds types.MigrateClusterCredentials) sourceClientInputs {
	conn := types.MigrateConn(bootstrap, creds)
	authType, err := conn.GetSelectedAuthType()
	require.NoError(t, err)
	region := ""
	if conn.AuthMethod.IAM != nil {
		region = conn.AuthMethod.IAM.Region
	}
	return sourceClientInputs{
		authType:   authType,
		authMethod: conn.AuthMethod,
		region:     region,
		brokers:    conn.BootstrapServers,
		insecure:   conn.InsecureSkipTLSVerify,
	}
}

// TestSourceConn_PassThroughMatchesFlattenAndRebuild guards replacing the
// static branch's flatten-and-rebuild of the source credentials with the
// dynamic branch's pass-through: for every source auth method, both must hand
// the shared client code identical inputs.
func TestSourceConn_PassThroughMatchesFlattenAndRebuild(t *testing.T) {
	const ca = "/etc/certs/source-ca.pem"
	bootstrap := []string{"b-1.example:9096", "b-2.example:9096"}

	cases := map[string]types.MigrateClusterCredentials{
		"iam": {IAM: &types.MigrateIAM{Region: "us-east-1"}},
		"sasl_scram with ca": {SASLScram: &types.MigrateSASLScram{
			Username: "u", Password: "p", Mechanism: "SHA512", CACert: ca}},
		"sasl_scram default mechanism, no ca": {SASLScram: &types.MigrateSASLScram{Username: "u", Password: "p"}},
		"sasl_plain cleartext":                {SASLPlain: &types.MigrateSASLPlain{Username: "u", Password: "p"}},
		"sasl_plain tls, system trust":        {SASLPlain: &types.MigrateSASLPlain{Username: "u", Password: "p", UseTLS: true}},
		"sasl_plain with ca":                  {SASLPlain: &types.MigrateSASLPlain{Username: "u", Password: "p", CACert: ca}},
		"mtls": {MTLS: &types.MigrateMTLS{
			CACert: ca, ClientCert: "c.pem", ClientKey: "k.pem"}},
		"unauthenticated_tls with ca":      {UnauthenticatedTLS: &types.MigrateUnauthenticatedTLS{CACert: ca}},
		"unauthenticated_tls system trust": {UnauthenticatedTLS: &types.MigrateUnauthenticatedTLS{}},
		"unauthenticated_plaintext":        {UnauthenticatedPlaintext: &types.MigrateUnauthenticatedPlaintext{}},
		"sasl_scram insecure_skip_tls_verify": {
			SASLScram: &types.MigrateSASLScram{Username: "u", Password: "p", CACert: ca}, InsecureSkipTLSVerify: true},
	}

	for name, creds := range cases {
		t.Run(name, func(t *testing.T) {
			old := oldStaticSourceInputs(bootstrap, creds)
			pass := passThroughSourceInputs(t, bootstrap, creds)

			// The one known difference: pass-through keeps the IAM region on
			// AuthMethod.IAM.Region as well as passing it to NewKafkaClient.
			// AdminOptionForAuthMethod maps IAM to WithIAMAuth() and never reads
			// that field, so it is inert; region itself is compared below.
			if pass.authMethod.IAM != nil {
				require.NotNil(t, old.authMethod.IAM)
				iam := *pass.authMethod.IAM
				iam.Region = ""
				pass.authMethod.IAM = &iam
			}

			assert.Equal(t, old, pass)
		})
	}
}
