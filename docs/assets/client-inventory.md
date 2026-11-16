# Discover Clients on an MSK Cluster

`kcp scan client-inventory` lists the producers and consumers that use an Amazon Managed Streaming for Apache Kafka® (Amazon MSK) cluster. This guide takes you from a cluster with no logging to a populated client list: you set up the AWS side, run the scan, and read the results.

Use the list before a migration to see which applications have to move to Confluent Cloud, and which IAM principals need access there.

## What you get

For each client, kcp records the following in `kcp-state.json`, under the cluster's `discovered_clients`:

| Field | Meaning |
|---|---|
| `client_id` | The client ID that the application sets. |
| `topic` | The topic the client produces to or consumes from. |
| `role` | `Producer` or `Consumer`. |
| `auth` | The authentication type: `IAM`, `SASL_SCRAM`, `TLS`, or `UNAUTHENTICATED`. |
| `principal` | The identity the client authenticates as, such as an IAM role ARN. |

A client that uses several topics appears once for each topic. Client inventory works for Amazon MSK clusters only.

## How it works

Brokers write a log line for each produce and fetch request, but only when the `kafka.server.KafkaApis` logger is set to TRACE. kcp reads those lines from the broker logs that MSK delivers to Amazon S3 or Amazon CloudWatch Logs. You therefore prepare the cluster in this order:

