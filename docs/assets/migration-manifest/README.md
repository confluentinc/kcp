# migration.yaml

`migration.example.yaml` is the reference manifest for a KCP migration. Copy it and fill in your values, then validate with:

```bash
kcp migrate validate -f migration.yaml
```

## Credential files

The manifest describes the topology — including all **connection addresses** (`bootstrapServers`, REST `endpoint`s) — and references external credential files by path. **Credential files hold only secrets (auth); addresses live in the manifest, not the creds files.**

Every connection in the manifest is a uniform `{address, credentials}` pair: Kafka slots (`spec.source`, `spec.clusterLink.source`, `spec.clusterLink.destination`) carry `bootstrapServers` + `credentials`; REST slots (`spec.target.kafka.restEndpoint` + `spec.target.credentials`, `spec.clusterLink.sourceRest`) carry `endpoint` + `credentials`.

**Kafka credentials** (the file each Kafka slot's `credentials:` points at) are auth-only. Specify exactly one auth method as a top-level block — the block's presence selects it (no `auth_method:` wrapper, no `use:` flag):

```yaml
sasl_scram: { username: admin, password: secret, mechanism: SHA256, ca_cert: ./ca.pem }
# or exactly one of:
# sasl_plain: { username: admin, password: secret }
# mtls: { ca_cert: ./ca.pem, client_cert: ./client.pem, client_key: ./client.key }
# unauthenticated_tls: { ca_cert: ./ca.pem }
# unauthenticated_plaintext: {}
insecure_skip_tls_verify: false   # optional; test environments only
```

This is distinct from the `kcp scan` `apache-kafka-credentials.yaml` (which lists multiple `clusters:`, each with its own `bootstrap_servers`, an `auth_method:` wrapper with `use:` flags, and scan-only metrics blocks). Passing the scan format — an `auth_method:` wrapper, a `clusters:` list, or a stray `bootstrap_servers:` — to a migrate creds file is rejected with a hint.

**REST credentials** (`spec.target.credentials`, `spec.clusterLink.sourceRest.credentials`) authenticate to the Kafka REST / Admin API and use one of a `basic`, `api_key`, `bearer`, or `mtls` block.

## spec.topics

`spec.topics` selects source topics and reproduces them on the target. It has three fields — `mode`, `include`, and `exclude` (there is no `prefix` here; the prefix is a cluster-link concept, set under `spec.clusterLink.prefix`).

**Selection.** `include` (required) and `exclude` (optional) are glob patterns (`path.Match` semantics, e.g. `orders.*`, `*`) matched against the **live source topic list** read at apply time. A topic is selected when it matches any `include` and no `exclude` pattern. **Internal topics — names starting with `_`, such as `__consumer_offsets` and `_schemas` — are always auto-excluded** regardless of the patterns.

**Two modes:**

- **`mode: mirror`** — creates read-only mirror topics on the cluster link, one per selected source topic, fed through the link. **Requires `spec.clusterLink`** (validation fails otherwise). Mirror topic names are `<prefix>+<sourceName>`, where the prefix comes from `spec.clusterLink.prefix` (`cluster.link.prefix`); with no prefix the mirror keeps the source name. Works in both destination-initiated and source-initiated link topologies.
- **`mode: new`** — creates plain (non-mirror) topics directly on the target, reproducing each source topic's **partition count, replication factor, and explicitly-set (non-default) configs**. A small managed/read-only skip-list (broker-managed keys such as `confluent.tier.*` and replication-throttle keys) is not forwarded. Needs **no** cluster link.

**Drift (report-only).** Apply is **additive: it only creates absent topics — it never alters or deletes existing topics.** In `mode: mirror`, an existing mirror topic is reported `Present` (mirror health is out of scope). In `mode: new`, an existing target topic whose partition count differs from the source is reported as `Drift` and left untouched.

## Editor support

VS Code with the [Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml) picks up the `# yaml-language-server: $schema=…` modeline already present in `migration.example.yaml` — autocomplete and inline validation work automatically when you copy the file.

For offline or repo-local use, map the schema in your workspace settings instead:

```jsonc
// .vscode/settings.json
"yaml.schemas": {
  "internal/manifest/migration.schema.json": ["migration*.yaml"]
}
```
