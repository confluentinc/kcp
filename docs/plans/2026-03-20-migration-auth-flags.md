# Migration Auth Flags Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Replace `--credentials-file` on migration execute with direct auth flags (`--use-sasl-iam`, `--use-sasl-scram`, etc.) on both init and execute, removing the dependency on running `kcp discover` first.

**Architecture:** Auth flags are duplicated on both commands. Init stores the auth type string in `MigrationConfig.AuthMode` but doesn't connect to the source cluster. Execute uses the flags to determine auth type, calls MSK API with the ARN to discover bootstrap brokers, picks the right brokers for the auth type, and connects.

**Tech Stack:** Go, cobra, pflag, sarama (Kafka client), AWS MSK SDK

**Design doc:** `docs/plans/2026-03-20-migration-auth-flags-design.md`

---

### Task 1: Add auth flags to migration init command

**Files:**
- Modify: `cmd/migration/init/cmd_migration_init.go`

**Step 1: Add auth flag variables**

Add these variables to the existing `var` block (after `switchoverCrYamlPath`):

```go
	useSaslIam                  bool
	useSaslScram                bool
	useTls                      bool
	useUnauthenticatedTLS       bool
	useUnauthenticatedPlaintext bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
```

**Step 2: Add flag set groups in `NewMigrationInitCmd`**

After the `optionalFlags` block and before `migrationInitCmd.SetUsageFunc`, add three new flag groups:

```go
	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useSaslIam, "use-sasl-iam", false, "Use IAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useSaslScram, "use-sasl-scram", false, "Use SASL/SCRAM authentication for the source MSK cluster.")
	authFlags.BoolVar(&useTls, "use-tls", false, "Use TLS authentication for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedTLS, "use-unauthenticated-tls", false, "Use unauthenticated (TLS encryption) for the source MSK cluster.")
	authFlags.BoolVar(&useUnauthenticatedPlaintext, "use-unauthenticated-plaintext", false, "Use unauthenticated (plaintext) for the source MSK cluster.")
	migrationInitCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Source Cluster Authentication Flags"

	// SASL/SCRAM credential flags.
	saslScramFlags := pflag.NewFlagSet("sasl-scram", pflag.ExitOnError)
	saslScramFlags.SortFlags = false
	saslScramFlags.StringVar(&saslScramUsername, "sasl-scram-username", "", "SASL/SCRAM username for the source MSK cluster.")
	saslScramFlags.StringVar(&saslScramPassword, "sasl-scram-password", "", "SASL/SCRAM password for the source MSK cluster.")
	migrationInitCmd.Flags().AddFlagSet(saslScramFlags)
	groups[saslScramFlags] = "SASL/SCRAM Flags"

	// TLS credential flags.
	tlsFlags := pflag.NewFlagSet("tls", pflag.ExitOnError)
	tlsFlags.SortFlags = false
	tlsFlags.StringVar(&tlsCaCert, "tls-ca-cert", "", "Path to the TLS CA certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientCert, "tls-client-cert", "", "Path to the TLS client certificate for the source MSK cluster.")
	tlsFlags.StringVar(&tlsClientKey, "tls-client-key", "", "Path to the TLS client key for the source MSK cluster.")
	migrationInitCmd.Flags().AddFlagSet(tlsFlags)
	groups[tlsFlags] = "TLS Flags"
```

**Step 3: Add mutual exclusivity**

After the existing `MarkFlagRequired` calls, add:

```go
	migrationInitCmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")
```

**Step 4: Update `SetUsageFunc` to include new flag groups**

Update the `flagOrder` and `groupNames` slices to include the three new groups:

```go
	flagOrder := []*pflag.FlagSet{requiredFlags, optionalFlags, authFlags, saslScramFlags, tlsFlags}
	groupNames := []string{"Required Flags", "Optional Flags", "Source Cluster Authentication Flags", "SASL/SCRAM Flags", "TLS Flags"}
```

**Step 5: Update `preRunMigrationInit` for conditional requirements**

Add conditional flag requirements after `BindEnvToFlags`:

```go
func preRunMigrationInit(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslScram {
		cmd.MarkFlagRequired("sasl-scram-username")
		cmd.MarkFlagRequired("sasl-scram-password")
	}

	if useTls {
		cmd.MarkFlagRequired("tls-ca-cert")
		cmd.MarkFlagRequired("tls-client-cert")
		cmd.MarkFlagRequired("tls-client-key")
	}

	return nil
}
```

**Step 6: Map auth flags to `config.AuthMode` in `runMigrationInit`**

Add a helper function and call it when building `config`:

```go
func resolveAuthMode() string {
	switch {
	case useSaslIam:
		return string(types.AuthTypeIAM)
	case useSaslScram:
		return string(types.AuthTypeSASLSCRAM)
	case useTls:
		return string(types.AuthTypeTLS)
	case useUnauthenticatedTLS:
		return string(types.AuthTypeUnauthenticatedTLS)
	case useUnauthenticatedPlaintext:
		return string(types.AuthTypeUnauthenticatedPlaintext)
	default:
		return ""
	}
}
```

In `runMigrationInit`, set `AuthMode` on the config struct:

