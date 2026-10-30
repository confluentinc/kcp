# Connection Settings in the Migration Manifest

Four sections of the migration manifest tell kcp where your source and destination Apache Kafka® clusters, the cluster link, and Confluent Gateway are: `spec.source`, `spec.target`, `spec.clusterLink`, and `spec.gateway`.

## `spec.source`

The cluster being migrated from. `kcp` only ever reads from it.

| Field              | Type           | Required | Notes                                                               |
| ------------------ | -------------- | -------- | ------------------------------------------------------------------- |
| `type`{ #source-type }             | enum           | yes      | `msk` or `apache-kafka`. Gates auth: `iam` is valid only for `msk`. |
| `bootstrapServers`{ #source-bootstrapservers } | `[]string`     | yes      | Non-empty. Each entry is `host:port`.                               |
| `credentials`{ #source-credentials }      | path           | yes      | Path to a Kafka-family credentials file. See [Credentials in the Migration Manifest](credentials.md).   |

`confluent-platform` is not a valid value here. This manifest only ever
points at a link that already exists, so it never needs to act as a
source-side link initiator.

## `spec.target`

The cluster being migrated to.

| Field                    | Type           | Required                                                      | Notes                                                                                                                                                                                    |
| ------------------------ | -------------- | ------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `type`{ #target-type }                   | enum           | yes                                                           | `confluent-cloud` or `confluent-platform`.                                                                                                                                               |
| `clusterId`{ #target-clusterid }              | string         | yes                                                           | Required for both destination types: this kind never discovers it live.                                                                                                             |
| `kafka.bootstrapServers`{ #target-kafka-bootstrapservers } | `[]string`     | yes                                                           | Destination Kafka bootstrap.                                                                                                                                                             |
| `kafka.restEndpoint`{ #target-kafka-restendpoint }      | string         | yes                                                           | Destination Admin REST endpoint (cluster link + topic operations).                                                                                                                       |
| `kafka.clusterCredentials`{ #target-kafka-clustercredentials } | path          | yes                                                           | Path to the destination Kafka leg credentials file, dialed directly to read destination-side offsets. Accepts `sasl_plain`, `sasl_scram`, `mtls`, `unauthenticated_tls`, or `unauthenticated_plaintext` (see below). |

The destination REST (cluster-link) credential is not on `spec.target.kafka`.
It lives separately at [`spec.clusterLink.linkCredentials`](#specclusterlink)
and is always required.

`kafka.clusterCredentials` accepts any Kafka auth method except `iam`. The
destination is Confluent Cloud or Confluent Platform, never MSK, so `iam` is
rejected rather than silently accepted and then failing opaquely at
connection time. The rejection happens when `execute` (including `--dry-run`)
reads the credentials file, not when the manifest is parsed. `lag-check`
never reads that file. As on the source leg, a `ca_cert` inside `sasl_plain`
names a private CA the client trusts directly, on top of (or instead of) the
public truststore `tls: true` selects.

Unlike a source `sasl_plain`, a destination `sasl_plain` with neither
`ca_cert` nor `tls` set does not fall back to `SASL_PLAINTEXT`: it defaults to
`tls: true` against the public truststore. The destination is always a managed or production
cluster, never on-premises plaintext like a source might legitimately be. No
`sasl_plain` field opts back into `SASL_PLAINTEXT` against the
destination. For a genuinely plaintext destination (test or lab only), use
`unauthenticated_plaintext`.

## `spec.clusterLink`

| Field                     | Type       | Required | Default | Notes                                                                                                                                            |
| ------------------------- | ---------- | -------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `name`{ #clusterlink-name }                    | string     | yes      | —       | Name of a cluster link that already exists on the destination. This kind never creates one.                                                  |
| `bootstrapServers`{ #clusterlink-bootstrapservers }        | `[]string` | no       | —       | Repeats `spec.target.kafka.bootstrapServers` for manifest self-documentation. Not validated against it.                                          |
| `linkCredentials`{ #clusterlink-linkcredentials }         | path       | yes      | —       | Path to the destination REST (cluster-link) credentials file. See [REST credentials](credentials.md#rest-credentials-specclusterlinklinkcredentials).  |

`spec.clusterLink` takes a single link, and a dynamic route is paired with one
link: the same link for every topic-based migration batch on the route and for the
[route conversion](route-conversion.md), which takes its scope (the topics and
consumer groups to move) from it. kcp doesn't check that separate manifests for the
same route name the same link, so don't split one route's batches across links.

**Why two destination credentials at all?** `spec.target.kafka.clusterCredentials`
authenticates a direct Kafka-protocol connection to the destination
`bootstrapServers`. `spec.clusterLink.linkCredentials` authenticates HTTP calls
to `restEndpoint`'s Admin REST API, which is what actually manages the cluster
link (status, list and promote mirror topics, alter configurations). These are two
different servers speaking two different protocols. On self-managed Confluent
Platform they are backed by two independent credential stores the operator
configures separately on the cluster: the Kafka SASL/SCRAM (or mTLS) listener
has its own store, while the REST API's auth, set by `kafka.rest.authentication.method`,
has another: a Basic realm file, Metadata Service (MDS) for bearer, or its own mTLS truststore. A
valid Kafka credential is not automatically a valid REST credential, even
against the same broker process. `linkCredentials` is therefore always
required and never derived from the Kafka leg. Its top-level shape is one of
an `api_key` and `api_secret` pair, `basic`, `bearer`, or `mtls` (see the
[REST credentials table](credentials.md#rest-credentials-specclusterlinklinkcredentials)).

## `spec.gateway`

The manifest has no `crs.fenced` or `crs.switchover` file: kcp derives both the fenced
custom resource (CR) and the switched CR from the live initial CR at cutover. On a static route
that is a route-level fence added to the route named in the declared section's `route`, then that
route's `streamingDomain` flipped to its declared target. On a dynamic route it
is a `rules.fencing` entry for the selected topics, then a routing condition
sending those topics to the target domain. The old `crs.initial` nesting
is flattened to a single `cr-name`, and the retired `crs` and `routes` keys are
removed from the schema entirely: a stale manifest that still uses them fails
the strict decode with an unknown-field error.

| Field        | Type   | Required | Notes                                                                                               |
| ------------ | ------ | -------- | --------------------------------------------------------------------------------------------------- |
| `namespace`{ #gateway-namespace }  | string | yes      | Kubernetes namespace where the Gateway is deployed.                                                 |
| `kubeconfig`{ #gateway-kubeconfig } | string | no       | Path to the kubeconfig to use. The one field in this manifest where a leading `~/` is expanded. If unset, kcp uses the pod's in-cluster service account when it runs in a pod, and `~/.kube/config` otherwise. |
| `cr-name`{ #gateway-cr-name }    | string | yes      | The name of the initial Gateway CR, read live from the cluster on each `execute` run. Not a file path. |
