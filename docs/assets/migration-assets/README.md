# Migration assets

A `kcp migrate` migration is driven by a single **manifest** (`migration.yaml`)
that describes the desired end state, plus external **credential files** that hold
auth only. This section provides a ready-to-copy reference manifest, worked
examples for each scenario, and a catalog of every credential format.

| | |
|---|---|
| [`migration.example.yaml`](migration.example.yaml) | the fully-annotated reference manifest — every field, with comments |
| [examples/](examples/) | minimal, validated manifests per scenario (below) |
| [credentials/](credentials/README.md) | one credential file per auth method, with a slot-applicability table |

## Examples

| Example | Topics mode | Cluster link | Source → target |
|---|---|---|---|
| [new-topics](examples/new-topics/) | `new` | none | Apache Kafka → Confluent Cloud |
| [mirror-topics](examples/mirror-topics/) | `mirror` | destination-initiated | MSK → Confluent Cloud |
| [source-initiated](examples/source-initiated/) | `mirror` | source-initiated | private Confluent Platform → Confluent Cloud |

Validate, preview, then apply any of them:

```bash
kcp migrate validate -f migration.yaml
kcp migrate apply -f migration.yaml --dry-run   # preview; changes nothing
kcp migrate apply -f migration.yaml
```

`apply` is **additive**: it only creates what is absent and reports — never alters
or deletes — what already exists. `--dry-run` prints the same plan without making
any change.

## Manifest at a glance

The manifest carries all **addresses**; `credentials:` fields reference external
files that carry **secrets only**. Each connection is a uniform `{address,
credentials}` pair:

- **Kafka slots** (`spec.source`, `spec.clusterLink.source`,
  `spec.clusterLink.destination`) — `bootstrapServers` + `credentials`.
- **REST slots** (`spec.target` + `spec.target.kafka.restEndpoint`,
  `spec.clusterLink.sourceRest`) — `endpoint`/`restEndpoint` + `credentials`.

Which credential family each slot uses, and every supported auth method, is in the
[credential catalog](credentials/README.md).

## spec.topics

`spec.topics` selects source topics and reproduces them on the target. Fields:
`mode`, `include` (required), `exclude` (optional). There is no `prefix` here — the
prefix is a cluster-link concept (`spec.clusterLink.prefix`).

**Selection.** `include`/`exclude` are globs (`path.Match`, e.g. `orders.*`, `*`)
matched against the **live source topic list** read at apply time. A topic is
selected when it matches any `include` and no `exclude`. **Internal topics — names
starting with `_`, e.g. `__consumer_offsets`, `_schemas` — are always
auto-excluded**, regardless of the patterns.

**Modes.**

- **`mode: mirror`** — read-only mirror topics on the cluster link, one per
  selected source topic. **Requires `spec.clusterLink`.** Mirror names are
  `<spec.clusterLink.prefix>+<sourceName>` (no prefix → same name). Works in both
  destination- and source-initiated topologies.
- **`mode: new`** — plain (non-mirror) topics created directly on the target,
  reproducing each source topic's partition count, replication factor, and
  explicitly-set configs. If the target rejects a config, that topic's create is
  reported as a **failure** (not silently dropped). Needs **no** cluster link.

**Drift (report-only).** Apply only creates absent topics. In `mode: mirror`, an
existing mirror is `Present`; a target name already taken (by a plain topic, or a
mirror on another link) is reported as **drift** and skipped. In `mode: new`, an
existing target topic whose partition count differs from the source is **drift** and
left untouched.

## Editor support

`migration.example.yaml` and every example carry a `# yaml-language-server:
$schema=…` modeline, so VS Code with the
[Red Hat YAML extension](https://marketplace.visualstudio.com/items?itemName=redhat.vscode-yaml)
gives autocomplete and inline validation automatically. For offline/repo-local use,
map the schema in your workspace settings instead:

```jsonc
// .vscode/settings.json
"yaml.schemas": {
  "internal/manifest/migration.schema.json": ["migration*.yaml"]
}
```
