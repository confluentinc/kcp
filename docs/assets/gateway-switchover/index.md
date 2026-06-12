# Gateway Switchover Examples for KCP Migrations

These examples illustrate how to configure Confluent Gateway when using KCP to migrate from Amazon MSK to Confluent Cloud. Each example demonstrates a specific combination of source and target authentication methods and provides a representative (but not exhaustive) set of Gateway custom resource configurations for a switchover migration.

## The Switchover Pattern

KCP uses a three-state switchover pattern to migrate clients without reconfiguration. Users author three gateway YAML files — init, fenced, and switchover — then apply the init configuration themselves. KCP automatically applies the fenced and switchover configurations during migration. The init state routes traffic to MSK, the fenced state blocks all client traffic while data is mirrored to Confluent Cloud, and the switchover state routes traffic to Confluent Cloud. Clients continue using their original credentials and endpoints throughout all three states.

## Authentication Combination Matrix

| Source (MSK) Auth | Target (CCloud) Auth | Example |
|---|---|---|
| None | SASL/PLAIN | [switchover-none-to-saslplain](switchover-none-to-saslplain/README.md) |
| None | OAuth | [switchover-none-to-oauth](switchover-none-to-oauth/README.md) |
| None | mTLS | [switchover-none-to-mtls](switchover-none-to-mtls/README.md) |
| mTLS | mTLS | [switchover-mtls-to-mtls](switchover-mtls-to-mtls/README.md) |
| mTLS | OAuth | [switchover-mtls-to-oauth](switchover-mtls-to-oauth/README.md) |
| mTLS | SASL/PLAIN | [switchover-mtls-to-sasl-plain](switchover-mtls-to-sasl-plain/README.md) |
| SASL/SCRAM | SASL/PLAIN | [switchover-sasl-scram-to-sasl-plain](switchover-sasl-scram-to-sasl-plain/README.md) |
| SASL/SCRAM | OAuth | [switchover-sasl-scram-to-oauth](switchover-sasl-scram-to-oauth/README.md) |

## Auth Class Considerations

The three source authentication classes have different implications for how the migration is prepared:

**No-auth source (`none-to-*`)**: The simplest pattern. No client credential management is required on the source side — clients connect without credentials. In the switchover state, the gateway maps the `ANONYMOUS` identity to CCloud credentials via a file store.

**mTLS source (`mtls-to-*`)**: TLS terminates independently on each leg (client→gateway, then gateway→cluster). True passthrough is not possible. In the init and fenced states, the gateway maintains the client's identity by using the same ACM PCA-issued certificate on both legs — MSK sees the same CN on both hops, making the gateway transparent to its ACL system. In the switchover state, the same client certificate is presented to CCloud (mTLS→mTLS), or the cert CN is used to look up CCloud credentials via file store (mTLS→SASL/PLAIN or mTLS→OAuth).

**SASL/SCRAM source (`sasl-scram-to-*`)**: Requires a pre-registration step before the fenced state can be applied. All SCRAM users must be registered via a dedicated pre-registration route while the gateway is in the init state. The gateway intercepts `AlterUserScramCredentials` requests and stores SCRAM verifiers in Vault, which are then used during the switchover state. Vault must be pre-populated with admin credential mappings before the init state is deployed.

## Prerequisites

- CFK operator installed on a Kubernetes cluster
- kubectl configured for the cluster
- Confluent Cloud cluster provisioned
- Familiarity with gateway CR structure and deployment

## External References

- [Confluent Gateway Examples](https://github.com/confluentinc/confluent-kubernetes-examples/tree/master/gateway)
- [Gateway Operator API Reference](https://docs.confluent.io/operator/current/co-api.html#tag/Gateway)
- [CCloud OAuth identity provider setup](https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/oauth/overview.html)
- [CCloud mTLS identity provider setup](https://docs.confluent.io/cloud/current/security/authenticate/workload-identities/identity-providers/mtls/configure.html)
- [Gateway certificates guide](https://github.com/confluentinc/confluent-kubernetes-examples/blob/master/gateway/certificates/README.md)
