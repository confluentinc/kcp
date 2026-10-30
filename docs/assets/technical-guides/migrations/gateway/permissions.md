# Permissions for Gateway Migrations

`kcp migration cutover execute` talks to five things: the Kubernetes API, the source Apache Kafka® cluster, the destination Kafka cluster, the cluster link's REST API, and, indirectly, the cluster link itself. Each one needs its own identity, and this page lists what each identity needs, which workflow needs it, and what you see when it is missing.

For the credentials file formats, see [Credentials](../../../migration-assets/cutover/credentials.md). For where each connection is used, see [Connections](../../../migration-assets/cutover/connections.md).

## Summary

| Identity                  | Where you set it                                         | What kcp does with it                                                                                                    |
| ------------------------- | -------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------------------ |
| Kubernetes                | `spec.gateway.kubeconfig` (or the pod's service account) | Reads and patches the Gateway custom resource (CR), reads its Deployment and pods.                                       |
| Source Kafka              | `spec.source.credentials`                                | Read-only: topic metadata and offsets. Conversion also lists groups and reads their offsets.                             |
| Destination Kafka         | `spec.target.kafka.clusterCredentials`                   | Read-only for static and dynamic. Conversion also writes consumer group offsets.                                         |
| Cluster link REST         | `spec.clusterLink.linkCredentials`                       | Reads link state, mirrors, and configurations. Promotes mirrors. A static migration with `pause` writes a configuration. |
| The link's own credential | Set when the link was created                            | Not used, read, or checked by cutover. It decides whether the link itself can read the source.                           |

Every write kcp makes is to the Gateway CR, the cluster link, or, for a conversion, the destination's consumer group offsets. kcp never writes to the source cluster.

## Kubernetes

kcp builds its client from `spec.gateway.kubeconfig`, expanding a leading `~/`. With no `kubeconfig` set, it uses the pod's service account when it runs inside a cluster, and `~/.kube/config` otherwise. A kubeconfig is used at its current context. The manifest has no field to pick another one. The namespace comes only from `spec.gateway.namespace`.

### Role

Grant this in the Gateway's namespace:

```yaml
rules:
  - apiGroups: ["platform.confluent.io"]
    resources: ["gateways"]
    verbs: ["get", "patch"]
  - apiGroups: ["apps"]
    resources: ["deployments"]
    verbs: ["get", "list"]
  - apiGroups: [""]
    resources: ["pods"]
    verbs: ["list"]
  - apiGroups: [""]
    resources: ["pods/proxy"]
    verbs: ["get"]
  - apiGroups: [""]
    resources: ["secrets"]
    verbs: ["get"]
```

| Resource and verb     | Needed when                                                                                                                            |
| --------------------- | -------------------------------------------------------------------------------------------------------------------------------------- |
| `gateways`: `get`     | Always, including `--dry-run`.                                                                                                         |
| `gateways`: `patch`   | Any real run. kcp applies the fence, the switch, and any rollback as JSON Patches.                                                     |
| `deployments`: `get`  | Any real run. kcp reads the Gateway's Deployment generation before each patch and while waiting for the rollout.                       |
| `deployments`: `list` | Only as a fallback, when no Deployment is named like the Gateway CR. kcp then finds the one owned by the CR.                           |
| `pods`: `list`        | The Gateway has `spec.hotReload.enabled: true`, or a static migration has `detectUnroutedProducersDuration` set and hot reload is off. |
| `pods/proxy`: `get`   | The Gateway has `spec.hotReload.enabled: true`. kcp reads each pod's `/config` through the API server to confirm the new revision.     |
| `secrets`: `get`      | Optional, static migrations only. See below.                                                                                           |

The `secrets` grant is optional. A static migration checks that every Secret referenced by the staged `security.cluster.<destination>` block exists, by name, without reading its contents. If the identity can't read Secrets, kcp skips the check and warns. A confirmed missing Secret still refuses the run. The Confluent operator, not kcp, reads the Secrets at runtime.

### Cluster-scoped grant for hot reload

When the live Gateway CR has `spec.hotReload.enabled: true`, kcp also reads the Gateway custom resource definition (CRD) to confirm the operator declares `spec.configId`:

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
rules:
  - apiGroups: ["apiextensions.k8s.io"]
    resources: ["customresourcedefinitions"]
    resourceNames: ["gateways.platform.confluent.io"]
    verbs: ["get"]
```

kcp uses `spec.configId` to confirm that each pod applied a hot reload, so without this grant it can't check that the field is declared, and it refuses before fencing. The refusal names two ways out: grant the permission, or set `spec.hotReload.enabled: false` in the live CR for the duration of the migration.

### When a Kubernetes grant is missing

| Missing                       | What you see                                                                                                                                                                                                                                                                           |
| ----------------------------- | -------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------------- |
| `gateways` get                | `failed to produce the reconcile plan: loading gateway config: reading gateway CR ...`, with the API server's 403 text. Before anything changes.                                                                                                                                       |
| `gateways` patch              | `failed to apply fenced gateway CR: ...` at the fence step. A rollback patch needs the same grant.                                                                                                                                                                                     |
| `deployments` get             | `failed to get gateway deployment: ...` while waiting for a rollout. After the fence, kcp rolls the fence back where it can.                                                                                                                                                           |
| CRD get (hot reload)          | A refusal before the fence, naming the grant (see above).                                                                                                                                                                                                                              |
| `pods/proxy` get (hot reload) | Before the fence: `could not reach the config endpoint on any of the N ready gateway pods ... (check the kcp identity has "get" on pods/proxy)`. If the grant is lost after that check, you see only `timed out after 90s waiting for the gateway to apply the configId on every pod`. |

For the Gateway and Confluent for Kubernetes (CFK) versions that hot reload needs, see [Version requirements](overview.md#version-requirements).

## Source Kafka cluster

kcp only reads from the source, with whichever of `iam` (MSK only), `sasl_scram`, `sasl_plain`, `mtls`, `unauthenticated_tls`, or `unauthenticated_plaintext` you put in the credentials file. With `iam`, kcp signs with the AWS credential chain on the machine it runs on, and that principal needs the MSK `kafka-cluster:*` actions for the calls below.

| What kcp reads                                   | Used by       | Kafka access control list (ACL)         |
| ------------------------------------------------ | ------------- | --------------------------------------- |
| Topic metadata and cluster ID                    | All workflows | `DESCRIBE` on topics                    |
| Partition offsets (lag, unrouted-producer check) | All workflows | `DESCRIBE` on topics                    |
| Every consumer group, with its committed offsets | Conversion    | `DESCRIBE` on all groups and all topics |

A credential that lacks `DESCRIBE` on a topic doesn't fail loudly. It sees fewer topics, and kcp reports `<topic> not found on the source`. If you see that for a topic you know exists, check the ACL before anything else.

## Destination Kafka cluster

The destination credential is separate from the link REST credential, and kcp never derives one from the other. It can be any of the types above except `iam`. On Confluent Cloud this is a cluster API key and secret, used as the SASL/PLAIN username and password. A `sasl_plain` credential with neither a CA certificate nor `tls: true` is forced to TLS.

| What kcp does                                        | Used by       | Kafka ACL                               |
| ---------------------------------------------------- | ------------- | --------------------------------------- |
| Topic metadata and cluster ID                        | All workflows | `DESCRIBE` on topics                    |
| Partition offsets (lag, promotion at zero lag)       | All workflows | `DESCRIBE` on topics                    |
| Lists every consumer group and describes every topic | Conversion    | `DESCRIBE` on all groups and all topics |
| Commits each group's offsets                         | Conversion    | `READ` on all groups and all topics     |

On Confluent Cloud, grant these to the API key's service account with ACLs or the equivalent role bindings.

## Cluster link

kcp doesn't create the link. It must exist, with its mirror topics, before the run.

### REST credential

`linkCredentials` is a cluster-scoped credential, sent to `restEndpoint` as `api_key` plus `api_secret`, `basic`, `bearer`, or `mtls`.

| Call                              | Used by                                                |
| --------------------------------- | ------------------------------------------------------ |
| Describe the link                 | All workflows, `lag-check`                             |
| List mirror topics                | All workflows, `lag-check`                             |
| List link configurations          | All workflows                                          |
| Promote mirror topics             | Static, dynamic                                        |
| Set `consumer.offset.sync.enable` | Static, and only with `consumerOffsetSync.pause: true` |

Dynamic migrations and conversions only read the link. A static migration with `pause: true` writes one configuration, twice: it pauses offset sync after the fence and restores it after the switch. That write happens after the fence, so a credential that can read but not write fails the run mid-cutover. kcp then rolls the fence back, and the rollback restore needs the same write permission.

| Status                                               | What you see                                                                                  |
| ---------------------------------------------------- | --------------------------------------------------------------------------------------------- |
| 401 or 403 describing the link                       | `authentication failed (status N)` followed by a prompt to verify the target REST credentials |
| 404                                                  | `cluster link "<name>" not found on cluster <id>`                                             |
| 401 or 403 on a configuration write                  | `authentication failed (status N) while SET "consumer.offset.sync.enable" ...`                |
| 403 on list mirrors, list configurations, or promote | The raw status and body, for example `unexpected status code 403`. These aren't translated.   |

### The link's own credential

The link reads the source with the credential it was created with. Cutover never reads, sets, or verifies this credential, and can't see it. If it is short, the symptom is mirrors that don't catch up or offsets that don't sync, not a kcp permission error.

The link's credential needs `READ`, `DESCRIBE`, and `DESCRIBE_CONFIGS` on the topics you mirror, and with offset sync on, `READ` and `DESCRIBE` on the consumer groups it syncs. An MSK link uses SCRAM, because a cluster link can't use IAM credentials. On MSK, these are therefore Kafka ACLs.

See [Migrate consumer groups](../../consumer-groups/migration.md) for how this plays out in a static migration.

## Gateway secrets

kcp doesn't hold the destination's credentials during a cutover. The route must already carry `security.cluster.<destination>` authentication, with its secret store and Secrets present, before the run (see [Stage the destination on the Gateway](static-migration.md#1-stage-the-destination-on-the-gateway)). The checks differ by workflow:

- **Static:** the staged block must exist and its referenced Secrets must exist (subject to the optional `secrets` grant above).
- **Dynamic:** kcp doesn't check `security.cluster` or its Secrets. A problem shows up when the operator rejects the patch.
- **Conversion:** the `security.cluster.<destination>` block must exist. kcp doesn't check Secrets.

If the operator rejects the applied CR, for example for a missing `secretRef`, kcp reports `gateway "<name>" was rejected by the Confluent operator` with a `kubectl` command to read the conditions, after a 30 second settle window.

## Conversion credential checks

A conversion probes its Kafka credentials during reconcile, so `--dry-run` finds these before any change. It re-runs them after the fence. The probes use fixed sentinel group and topic names, so they test wildcard-level access: an identity with ACLs scoped to a prefix fails them even if it could cover the groups you have. A failing probe skips the later link checks, so fixing one credential can reveal the next.

| Refusal                                                     | Grant it asks for                                                                                             |
| ----------------------------------------------------------- | ------------------------------------------------------------------------------------------------------------- |
| Source or destination credential can't list every group     | `DESCRIBE` on all consumer groups (an ACL on `*`, or with MSK IAM, the `DescribeGroup` action on every group) |
| Source or destination credential can't describe every topic | `DESCRIBE` on all topics (an ACL on `*`, or with MSK IAM, the `DescribeTopic` action on every topic)          |
| Destination credential can't write consumer group offsets   | `READ` on all consumer groups and all topics (ACLs on `*`)                                                    |

The offset-write probe commits an offset for a group and topic that don't exist and reads the authorization error. The probe doesn't create a group. If a broker accepts it anyway, kcp tells you which group to delete.

## Related content

- [Gateway migrations](overview.md)
- [Idempotency and resumability of Gateway migrations](idempotency.md)
- [Gateway migration limitations](limitations.md)
- [Credentials](../../../migration-assets/cutover/credentials.md)
- [Connections](../../../migration-assets/cutover/connections.md)
