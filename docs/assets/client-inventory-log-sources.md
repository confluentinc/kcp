# Discover Clients From MSK Broker Logs

`kcp scan client-inventory` finds the producers and consumers that use an Amazon Managed Streaming for Apache Kafka® (Amazon MSK) cluster by reading the broker request logs. For each client it records the client ID, topic, role, authentication type, and principal in `kcp-state.json`, from broker logs stored in either Amazon S3 or Amazon CloudWatch Logs.

Both sources feed the same parser, so the same log lines produce the same clients. Only the place the logs are read from differs. Choose exactly one source for each run.

The scan can only see what the brokers logged, so you prepare the cluster first:

1. [Deliver broker logs](#1-deliver-broker-logs-to-s3-or-cloudwatch) to S3, CloudWatch Logs, or both.
2. [Turn on TRACE logging](#2-turn-on-trace-logging) for the request logger on every broker.
3. [Let clients show up, then scan](#3-let-clients-show-up-then-scan).
4. [Turn TRACE off](#4-turn-trace-off).

## Choose a source

| | S3 (`--s3-uri`) | CloudWatch Logs (`--log-group-name`) |
|---|---|---|
| Region and cluster name | Derived from the S3 URI | Given with `--region` and `--cluster-name` |
| Time window | All log files under the prefix you give | `--start-time` and `--end-time` (default: the last 24 hours) |
| Completeness at TRACE | Complete | Best effort, see the note below |
| Permissions | `s3:GetObject`, `s3:ListBucket` | `logs:StartQuery`, `logs:GetQueryResults` (Logs Insights, billed per GB scanned) |
| Delivery lag | MSK writes objects about every 5 minutes | Close to real time, with a short ingestion lag |
| Best for | Complete or historical discovery, bulk runs | Quick, recent windows, or when S3 access is hard to grant but logs already flow to CloudWatch |

!!! warning "CloudWatch completeness at TRACE"
    AWS recommends S3 or Firehose for DEBUG and TRACE broker logging, and cautions that CloudWatch Logs "may continuously deliver a sample of logs." Because client discovery relies on TRACE request logs, discovery through CloudWatch is best effort and can miss clients that were active in the window. Use S3 for complete or historical discovery. Use CloudWatch for quick, recent checks.

## Set up the cluster

### 1. Deliver broker logs to S3 or CloudWatch

MSK can deliver broker logs to S3, CloudWatch Logs, or Firehose. kcp reads S3 and CloudWatch Logs. Enable the destination you plan to scan from.

In the AWS Management Console, open the cluster in Amazon MSK, choose the **Properties** tab, find **Broker log delivery**, and choose **Edit**. Enable **Amazon S3** (a bucket and an optional prefix), **Amazon CloudWatch Logs** (a log group), or both. Console labels can change, so for the current steps and the bucket permissions that MSK needs, see the AWS documentation linked under [Related content](#related-content).

To do the same with the AWS CLI, first read the cluster's current version:

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

MSK writes compressed `.log.gz` files to S3 under this path:

```
s3://<bucket>/<prefix>AWSLogs/<account_id>/KafkaBrokerLogs/<region>/<cluster_name>-<cluster_uuid>-<n>/<yyyy-mm-dd-hh>/
```

A bucket prefix that you configured appears before `AWSLogs`, and kcp handles it. To find the exact path to scan, list the bucket:

```bash
aws s3 ls s3://<bucket>/<prefix>AWSLogs/<account_id>/KafkaBrokerLogs/<region>/ --recursive | head
```

In CloudWatch Logs, the broker log lines appear as events in the log group you named.

### 2. Turn on TRACE logging

Client discovery reads the broker's request-level log, which brokers write only at TRACE:

```
kafka.server.KafkaApis=TRACE
```

Set it dynamically on every broker with the Apache Kafka `kafka-configs` tool and the `broker-loggers` entity type. It takes effect immediately and needs no restart. The identity you use needs permission to alter broker configuration on the cluster.

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
- AWS cautions that delivering DEBUG or TRACE logs to CloudWatch can significantly affect broker performance. In a light test, broker CPU didn't change measurably, but a busy production cluster can behave differently. Keep the TRACE window short, and prefer S3 where you can.

### 3. Let clients show up, then scan

kcp records only clients that make a request while TRACE is on. About an hour usually shows the regularly active clients. Clients that connect rarely, such as nightly jobs, need a longer window or a second run later. Results accumulate in the same state file.

- For S3, objects arrive about every 5 minutes, so the newest minutes of your window might not be there yet.
- For CloudWatch, events arrive within a few minutes, so end the window a few minutes before the current time.

### 4. Turn TRACE off

Run this command on every broker where you turned TRACE on:

```bash
kafka-configs.sh --bootstrap-server <broker_endpoint>:9098 --command-config client-iam.properties \
    --entity-type broker-loggers --entity-name <broker_id> \
    --alter --delete-config kafka.server.KafkaApis
```

Logs that were already delivered stay available to scan. In S3 they stay until you delete them, and in CloudWatch until they pass the log group's retention period.

## Scan from S3

```bash
kcp scan client-inventory \
    --s3-uri s3://my-cluster-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-xxxx-5/2025-08-13-14/ \
    --state-file kcp-state.json
```

kcp parses the AWS Region and the cluster name from the S3 URI, which follows the layout MSK uses to deliver broker logs. A bucket prefix before `AWSLogs` is fine:

```
s3://my-bucket/broker-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-xxxx-5/2025-08-13-14/
```

kcp reads every `.log.gz` file under the prefix, including sub-folders, so where the URI ends decides how much it scans:

| URI ends at | Scans |
|---|---|
| `.../msk-cluster-xxxx-5/2025-08-13-14/` | That one hour. |
| `.../msk-cluster-xxxx-5/` | Every hour delivered for that cluster. |

The URI must reach at least the cluster folder, `<cluster_name>-<cluster_uuid>-<n>`, because kcp takes the cluster name from it. A broad prefix over many days of TRACE logs can be a lot of data, so start with the hours when you know TRACE was on.

The S3 source needs this IAM policy:

```json
{
  "Effect": "Allow",
  "Action": ["s3:GetObject", "s3:ListBucket"],
  "Resource": [
    "arn:aws:s3:::<broker_logs_bucket>",
    "arn:aws:s3:::<broker_logs_bucket>/*"
  ]
}
```

## Scan from CloudWatch Logs

```bash
kcp scan client-inventory \
    --log-group-name /aws/msk/my-cluster/broker-logs \
    --region us-east-1 \
    --cluster-name msk-cluster \
    --state-file kcp-state.json
```

A CloudWatch log group name doesn't encode the Region or the cluster name the way an S3 URI does, so you provide both:

- `--region` is the AWS Region of the log group.
- `--cluster-name` is the cluster to attribute the discovered clients to. It must match a cluster already in `kcp-state.json`, so run `kcp discover` first.

### Set the time window

| Rule | Behavior |
|---|---|
| Format | RFC3339, for example `2025-08-13T14:00:00Z`. A timezone offset such as `+01:00` also works. kcp rejects any other format and shows an example. |
| `--end-time` default | The current time. |
| `--start-time` default | 24 hours before the end time. |
| Order | The start must be before the end. |
| One flag alone | Allowed. A lone `--start-time` ends now, and a lone `--end-time` starts 24 hours earlier. |

```bash
kcp scan client-inventory \
    --log-group-name /aws/msk/my-cluster/broker-logs \
    --region us-east-1 \
    --cluster-name msk-cluster \
    --start-time 2025-08-13T14:00:00Z \
    --end-time 2025-08-13T18:00:00Z \
    --state-file kcp-state.json
```

kcp doesn't check the window against the log group's retention period or against the current time. Data older than the retention period is gone, and AWS rejects a query whose window falls outside it. A 24-hour window on a busy TRACE cluster is large. For a first look, use 10 to 60 minutes that ended a few minutes ago.

### How kcp reads CloudWatch logs

kcp runs a CloudWatch Logs Insights query over the window. CloudWatch filters to the TRACE `PRODUCE` and `FETCH` lines and removes duplicates, and returns one representative line for each distinct client. The kcp parser then runs on those lines unchanged. The grouping is never coarser than the kcp client key, `clientID|topic|role|auth|principal`, so distinct clients are never merged.

Keep these points in mind:

- The query is fast and light. On a 10-minute window of a test cluster, kcp downloaded 22 lines in about 3 seconds. Reading every event would have meant 62,771 lines in about 74 seconds.
- Logs Insights charges for the log data that a query scans, so use a narrow window on large log groups.
- A query returns at most 10,000 distinct rows. If a window has more distinct clients than that, the scan fails and asks you to narrow `--start-time` and `--end-time`, rather than save a partial inventory.
- Only data still inside the log group's retention period can be queried.
- If you can't grant the query permissions below, use the S3 source instead.

The CloudWatch source needs this IAM policy:

```json
[
  {
    "Effect": "Allow",
    "Action": ["logs:StartQuery"],
    "Resource": "arn:aws:logs:<aws_region>:<aws_account_id>:log-group:<log_group_name>:*"
  },
  {
    "Effect": "Allow",
    "Action": ["logs:GetQueryResults"],
    "Resource": "*"
  }
]
```

## Troubleshoot a scan

| What you see | Likely cause and fix |
|---|---|
| `No clients discovered; leaving state unchanged` | The scanned logs contain no TRACE request lines. Check that TRACE was on during the window (a broker restart resets it), that log delivery was enabled before then, and that your window or S3 prefix covers that time. |
| An `ERROR failed to extract API requests` line, then `No clients discovered` | Reading the logs failed, for example because of permissions, a window outside retention, or a wrong path. The run still ends with the "no clients" message, so read the ERROR line for the cause. |
| `invalid S3 URI format` | The URI must contain `AWSLogs/<account_id>/KafkaBrokerLogs/<region>/<cluster_name>-<cluster_uuid>-<n>/`. Copy the path from `aws s3 ls`. |
| `insights returned the maximum 10000 rows` | The window has too many distinct clients for one query. Narrow `--start-time` and `--end-time`. |
| An error that the query's end date is before the log group's creation or exceeds its retention | The window is outside the log group's retention period. Use a more recent window, or scan the S3 copy. |
| An error that the cluster isn't in the state file | Run `kcp discover` first, and make sure `--cluster-name` matches the discovered cluster's name. |
| `AccessDenied` from S3 or CloudWatch | Grant the IAM permissions for your source, listed in the sections above. |

## Run more than once

kcp keys each client by `clientID|topic|role|auth|principal`, so a later run against more logs from either source appends only the clients that don't already match an existing key. You can run the S3 source for a historical sweep and the CloudWatch source for a recent top-up against the same state file.

## Related content

- [Getting Started](getting-started.md)
- [`kcp scan client-inventory` command reference](command-reference/scan/client-inventory.md)
- [Source Compatibility](source-compatibility.md)
- AWS Documentation: [Monitor Amazon MSK broker logs](https://docs.aws.amazon.com/msk/latest/developerguide/msk-logging.html)