```go
	config := &types.MigrationConfig{
		// ... existing fields ...
		AuthMode:        resolveAuthMode(),
	}
```

**Step 7: Build and verify**

Run: `go build ./...`
Expected: builds successfully

**Step 8: Commit**

```
git add cmd/migration/init/cmd_migration_init.go
git commit -m "feat: add auth flags to migration init command"
```

---

### Task 2: Add auth flags to migration execute command

**Files:**
- Modify: `cmd/migration/execute/cmd_migration_execute.go`

**Step 1: Update var block**

Replace the existing var block with:

```go
var (
	migrationStateFile string
	migrationId        string
	lagThreshold       int64
	clusterApiKey      string
	clusterApiSecret   string
	ccBootstrap        string
	sourceClusterArn   string

	useSaslIam                  bool
	useSaslScram                bool
	useTls                      bool
	useUnauthenticatedTLS       bool
	useUnauthenticatedPlaintext bool

	saslScramUsername string
	saslScramPassword string

	tlsCaCert     string
	tlsClientCert string
	tlsClientKey  string
)
```

Note: `credentialsFile` is removed, `sourceClusterArn` is added.

**Step 2: Update required flags in `NewMigrationExecuteCmd`**

Remove `credentialsFile` from required flags, add `sourceClusterArn`:

```go
	requiredFlags.StringVar(&sourceClusterArn, "source-cluster-arn", "", "ARN of the source MSK cluster.")
```

Remove this line:
```go
	requiredFlags.StringVar(&credentialsFile, "credentials-file", "", "Credentials YAML file for MSK cluster authentication.")
```

Add to MarkFlagRequired calls:
```go
	migrationExecuteCmd.MarkFlagRequired("source-cluster-arn")
```

Remove:
```go
	migrationExecuteCmd.MarkFlagRequired("credentials-file")
```

**Step 3: Add auth flag groups**

After the required flags block, add the same three flag groups as in Task 1 (auth, sasl-scram, tls) — same code, just using `migrationExecuteCmd` instead of `migrationInitCmd`.

**Step 4: Add mutual exclusivity**

```go
	migrationExecuteCmd.MarkFlagsMutuallyExclusive("use-sasl-iam", "use-sasl-scram", "use-tls", "use-unauthenticated-tls", "use-unauthenticated-plaintext")
```

**Step 5: Update `SetUsageFunc` to include new flag groups**

```go
	flagOrder := []*pflag.FlagSet{requiredFlags, authFlags, saslScramFlags, tlsFlags}
	groupNames := []string{"Required Flags", "Source Cluster Authentication Flags", "SASL/SCRAM Flags", "TLS Flags"}
```

**Step 6: Update `preRunMigrationExecute` for conditional requirements**

Same pattern as init:

```go
func preRunMigrationExecute(cmd *cobra.Command, args []string) error {
	if err := utils.BindEnvToFlags(cmd); err != nil {
		return err
	}

	if useSaslScram {
		cmd.MarkFlagRequired("sasl-scram-username")
		cmd.MarkFlagRequired("sasl-scram-password")
	}

	if useTls {
		cmd.MarkFlagRequired("tls-ca-cert")
		cmd.MarkFlagRequired("tls-client-cert")
		cmd.MarkFlagRequired("tls-client-key")
	}

	return nil
}
```

**Step 7: Add `resolveAuthType` helper and update `parseMigrationExecutorOpts`**

```go
func resolveAuthType() types.AuthType {
	switch {
	case useSaslIam:
		return types.AuthTypeIAM
	case useSaslScram:
		return types.AuthTypeSASLSCRAM
	case useTls:
		return types.AuthTypeTLS
	case useUnauthenticatedTLS:
		return types.AuthTypeUnauthenticatedTLS
	case useUnauthenticatedPlaintext:
		return types.AuthTypeUnauthenticatedPlaintext
	default:
		return types.AuthTypeIAM
	}
}

func parseMigrationExecutorOpts(migrationState types.MigrationState, config types.MigrationConfig) MigrationExecutorOpts {
	return MigrationExecutorOpts{
		MigrationStateFile: migrationStateFile,
		MigrationState:     migrationState,
		MigrationConfig:    config,
		LagThreshold:       lagThreshold,
		ClusterApiKey:      clusterApiKey,
		ClusterApiSecret:   clusterApiSecret,
		CCBootstrap:        ccBootstrap,
		SourceClusterArn:   sourceClusterArn,
		AuthType:           resolveAuthType(),
		SaslScramUsername:   saslScramUsername,
		SaslScramPassword:   saslScramPassword,
		TlsCaCert:           tlsCaCert,
		TlsClientCert:      tlsClientCert,
		TlsClientKey:       tlsClientKey,
	}
}
```

**Step 8: Build and verify**

Run: `go build ./...`
Expected: build fails because `MigrationExecutorOpts` doesn't have the new fields yet (that's Task 3)

