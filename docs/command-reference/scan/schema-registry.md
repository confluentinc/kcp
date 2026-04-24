---
title: kcp scan schema-registry
---

## kcp scan schema-registry

Scan a schema registry for schemas and versions

### Synopsis

Scan a schema registry (Confluent or AWS Glue) to discover all schemas and their versions. Use --sr-type to select the registry type. Results are added to the state file under schema_registries.

```
kcp scan schema-registry [flags]
```

### Examples

```
  # Confluent Schema Registry, unauthenticated
  kcp scan schema-registry --sr-type confluent --state-file kcp-state.json \
      --url https://my-schema-registry:8081 --use-unauthenticated

  # Confluent Schema Registry, basic auth
  kcp scan schema-registry --sr-type confluent --state-file kcp-state.json \
      --url https://my-schema-registry:8081 \
      --use-basic-auth --username my-user --password my-pass

  # AWS Glue Schema Registry
  kcp scan schema-registry --sr-type glue --state-file kcp-state.json \
      --region us-east-1 --registry-name my-glue-registry
```

### Options

```
  -h, --help                   help for schema-registry
      --password string        The password to use for Basic Authentication
      --region string          The AWS region where the Glue Schema Registry is located.
      --registry-name string   The name of the AWS Glue Schema Registry to scan.
      --sr-type string         Schema registry type: 'confluent' or 'glue'
      --state-file string      The path to the kcp state file.
      --url string             The URL of the schema registry to scan.
      --use-basic-auth         Use Basic Authentication
      --use-unauthenticated    Use Unauthenticated Authentication
      --username string        The username to use for Basic Authentication
```

### Options inherited from parent commands

```
      --verbose   Enable verbose logging to console
```

### AWS IAM Permissions

Only required for `--sr-type glue`. AWS Glue scans use the AWS default credential chain.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "glue:ListSchemas",
        "glue:ListSchemaVersions",
        "glue:GetSchema",
        "glue:GetSchemaByDefinition",
        "glue:GetSchemaVersion",
        "glue:GetRegistry"
      ],
      "Resource": [
        "arn:aws:glue:<AWS REGION>:<AWS ACCOUNT ID>:registry/<REGISTRY NAME>",
        "arn:aws:glue:<AWS REGION>:<AWS ACCOUNT ID>:schema/<REGISTRY NAME>/*"
      ]
    }
  ]
}
```

### SEE ALSO

* [kcp scan](index.md)	 - Scan AWS resources for migration planning

