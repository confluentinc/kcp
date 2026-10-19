# kcp Lambda scanner

A one-shot AWS Lambda that runs kcp scan steps from **inside the cluster's VPC**, so private MSK
clusters (brokers not reachable from outside) can be scanned. It bundles the `kcp` binary, runs it with the AWS credentials from the invoke
payload (e.g. short-lived STS credentials), and returns `kcp-state.json` in the response. It is a separate `package main` (custom runtime
`bootstrap`), not a kcp subcommand.

## Build

```bash
make build-lambda-scanner VERSION=v1.2.3   # dist/kcp-lambda-scanner-linux-arm64.zip (bootstrap + kcp), prints SHA-256
```

`VERSION` sets the version reported by the bundled kcp (default `0.0.0-localdev`).

## Event

```json
{
  "cluster_arn": "arn:aws:kafka:us-east-1:123456789012:cluster/demo/abc",
  "region": "us-east-1",
  "steps": ["discover", "scan-clusters", "scan-msk-connectors", "scan-self-managed-connectors", "scan-schema-registry"],
  "aws_access_key_id": "...", "aws_secret_access_key": "...", "aws_session_token": "...",
  "state": {}, "credentials_yaml": "...",
  "skip_metrics": false,
  "connector_metrics_granularity": "1d",
  "connect_rest_url": "http://connect:8083",
  "connect_auth": {"type": "basic", "username": "...", "password": "..."},
  "sasl_secret_arn": "arn:aws:secretsmanager:us-east-1:123456789012:secret:AmazonMSK_demo-AbCdEf",
  "connect_insecure_skip_tls_verify": false,
  "schema_registries": [
    {"type": "confluent", "url": "https://sr:8081", "auth": {"type": "basic", "username": "...", "password": "..."}, "insecure_skip_tls_verify": false},
    {"type": "glue", "registry_name": "my-registry", "region": "us-east-1"}
  ],
  "auth_method": "sasl_scram",
  "log_tail_bytes": 6000
}
```

- `cluster_arn`, `region`, `aws_access_key_id` and `aws_secret_access_key` are required; `region`
  must match the region in `cluster_arn`. `steps` defaults to all; steps always run in the order
  above. Unknown steps are rejected.
- If `discover` is not a step, pass `state` and `credentials_yaml` from an earlier discover.
- `scan-self-managed-connectors` only runs when `connect_rest_url` is set. The Connect password is
  passed to kcp through its environment, never on the command line.
- `scan-schema-registry` runs last, once per `schema_registries` item (needs state). `confluent` needs
  `url` and optional `auth` (`unauthenticated` default, or `basic`; the password goes to kcp via its
  environment, never argv); `glue` needs `registry_name` and uses `region` (default: event region)
  with the payload AWS credentials. mTLS and custom CA (`use_mtls`, `tls_ca_cert`) are rejected: not
  supported yet (same file-path limitation as Kafka TLS). If requested with an empty list it is
  reported as skipped; each result carries a `target` naming the registry.
- `sasl_secret_arn`, `connect_auth.secret_arn` and `schema_registries[].auth.secret_arn` take a
  Secrets Manager ARN instead of a password; see [Credentials from Secrets Manager](#credentials-from-secrets-manager).
  `secret_arn` and `password` are mutually exclusive, and `secret_arn` requires `type: basic`.
- Validation: `credentials_yaml` must be valid YAML (otherwise rejected with a generic error that
  does not quote the input), and `connect_rest_url` and `schema_registries[].url` must not contain
  userinfo (`https://user:pass@host`); use the auth fields instead.
- If `discover` fails and no `state` was supplied, the remaining steps are skipped.
- `log_tail_bytes` is capped at 64 KiB.
- `connector_metrics_granularity`: default `1d`, `none` skips metrics.
- `auth_method` (`iam`, `sasl_scram`, `sasl_plain`, `tls`, `unauthenticated_tls`,
  `unauthenticated_plaintext`) rewrites `use:` in `msk-credentials.yaml` so only that method is used
  by `scan-clusters`. Without it kcp may pick unauthenticated plaintext and return no ACLs. If the
  rewrite fails, the step fails instead of falling back to the discovered auth.

## Response

`steps[]` (`step`, `args` with passwords redacted, `exit_code` (-1 = skipped), `duration_ms`,
`log_tail`, `warnings` = WARN/ERROR lines from `kcp.log`), `duration_ms`, `error`, `state_bytes`,
and the state as either `state` (raw JSON, up to 3 MiB) or `state_gzip_b64` (gzip + base64).
`credentials_yaml` is returned when `discover` ran, with usernames, passwords and client keys
blanked (keep your own copy of any secrets you supplied); TLS `client_key` (like `ca_cert` and
`client_cert`) is a file path, and is blanked anyway. Known secret values are also masked in
logs and args. Responses over about 5.5 MB are replaced by an error result without state or logs.
Invalid events (bad JSON, validation errors) are reported as a Lambda function error.

## IAM

- Execution role: only `AWSLambdaENIManagementAccess` (VPC ENI management). It has no data access;
  the scan uses the caller's credentials from the payload, and the function role's credentials are
  never passed to kcp. The only use of the role's own credentials is reading Secrets Manager secrets
  named by `secret_arn` fields (see above).
- Caller credentials in the payload: the same permissions as the kcp `discover` / `scan` commands
  being run.

## Credentials from Secrets Manager

Pass a secret ARN instead of a password so the caller never handles the secret value. Each secret
is JSON in the MSK SCRAM format, `{"username": "...", "password": "..."}`, and both keys are required.

- `sasl_secret_arn`: applied to the `cluster_arn` entry in `msk-credentials.yaml` before
  `scan-clusters`: it sets the username and password on `sasl_scram` (or on `sasl_plain` when
  `auth_method` is `sasl_plain`). If `auth_method` is empty it is set to `sasl_scram`. If the read
  fails, the `scan-clusters` step fails (the invoke continues with the other steps).
- `connect_auth.secret_arn` and `schema_registries[].auth.secret_arn` (type `basic`): replace the
  username and password.

The Lambda reads each distinct ARN once per invoke (30 s timeout) with its own execution role
credentials, in the region of the ARN. Those credentials are never passed to kcp. Passwords are
added to the log scrubber before any step runs; secret values are never returned or logged.
Errors may include the ARN and the AWS error code and message. Inline passwords remain supported,
but `secret_arn` is recommended.

### Permissions

The execution role has only `AWSLambdaENIManagementAccess` as a permission boundary, so identity-based
grants do not work. Access comes from the secret's resource policy, plus (for customer-managed KMS
keys, which MSK SCRAM secrets require) a `kms:Decrypt` statement in the key policy. Append these to
the existing policies: MSK secrets already have a `kafka.amazonaws.com` statement, and
`put-resource-policy` replaces the whole policy.

