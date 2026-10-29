# Connections

The four sections that tell kcp where the clusters, the cluster link and the gateway are.

## `spec.source`

The cluster being migrated from. `kcp` only ever reads from it.

| Field              | Type           | Required | Notes                                                               |
| ------------------ | -------------- | -------- | ------------------------------------------------------------------- |
| `type`{ #source-type }             | enum           | yes      | `msk` or `apache-kafka`. Gates auth: `iam` is valid only for `msk`. |
| `bootstrapServers`{ #source-bootstrapservers } | `[]string`     | yes      | Non-empty; each entry `host:port`.                                  |
| `credentials`{ #source-credentials }      | path           | yes      | Path to a Kafka-family credentials file — see [Credentials](credentials.md#credentials) below.   |

`confluent-platform` is not a valid value here — this manifest only ever
points at a link that already exists, so it never needs to act as a
source-side link initiator.

## `spec.target`

The cluster being migrated to.

| Field                    | Type           | Required                                                      | Notes                                                                                                                                                                                    |
| ------------------------ | -------------- | ------------------------------------------------------------- | ---------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `type`{ #target-type }                   | enum           | yes                                                           | `confluent-cloud` or `confluent-platform`.                                                                                                                                               |
| `clusterId`{ #target-clusterid }              | string         | yes                                                           | Required for **both** destination types — this kind never discovers it live.                                                                                                             |
| `kafka.bootstrapServers`{ #target-kafka-bootstrapservers } | `[]string`     | yes                                                           | Destination Kafka bootstrap.                                                                                                                                                             |
| `kafka.restEndpoint`{ #target-kafka-restendpoint }      | string         | yes                                                           | Destination Admin REST endpoint (cluster link + topic operations).                                                                                                                       |
| `kafka.clusterCredentials`{ #target-kafka-clustercredentials } | path          | yes                                                           | Path to the destination Kafka leg credentials file, dialled directly to read destination-side offsets. Accepts `sasl_plain`, `sasl_scram`, `mtls`, `unauthenticated_tls`, or `unauthenticated_plaintext` — see below. |

The destination REST (cluster-link) credential is **not** on `spec.target.kafka`
— it lives at [`spec.clusterLink.linkCredentials`](#specclusterlink), separately
and always required.

`kafka.clusterCredentials` accepts any Kafka auth method **except** `iam` — the
destination is Confluent Cloud or Confluent Platform, never MSK, so `iam` is
rejected rather than silently accepted and then failing opaquely at
connection time. The rejection happens when `execute` (including `--dry-run`)
reads the credentials file — not when the manifest is parsed, and `lag-check`
never reads that file. As on the source leg, a `ca_cert` inside `sasl_plain`
names a private CA the client trusts directly, on top of (or instead of) the
public trust store `tls: true` selects.

Unlike the shared table above, a destination `sasl_plain` with **neither**
`ca_cert` nor `tls` set does not fall back to `SASL_PLAINTEXT`: it defaults to
`tls: true` against the public trust store — the destination is always a managed/production
cluster, never on-prem plaintext like a source may legitimately be. There is
no `sasl_plain` field that opts back into `SASL_PLAINTEXT` against the
destination; use `unauthenticated_plaintext` for a genuinely plaintext
destination (test/lab only).

## `spec.clusterLink`

| Field                     | Type       | Required | Default | Notes                                                                                                                                            |
| ------------------------- | ---------- | -------- | ------- | ------------------------------------------------------------------------------------------------------------------------------------------------ |
| `name`{ #clusterlink-name }                    | string     | yes      | —       | Name of a cluster link that **already exists** on the destination. This kind never creates one.                                                  |
| `bootstrapServers`{ #clusterlink-bootstrapservers }        | `[]string` | no       | —       | Repeats `spec.target.kafka.bootstrapServers` for manifest self-documentation. Not validated against it.                                          |
| `linkCredentials`{ #clusterlink-linkcredentials }         | path       | yes      | —       | Path to the destination REST (cluster-link) credentials file — see [REST credentials](credentials.md#rest-credentials-specclusterlinklinkcredentials) below.  |

**Why two destination credentials at all?** `spec.target.kafka.clusterCredentials`
authenticates a direct Kafka-protocol connection to the destination
`bootstrapServers`; `spec.clusterLink.linkCredentials` authenticates HTTP calls
to `restEndpoint`'s Admin REST API, which is what actually manages the cluster
link (status, list/promote mirror topics, alter configs). These are two
different servers speaking two different protocols. On self-managed Confluent
Platform they are backed by two independent credential stores the operator
configures separately on the cluster: the Kafka SASL/SCRAM (or mTLS) listener
has its own store, while the REST API's auth (`kafka.rest.authentication.method`
— a Basic realm file, MDS for bearer, or its own mTLS trust store) has another. A
valid Kafka credential is **not** automatically a valid REST credential, even
against the same broker process. `linkCredentials` is therefore **always
required** and never derived from the Kafka leg — its top-level shape is one of
`api_key`/`api_secret`, `basic`, `bearer`, or `mtls` (see the
[REST credentials table](credentials.md#rest-credentials-specclusterlinklinkcredentials)).

## `spec.gateway`

There is no `crs.fenced` or `crs.switchover` file: kcp derives both the fenced
CR and the switched CR from the live initial CR at cutover. On a static route
that is a route-level fence added to the route named in the declared section's `route`, then that
route's `streamingDomain` flipped to its declared target; on a dynamic route it
is a `rules.fencing` entry for the selected topics, then a routing condition
sending those topics to the target domain. The old `crs.initial` nesting
is flattened to a single `cr-name`, and the retired `crs`/`routes` keys are
removed from the schema entirely: a stale manifest that still uses them fails
the strict decode with an unknown-field error.

| Field        | Type   | Required | Notes                                                                                               |
| ------------ | ------ | -------- | --------------------------------------------------------------------------------------------------- |
| `namespace`{ #gateway-namespace }  | string | yes      | Kubernetes namespace where the gateway is deployed.                                                 |
| `kubeconfig`{ #gateway-kubeconfig } | string | no       | Path to the kubeconfig to use. The **one** field in this manifest where a leading `~/` is expanded. Unset: the pod's in-cluster service account when kcp runs in a pod, `~/.kube/config` otherwise. |
| `cr-name`{ #gateway-cr-name }    | string | yes      | The **name** of the initial gateway custom resource — read live from the cluster on each `execute` run, not a file path. |
