# migration.yaml

`migration.example.yaml` is the reference manifest for a KCP migration. Copy it and fill in your values, then validate with:

```bash
kcp migrate validate -f migration.yaml
```

## Credential files

The manifest never contains secrets — it references external credential files by path.

**Kafka credentials** (`spec.source.credentials`, `spec.clusterLink.sourceCredentials`, `spec.clusterLink.destinationCredentials`) use a flat, single-cluster format — a migration only ever connects to one source and one destination cluster:

```yaml
bootstrap_servers: ["broker1:9092", "broker2:9092"]
auth_method:
  # exactly one of:
  sasl_scram: { use: true, username: admin, password: secret, mechanism: SHA256, ca_cert: ./ca.pem }
  # sasl_plain: { use: true, username: admin, password: secret }
  # tls: { use: true, ca_cert: ./ca.pem, client_cert: ./client.pem, client_key: ./client.key }
  # unauthenticated_tls: { use: true, ca_cert: ./ca.pem }
  # unauthenticated_plaintext: { use: true }
insecure_skip_tls_verify: false   # optional; test environments only
```

This is distinct from the `kcp scan` `apache-kafka-credentials.yaml` (which lists multiple `clusters:` with scan-only metrics blocks). Passing the scan format here is rejected with a hint.

**REST credentials** (`spec.target.credentials`, `spec.clusterLink.sourceRest.credentials`) authenticate to the Kafka REST / Admin API and use one of a `basic`, `api_key`, `bearer`, or `mtls` block.

## Editor support

VS Code with the [Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml) picks up the `# yaml-language-server: $schema=…` modeline already present in `migration.example.yaml` — autocomplete and inline validation work automatically when you copy the file.

For offline or repo-local use, map the schema in your workspace settings instead:

```jsonc
// .vscode/settings.json
"yaml.schemas": {
  "internal/manifest/migration.schema.json": ["migration*.yaml"]
}
```