**Step 9: Commit (WIP, won't build until Task 3)**

```
git add cmd/migration/execute/cmd_migration_execute.go
git commit -m "feat: add auth flags to migration execute command

WIP: depends on migration_executor.go changes"
```

---

### Task 3: Rewrite migration executor to use auth flags

**Files:**
- Modify: `cmd/migration/execute/migration_executor.go`

**Step 1: Update `MigrationExecutorOpts` struct**

Replace the struct with:

```go
type MigrationExecutorOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
	MigrationConfig    types.MigrationConfig
	LagThreshold       int64
	ClusterApiKey      string
	ClusterApiSecret   string
	CCBootstrap        string
	SourceClusterArn   string
	AuthType           types.AuthType
	SaslScramUsername   string
	SaslScramPassword   string
	TlsCaCert           string
	TlsClientCert      string
	TlsClientKey       string
}
```

Note: `CredentialsFile` is removed. `SourceClusterArn`, `AuthType`, and credential fields are added.

**Step 2: Rewrite `createSourceOffset`**

Replace the entire method:

```go
func (m *MigrationExecutor) createSourceOffset(ctx context.Context) (*offset.Service, error) {
	config := m.opts.MigrationConfig
	authType := m.opts.AuthType

	region, err := utils.ExtractRegionFromArn(m.opts.SourceClusterArn)
	if err != nil {
		return nil, err
	}

	slog.Debug("discovering MSK bootstrap brokers")
	mskAwsClient, err := client.NewMSKClient(region, 8, 1)
	if err != nil {
		return nil, fmt.Errorf("failed to create MSK API client: %w", err)
	}

	mskService := msk.NewMSKService(mskAwsClient)
	bootstrapOutput, err := mskService.GetBootstrapBrokers(ctx, config.SourceClusterArn)
	if err != nil {
		return nil, fmt.Errorf("failed to get bootstrap brokers: %w", err)
	}

	awsInfo := types.AWSClientInformation{BootstrapBrokers: *bootstrapOutput}
	brokerAddresses, err := awsInfo.GetBootstrapBrokersForAuthType(authType)
	if err != nil {
		return nil, err
	}

	// Build ClusterAuth from flag values
	clusterAuth := types.ClusterAuth{}
	switch authType {
	case types.AuthTypeSASLSCRAM:
		clusterAuth.AuthMethod.SASLScram = &types.SASLScramConfig{
			Use:      true,
			Username: m.opts.SaslScramUsername,
			Password: m.opts.SaslScramPassword,
		}
	case types.AuthTypeTLS:
		clusterAuth.AuthMethod.TLS = &types.TLSConfig{
			Use:        true,
			CACert:     m.opts.TlsCaCert,
			ClientCert: m.opts.TlsClientCert,
			ClientKey:  m.opts.TlsClientKey,
		}
	case types.AuthTypeIAM:
		clusterAuth.AuthMethod.IAM = &types.IAMConfig{Use: true}
	case types.AuthTypeUnauthenticatedTLS:
		clusterAuth.AuthMethod.UnauthenticatedTLS = &types.UnauthenticatedTLSConfig{Use: true}
	case types.AuthTypeUnauthenticatedPlaintext:
		clusterAuth.AuthMethod.UnauthenticatedPlaintext = &types.UnauthenticatedPlaintextConfig{Use: true}
	}

	slog.Debug("connecting to source cluster (MSK)")
	sourceClient, err := client.NewKafkaClient(brokerAddresses, region, client.AdminOptionForAuth(authType, clusterAuth))
	if err != nil {
		return nil, fmt.Errorf("failed to connect to source cluster: %w", err)
	}
	slog.Debug("source cluster connected")

	return offset.NewOffsetService(sourceClient), nil
}
```

Note: `config.SourceClusterArn` is used for `GetBootstrapBrokers` (it's in the state file from init). `m.opts.SourceClusterArn` could also be used — they should be the same value. Use `m.opts.SourceClusterArn` for consistency since it comes from the flag.

**Step 3: Build and verify**

Run: `go build ./...`
Expected: builds successfully (Tasks 2 + 3 together resolve the compilation)

**Step 4: Run tests**

Run: `go test ./cmd/migration/...`
Expected: all tests pass

**Step 5: Commit**

```
git add cmd/migration/execute/migration_executor.go
git commit -m "feat: rewrite createSourceOffset to use auth flags instead of credentials file"
```

---

### Task 4: Squash WIP commits and final verification

**Step 1: Run full build**

Run: `go build ./...`
Expected: builds successfully

**Step 2: Run all tests**

Run: `go test ./...`
Expected: all tests pass

**Step 3: Manually verify flag output**

Run: `go run . migration init --help`
Expected: shows Required Flags, Optional Flags, Source Cluster Authentication Flags, SASL/SCRAM Flags, TLS Flags

Run: `go run . migration execute --help`
Expected: shows Required Flags, Source Cluster Authentication Flags, SASL/SCRAM Flags, TLS Flags. No `--credentials-file`.

**Step 4: Interactive rebase to squash WIP commit from Task 2**

Squash the Task 2 and Task 3 commits together since Task 2 was a WIP that didn't build alone.

**Step 5: Final commit message**

The final history should have two clean commits:
1. `feat: add auth flags to migration init command`
2. `feat: add auth flags to migration execute command, remove credentials file`
