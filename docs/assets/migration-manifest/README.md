# migration.yaml

`migration.example.yaml` is the reference manifest for a KCP migration. Copy it and fill in your values, then validate with:

```bash
kcp migrate validate -f migration.yaml
```

## Credential files

The manifest describes the topology — including all **connection addresses** (`bootstrapServers`, REST `endpoint`s) — and references external credential files by path. **Credential files hold only secrets (auth); addresses live in the manifest, not the creds files.**

Every connection in the manifest is a uniform `{address, credentials}` pair: Kafka slots (`spec.source`, `spec.clusterLink.source`, `spec.clusterLink.destination`) carry `bootstrapServers` + `credentials`; REST slots (`spec.target.kafka.restEndpoint` + `spec.target.credentials`, `spec.clusterLink.sourceRest`) carry `endpoint` + `credentials`.

**Kafka credentials** (the file each Kafka slot's `credentials:` points at) are auth-only:

```yaml
auth_method:
  # exactly one of:
  sasl_scram: { use: true, username: admin, password: secret, mechanism: SHA256, ca_cert: ./ca.pem }
  # sasl_plain: { use: true, username: admin, password: secret }
  # tls: { use: true, ca_cert: ./ca.pem, client_cert: ./client.pem, client_key: ./client.key }
  # unauthenticated_tls: { use: true, ca_cert: ./ca.pem }
  # unauthenticated_plaintext: { use: true }
insecure_skip_tls_verify: false   # optional; test environments only
```

This is distinct from the `kcp scan` `apache-kafka-credentials.yaml` (which lists multiple `clusters:`, each with its own `bootstrap_servers` and scan-only metrics blocks). Passing the scan format — or a stray `bootstrap_servers:` — to a migrate creds file is rejected with a hint.

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
