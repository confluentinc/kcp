# Cutover Migration Manifest Reference

`kcp migration cutover execute` runs a cutover described by a single YAML manifest,
`gateway-static-migration.yaml`, `gateway-dynamic-migration.yaml`, or
`gateway-route-conversion.yaml`: it fences the route in Confluent Gateway,
promotes the mirror topics on the cluster link in batches, and switches
production traffic over to the destination.

The manifest
[declares which of three workflows it runs](#declare-the-migration-type):
all at once on a static route (`spec.staticMigration`), topic by topic on a
dynamic route (`spec.dynamicMigration`), or a
[conversion of a dynamic route to static](route-conversion.md)
(`spec.dynamicToStaticConversion`) after every topic on the cluster link is
migrated. `kcp migration cutover lag-check` reads the same manifest to show
replication lag on the cluster link.

| Page | What it covers |
|---|---|
| [Run a Migration Cutover](running-a-cutover.md) | How `execute` resumes and what to know before you run it |
| [Connection Settings in the Migration Manifest](connections.md) | `spec.source`, `spec.target`, `spec.clusterLink`, `spec.gateway` |
| [Migration Sections in the Migration Manifest](migrations.md) | `route`, `topicGroup`, and `consumerOffsetSync` for static and dynamic migrations |
| [Route Conversion Settings in the Migration Manifest](route-conversion.md) | `spec.dynamicToStaticConversion` |
| [Policies in the Migration Manifest](policies.md) | Per-run defaults and their override flags |
| [Credentials in the Migration Manifest](credentials.md) | Apache Kafka® and REST credentials files |
| [Migration Manifest Validation and Field Reference](validation-and-field-reference.md) | Validation rules and every field in one table |
| [Example Cutover Manifests](examples.md) | One annotated manifest per migration type |

## Declare the migration type

A `GatewayMigration` manifest declares what it does by which one section of
`spec` it carries. Exactly one is required:

| Section | What it does | Live route must be | Example |
|---|---|---|---|
| `staticMigration` | Migrates topics together across a static route (all-at-once). | `static` | [`gateway-static-migration.yaml`](../../gateway-examples/gateway-static-migration.yaml) |
| `dynamicMigration` | Migrates topics in batches across a dynamic route (topic-based). | `dynamic` | [`gateway-dynamic-migration.yaml`](../../gateway-examples/gateway-dynamic-migration.yaml) |
| `dynamicToStaticConversion` | Converts a fully-migrated dynamic route to a static route. Topics stay the same. The route's mode changes. | `dynamic` | [`gateway-route-conversion.yaml`](../../gateway-examples/gateway-route-conversion.yaml) |

A *migration* moves topic ownership from the source to the destination and
leaves the route's mode unchanged. A *conversion* changes the route's mode and
leaves the topics unchanged, so it selects no topics. kcp compares your
declaration with the live Gateway custom resource (CR) and refuses to run if the route's mode
disagrees. Each section carries its own `route`, its own `policies` (only the
fields that type uses), and, for migrations, a `topicGroup`. A policy flag
(such as `--promote-batch-size`) that the declared type does not have is an
error.

If you are upgrading a manifest from an earlier build: `spec.route` and
`spec.defaultPolicies` were replaced by the sections above, and
`spec.clusterLink.pauseConsumerOffsetSync` and `consumerOffsetSyncBaseline` moved
to `spec.staticMigration.consumerOffsetSync.pause` and `.baseline`.

## At a glance

```yaml
apiVersion: kcp.confluent.io/v1alpha1 # required, exact literal
kind: GatewayMigration # required, exact literal
metadata:
  name: my-migration # required, non-blank — the migration identity
spec:
  source: { ... } # required — cluster being migrated from
  target: { ... } # required — cluster being migrated to
  clusterLink: { ... } # required — an ALREADY-EXISTING cluster link
  gateway: { ... } # required — namespace, kubeconfig, initial gateway CR name
  # Exactly one of the next three — the declared migration type:
  staticMigration: { ... } # route, topicGroup, consumerOffsetSync, policies
  dynamicMigration: { ... } # route, topicGroup, policies
  dynamicToStaticConversion: { ... } # route, policies (no topicGroup)
```

`apiVersion` must equal `kcp.confluent.io/v1alpha1` and `kind` must equal
`GatewayMigration`, exactly. `metadata.name` is required and non-blank: it is the
migration's identity, used as its `migration_id` in logs and output.
`spec.source`, `spec.target`, `spec.clusterLink`, and `spec.gateway` are always
required, as is exactly one of `spec.staticMigration`, `spec.dynamicMigration`,
and `spec.dynamicToStaticConversion`. Each section's `policies` is optional.

Every credentials field is a path to a credentials file. The manifest never
holds secret material inline, and kcp does not substitute `${ENV_VAR}` references: a
`${VAR}`-shaped value is read literally.

## Commands that read the manifest

| Command                   | Flag                                                                                                                                                                                             | Required                            | Notes                                                                                                                                    |
| ------------------------- | ------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------ | ----------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------- |
| `kcp migration cutover execute`   | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest.                                                                                                                   |
|                           | `--dry-run`                                                                                                                                                                                      | no                                  | Run only the reconcile step and print the plan, changing nothing. Exits non-zero if the plan is refused.                                   |
|                           | `--lag-threshold`, `--promote-batch-size`, `--rollout-timeout`, `--detect-unrouted-producers-duration`, `--consumer-offset-sync-drain-duration`, `--hot-reload-timeout`, `--gateway-config-port`, `--detect-unrouted-commits-duration`, `--offset-sync-concurrency` | no                                  | Per-run overrides of the matching field in the declared section's `policies`, for this run only. A flag the declared type does not have is an error, including under `--dry-run`. |
| `kcp migration cutover lag-check` | `--migration-yaml`                                                                                                                                                                               | yes                                 | Path to this manifest. Reads only the destination REST leg (`spec.clusterLink.linkCredentials`), honored in whichever form it resolves (`api_key`, `basic`, `bearer`, or `mtls`). It never dials the source or destination Kafka legs. |
|                           | `--poll-interval`                                                                                                                                                                                | no (default `1`)                    | Poll interval in seconds. Values outside `1`-`60` are clamped to that range.                                                                                                      |

Every path in the manifest resolves relative to the process working
directory, not the manifest's own location. The one exception is
`spec.gateway.kubeconfig`, where a leading `~/` is expanded. The same applies
to the certificate and key paths inside a credentials file.

Every flag can also be set through an uppercase environment variable of the
same name with dashes as underscores (`--lag-threshold` is `LAG_THRESHOLD`,
`--dry-run` is `DRY_RUN`, `--poll-interval` is `POLL_INTERVAL`). An explicit
flag wins. A policy value set through an environment variable overrides the
manifest exactly as the flag would (and an inapplicable one is rejected the same way), so a stray `LAG_THRESHOLD` in the shell
silently changes a run. The overrides are validated with the rest of the
effective policy, including under `--dry-run`.

## Editor support

Each file under `gateway-examples/` carries a `# yaml-language-server:
$schema=…` modeline that fetches the manifest's JSON schema from the `main`
branch of the kcp repository on GitHub
(`internal/manifest/gatewaymigration.schema.json`), so VS Code with the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
gives autocomplete and inline validation when it can reach GitHub. The schema
covers structure and types but does not enforce every rule (for example, it
does not check the `10s` minimum or the `host:port` format), so `kcp
migration cutover execute --dry-run` is the authoritative check.