Secret resource policy, form (a): the role session ARN. Lambda's session name is the function name.
No wildcard, but it works only if the role and function names are fixed across runs.

```json
{
  "Sid": "AllowScanner",
  "Effect": "Allow",
  "Principal": {"AWS": "arn:aws:sts::123456789012:assumed-role/<scanner-role>/<function-name>"},
  "Action": "secretsmanager:GetSecretValue",
  "Resource": "*"
}
```

Form (b): any principal matching a role-name prefix. Works with per-run names, but anyone in the
account who can create a role matching the prefix can read the secret. Keep the prefix specific
and restrict `iam:CreateRole` on it.

```json
{
  "Sid": "AllowScanner",
  "Effect": "Allow",
  "Principal": "*",
  "Action": "secretsmanager:GetSecretValue",
  "Resource": "*",
  "Condition": {"ArnLike": {"aws:PrincipalArn": "arn:aws:iam::123456789012:role/<scanner-role-prefix>*"}}
}
```

KMS key policy statement (same principal as the secret statement; shown with form b):

```json
{
  "Sid": "AllowScannerDecryptViaSecretsManager",
  "Effect": "Allow",
  "Principal": "*",
  "Action": "kms:Decrypt",
  "Resource": "*",
  "Condition": {
    "ArnLike": {"aws:PrincipalArn": "arn:aws:iam::123456789012:role/<scanner-role-prefix>*"},
    "StringEquals": {"kms:ViaService": "secretsmanager.us-east-1.amazonaws.com"}
  }
}
```

Two forms do not work because the permission boundary applies: a policy naming the role ARN
(`arn:aws:iam::<account>:role/<scanner-role>`) as `Principal`, or the account root with an
`aws:PrincipalArn` condition.

## Deploy, invoke, clean up

```bash
aws lambda create-function --function-name kcp-scanner --runtime provided.al2023 \
  --architectures arm64 --handler bootstrap --timeout 900 --memory-size 1024 \
  --role arn:aws:iam::123456789012:role/kcp-scanner-exec \
  --zip-file fileb://dist/kcp-lambda-scanner-linux-arm64.zip \
  --vpc-config SubnetIds=subnet-aaa,subnet-bbb,SecurityGroupIds=sg-ccc

# While the function is Pending, invoke returns ResourceConflictException: retry until Active.
aws lambda wait function-active-v2 --function-name kcp-scanner
aws lambda invoke --function-name kcp-scanner --cli-binary-format raw-in-base64-out \
  --payload fileb://event.json out.json

aws lambda delete-function --function-name kcp-scanner
```

Use the cluster's own subnets and security group. Lambda VPC ENIs are released about 20 minutes
after the function is deleted.

## Limits

- 15 minute maximum run time; 6 MB response limit (large state is gzipped).
- Confluent schema registries must be reachable from the cluster subnets/security group.
- Brokers must be reachable from the chosen subnets/security group.
- `discover`, `scan-msk-connectors` and Glue `scan-schema-registry` call AWS APIs from inside the Lambda, so the VPC needs NAT or
  VPC endpoints.
- Supported auth: IAM, SASL/SCRAM, SASL/PLAIN (secrets in `credentials_yaml`) and unauthenticated.
  TLS client-cert (mTLS) and custom-CA TLS are not supported yet, because `ca_cert`, `client_cert`
  and `client_key` in the credentials file are file paths that the payload cannot supply
  (follow-up: a `files` map written under the working directory).
- MSK Serverless: ACLs are not available via the Kafka Admin API, so kcp skips them with a WARN.
- The scan stops about 30 seconds before the Lambda deadline and returns the state gathered so far,
  with an error note. Stale `/tmp/kcp-*` dirs from a killed sandbox are removed on start.

## Security

- The event payload (credentials) is never logged; only per-step exit codes and durations are.
- kcp runs with only the payload credentials (`AWS_EC2_METADATA_DISABLED=true`); all files live under
  `/tmp` and are removed after each invocation.
- Verify the deployed code: compare the zip's SHA-256 (printed by `make build-lambda-scanner`) with
  the function's `CodeSha256`:
  `aws lambda get-function-configuration --function-name kcp-scanner --query CodeSha256 --output text`
  (Lambda reports base64; `openssl dgst -sha256 -binary <zip> | base64`).

## Design and follow-ups

The handler exec's the bundled kcp CLI (proven path). A future version may call the kcp packages
directly. Releasing the zip is a follow-up: add a goreleaser `builds` entry for `cmd/lambda-scanner`
(linux/arm64) plus an archive step that zips it with the kcp binary, and attach it to the release.
