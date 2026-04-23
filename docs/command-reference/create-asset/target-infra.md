---
title: kcp create-asset target-infra
---

## kcp create-asset target-infra

Create a target infrastructure asset

### Synopsis

Create Terraform assets for Confluent Cloud target infrastructure including environment, cluster, and private link setup. Infrastructure provisioning is controlled by --needs-environment, --needs-cluster and --needs-private-link.

```
kcp create-asset target-infra [flags]
```

### Examples

```
  # Full provision from a kcp-state file (creates environment, cluster and private link)
  kcp create-asset target-infra \
      --state-file kcp-state.json \
      --source-cluster-id arn:aws:kafka:us-east-1:XXX:cluster/my-cluster/abc-5 \
      --needs-environment --env-name example-env \
      --needs-cluster --cluster-name example-cluster --cluster-type enterprise \
      --needs-private-link --subnet-cidrs 10.0.0.0/16,10.0.1.0/16,10.0.2.0/16 \
      --output-dir confluent-cloud-infrastructure

  # Reuse an existing environment + cluster, only wire up private link
  kcp create-asset target-infra \
      --aws-region us-east-1 --vpc-id vpc-xxxxxxxx \
      --env-id env-abc123 --cluster-id lkc-xyz789 --cluster-type dedicated \
      --needs-private-link --subnet-cidrs 10.0.0.0/16,10.0.1.0/16,10.0.2.0/16
```

### Options

```
      --aws-region string             AWS region for the infrastructure (required when --state-file is not provided)
      --cluster-availability string   Cluster availability: 'SINGLE_ZONE' or 'MULTI_ZONE' (default "SINGLE_ZONE")
      --cluster-cku int               Number of CKUs for dedicated clusters (MULTI_ZONE requires >= 2) (default 1)
      --cluster-id string             ID of existing cluster (required without --needs-cluster)
      --cluster-name string           Name for new cluster (required with --needs-cluster)
      --cluster-type string           Cluster type: 'dedicated' or 'enterprise' (required with --needs-cluster)
      --env-id string                 ID of existing environment (required without --needs-environment)
      --env-name string               Name for new environment (required with --needs-environment)
  -h, --help                          help for target-infra
      --needs-cluster                 Create a new cluster (requires --cluster-name and --cluster-type)
      --needs-environment             Create a new environment (requires --env-name)
      --needs-private-link            Setup private link (requires --subnet-cidrs). Required for Enterprise clusters.
      --output-dir string             Output directory for generated Terraform files (default "target_infra")
      --prevent-destroy               Set lifecycle { prevent_destroy = true } on resources (use --prevent-destroy=false to disable) (default true)
      --source-cluster-id string      The ARN of the MSK cluster (required when --state-file is provided).
      --state-file string             Path to kcp state file (if provided, vpc-id and aws-region are extracted from state)
      --subnet-cidrs strings          Subnet CIDRs for private link (required with --needs-private-link)
      --use-existing-route53-zone     Use an existing Route53 hosted zone instead of creating a new one
      --vpc-id string                 VPC ID (required when --state-file is not provided)
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

`kcp create-asset target-infra` itself only reads local configuration. The generated Terraform provisions Confluent Cloud resources (environment, cluster, private link attachment) and — when `--needs-private-link` is set — AWS networking resources (VPC endpoint, security group rule, and optionally a Route53 hosted zone with alias records). The executor of `terraform apply` needs a policy equivalent to:

> **TODO:** Populate from an `iamlive` capture against the generated Terraform. Enterprise (`--cluster-type enterprise`) and Dedicated (`--cluster-type dedicated`) paths use different Confluent networking primitives and may require different AWS permissions — document each variant if they diverge.

```json
{}
```

### SEE ALSO

* [kcp create-asset](index.md)	 - Generate infrastructure and migration assets