1. [Create the log destination](#1-create-the-log-destination).
2. [Turn on broker log delivery](#2-turn-on-broker-log-delivery).
3. [Turn on TRACE logging](#3-turn-on-trace-logging).
4. [Let clients show up](#4-let-clients-show-up).
5. [Run the scan](#5-run-the-scan).
6. [Review the results](#6-review-the-results).
7. [Turn TRACE off](#7-turn-trace-off).

kcp sees only the clients that made a request while TRACE was on and logs were being delivered.

## Before you begin

- A `kcp-state.json` file that contains the cluster. If you don't have one, run `kcp discover` first.
- The AWS CLI, configured for the account and AWS Region of the cluster.
- The Apache Kafka command-line tools, which provide `kafka-configs.sh`. If the cluster uses IAM authentication, also download the `aws-msk-iam-auth` library.
- Permissions for each task, as described in the following table.

| Task | Permissions the identity needs |
|---|---|
| Create the destination | `s3:CreateBucket` for S3, or `logs:CreateLogGroup` and `logs:PutRetentionPolicy` for CloudWatch Logs. |
| Turn on log delivery | The permissions in the AWS managed policy `AmazonMSKFullAccess`. For an S3 destination, also `s3:PutBucketPolicy`. |
| Turn TRACE on and off | Permission to alter broker configuration on the cluster. |
| Run the scan | The S3 or CloudWatch policy in [Scan MSK Broker Logs for Clients](client-inventory-log-sources.md). |

## 1. Create the log destination

MSK doesn't create the destination for you, so it must exist before you enable delivery. Choose S3, CloudWatch Logs, or both. To decide, see [Choose a source](client-inventory-log-sources.md#choose-a-source). In short, S3 gives a complete inventory and suits historical runs, and CloudWatch Logs suits quick checks of recent activity.

To create an S3 bucket, run the following command. For a Region other than `us-east-1`, add `--create-bucket-configuration LocationConstraint=<region>`.

```bash
aws s3api create-bucket --bucket <broker_logs_bucket> --region <region>
```

If the bucket uses server-side encryption with a customer managed AWS KMS key, add the following statement to the key policy so that MSK can write logs to the bucket:

```json
{
  "Sid": "Allow Amazon MSK to use the key.",
  "Effect": "Allow",
  "Principal": { "Service": ["delivery.logs.amazonaws.com"] },
  "Action": [
    "kms:Encrypt",
    "kms:Decrypt",
    "kms:ReEncrypt*",
    "kms:GenerateDataKey*",
    "kms:DescribeKey"
  ],
  "Resource": "*"
}
```

To create a CloudWatch Logs log group, run the following commands. TRACE logs are large, so set a short retention period to limit storage costs.

```bash
aws logs create-log-group --log-group-name <log_group_name> --region <region>
```

```bash
aws logs put-retention-policy --log-group-name <log_group_name> --retention-in-days 7 --region <region>
```

## 2. Turn on broker log delivery

You can use the AWS Management Console or the AWS CLI.

### Use the console

1. Open the cluster in Amazon MSK and choose the **Properties** tab.
2. In the **Log delivery** section, choose **Edit**.
3. Enable **Amazon S3** and enter the bucket and an optional prefix, enable **Amazon CloudWatch Logs** and enter the log group, or enable both.
4. Save the changes.

Console labels can change. For the current steps, see the AWS documentation linked under [Related content](#related-content).

### Use the AWS CLI

First read the cluster's current version:

```bash
aws kafka describe-cluster-v2 --cluster-arn <cluster_arn> --query 'ClusterInfo.CurrentVersion' --output text
```

Then enable delivery. The logging block replaces the existing one, so include every destination you want to keep, not only the one you add:

```bash
aws kafka update-monitoring \
    --cluster-arn <cluster_arn> \
    --current-version <current_version> \
    --logging-info '{
      "BrokerLogs": {
        "S3":             { "Enabled": true, "Bucket": "<broker_logs_bucket>", "Prefix": "broker-logs/" },
        "CloudWatchLogs": { "Enabled": true, "LogGroup": "<log_group_name>" }
      }
    }'
```

Changing the monitoring settings is a cluster update that takes about 5 minutes and can restart brokers. A restart resets the TRACE setting from the next step, so wait for the update to finish before you turn TRACE on.

To confirm that delivery is enabled, run:

```bash
aws kafka describe-cluster-v2 --cluster-arn <cluster_arn> --query 'ClusterInfo.Provisioned.LoggingInfo'
```

The `BrokerLogs` block in the output shows `"Enabled": true` for each destination you configured.

kcp reads broker logs only. The authorizer logs that MSK can also deliver aren't used by client inventory.

### Where the logs land

In S3, MSK writes compressed `.log.gz` files about every 5 minutes, under this path:

```
s3://<bucket>/<prefix>AWSLogs/<account_id>/KafkaBrokerLogs/<region>/<cluster_name>-<cluster_uuid>-<n>/<yyyy-mm-dd-hh>/
```

To find the exact path to scan, list the bucket:

```bash
aws s3 ls s3://<bucket>/<prefix>AWSLogs/<account_id>/KafkaBrokerLogs/<region>/ --recursive | head
```

In CloudWatch Logs, the broker log lines appear as events in the log group you named.

## 3. Turn on TRACE logging

Brokers write request-level lines only at TRACE:

```
kafka.server.KafkaApis=TRACE
```

Set it dynamically on every broker with the `kafka-configs` tool and the `broker-loggers` entity type. It takes effect immediately and needs no restart.

This example uses IAM authentication. If your cluster uses a different authentication method, use your usual client configuration instead. First put the MSK IAM authentication library on the classpath and create the client configuration file:

```bash
export CLASSPATH=<path_to>/aws-msk-iam-auth-<version>-all.jar

cat > client-iam.properties <<'EOF'
security.protocol=SASL_SSL
sasl.mechanism=AWS_MSK_IAM
sasl.jaas.config=software.amazon.msk.auth.iam.IAMLoginModule required;
sasl.client.callback.handler.class=software.amazon.msk.auth.iam.IAMClientCallbackHandler
EOF
```

Then run the following command for each broker ID (1, 2, 3, and so on). Use port 9098 from inside the virtual private cloud (VPC), or port 9198 for the public IAM endpoint:

```bash
kafka-configs.sh --bootstrap-server <broker_endpoint>:9098 \
    --command-config client-iam.properties \
    --entity-type broker-loggers --entity-name <broker_id> \
    --alter --add-config kafka.server.KafkaApis=TRACE
```

To confirm the setting on a broker, describe it:

```bash
kafka-configs.sh --bootstrap-server <broker_endpoint>:9098 --command-config client-iam.properties \
    --entity-type broker-loggers --entity-name <broker_id> --describe | grep KafkaApis
```

Before you turn TRACE on, consider these points:

- The setting applies at runtime only. A broker restart, patch, or cluster update resets it to INFO. If a scan finds nothing, check that TRACE is still on.
- TRACE is verbose. On a lightly loaded cluster of three brokers it produced about 14 MB of log data each minute. The volume grows with the request rate, and so do S3 storage and CloudWatch ingestion costs.
- AWS cautions that delivering DEBUG or TRACE logs to CloudWatch Logs can significantly affect broker performance. In a light test, broker CPU didn't change measurably, but a busy production cluster can behave differently. Keep the TRACE window short, and prefer S3 where you can.

## 4. Let clients show up

kcp records only clients that make a request while TRACE is on. About an hour usually shows the regularly active clients. Clients that connect rarely, such as nightly jobs, need a longer window or a second run later. Results accumulate in the same state file.

- For S3, objects arrive about every 5 minutes, so the newest minutes of your window might not be there yet.
- For CloudWatch Logs, events arrive within a few minutes, so end the window a few minutes before the current time.

## 5. Run the scan

To scan from S3, point `--s3-uri` at the folder of logs for the period you want:

```bash
kcp scan client-inventory \
    --s3-uri s3://<bucket>/<prefix>AWSLogs/<account_id>/KafkaBrokerLogs/<region>/<cluster_name>-<cluster_uuid>-<n>/<yyyy-mm-dd-hh>/ \
    --state-file kcp-state.json
```

To scan from CloudWatch Logs, give the log group, the Region, the cluster name, and a time window:

```bash
kcp scan client-inventory \
    --log-group-name <log_group_name> \
    --region <region> \
    --cluster-name <cluster_name> \
    --start-time <yyyy-mm-ddThh:mm:ssZ> \
    --end-time <yyyy-mm-ddThh:mm:ssZ> \
    --state-file kcp-state.json
```

For the time-window rules, the IAM policies, and the limits of each source, see [Scan MSK Broker Logs for Clients](client-inventory-log-sources.md).

## 6. Review the results

To list the clients for one cluster from the state file, run:

```bash
jq -r '.msk_sources.regions[].clusters[]
       | select(.name == "<cluster_name>")
       | (.discovered_clients // [])[]
       | [.client_id, .topic, .role, .auth, .principal] | @tsv' kcp-state.json
```

Each line shows the client ID, topic, role, authentication type, and principal:

```
orders-service    orders    Producer    IAM    arn:aws:sts::000123456789:assumed-role/orders-producer-role/<session_name>
```

To browse the same data in a web interface, start the kcp UI and open the **Clients** tab of the cluster:

```bash
kcp ui --state-file kcp-state.json
```

If the list is empty or shorter than you expect, see [Troubleshoot a scan](client-inventory-log-sources.md#troubleshoot-a-scan).

## 7. Turn TRACE off

Run this command on every broker where you turned TRACE on:

```bash
kafka-configs.sh --bootstrap-server <broker_endpoint>:9098 --command-config client-iam.properties \
    --entity-type broker-loggers --entity-name <broker_id> \
    --alter --delete-config kafka.server.KafkaApis
```

Logs that MSK already delivered stay available to scan. In S3 they stay until you delete them, and in CloudWatch Logs until they pass the log group's retention period. To stop delivery, disable the destinations in the same way you enabled them.

## Use the results

The discovered IAM principals can drive the creation of Confluent Cloud ACLs. When you pass `--state-file` and `--cluster-id` to `kcp iac acls iam generate`, kcp reads the IAM principals of the discovered clients and converts their permissions into Confluent Cloud ACLs, so you don't have to list roles by hand. For the command and its options, see [kcp iac acls iam generate](command-reference/iac/acls/iam/generate.md).

## Related content

- [Scan MSK Broker Logs for Clients](client-inventory-log-sources.md)
- [`kcp scan client-inventory` command reference](command-reference/scan/client-inventory.md)
- [Getting Started](getting-started.md)
- [Source Compatibility](source-compatibility.md)
- AWS Documentation: [Amazon MSK logging](https://docs.aws.amazon.com/msk/latest/developerguide/msk-logging.html)
