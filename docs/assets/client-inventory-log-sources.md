# Client Inventory: broker-log sources

`kcp scan client-inventory` discovers the clients using an MSK cluster by reading the broker request logs and extracting each client's identity (client ID, topic, role, auth type, principal). It can read those broker logs from **either Amazon S3 or Amazon CloudWatch Logs**. Pick exactly one source per run.

Both sources feed the **same** parser and write the same results into `kcp-state.json`, so the discovered clients are identical for the same set of log lines — only *where the logs are read from* differs.

## Prerequisite (both sources)

The source MSK cluster must be emitting request-level TRACE logs on each broker:

```
kafka.server.KafkaApis=TRACE
```

This is a dynamic broker-logger setting (it takes effect at runtime and resets when a broker restarts). Client discovery only works for the window during which TRACE was active and the logs were delivered to your chosen destination.

## Choosing a source

| | S3 (`--s3-uri`) | CloudWatch Logs (`--log-group-name`) |
|---|---|---|
| Region / cluster name | Derived from the S3 URI | Must be given explicitly (`--region`, `--cluster-name`) |
| Time window | All log files under the prefix | `--start-time` / `--end-time` (default: last 24h) |
| Completeness at TRACE | Complete (whole objects) | **Best-effort** — see the caution below |
| Best for | Complete or historical discovery, bulk runs | Quick, recent windows; when S3 access is hard to grant but logs already flow to CloudWatch |

!!! warning "CloudWatch completeness at TRACE"
    AWS recommends S3 (or Firehose) for DEBUG/TRACE broker logging and cautions that CloudWatch Logs "may continuously deliver a sample of logs." Because client discovery relies on TRACE-level request logs, discovery over CloudWatch is **best-effort** — it can miss clients that were active in the window. Prefer the **S3** source for complete or historical discovery, and use **CloudWatch** for quick, recent checks or to unblock discovery when provisioning S3 access is difficult.

## S3 source

```bash
kcp scan client-inventory \
    --s3-uri s3://my-cluster-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/msk-cluster-xxxx-5/2025-08-13-14/ \
    --state-file kcp-state.json
```

The AWS region and MSK cluster name are parsed from the S3 URI (which follows the `AWSLogs/<account>/KafkaBrokerLogs/<region>/<cluster>/<date>/` layout MSK uses when delivering broker logs to S3).

Required IAM (S3 source):

```json
{
  "Effect": "Allow",
  "Action": ["s3:GetObject", "s3:ListBucket"],
  "Resource": [
    "arn:aws:s3:::<BROKER_LOGS_BUCKET>",
    "arn:aws:s3:::<BROKER_LOGS_BUCKET>/*"
  ]
}
```

## CloudWatch Logs source

```bash
kcp scan client-inventory \
    --log-group-name /aws/msk/my-cluster/broker-logs \
    --region us-east-1 \
    --cluster-name msk-cluster \
    --state-file kcp-state.json
```

Because a CloudWatch log-group name does not encode the region or the MSK cluster name (the way an S3 URI does), you must supply both:

- `--region` — the AWS region of the log group.
- `--cluster-name` — the MSK cluster to attribute discovered clients to. This must match the cluster already present in `kcp-state.json` (run `kcp discover` / cluster scan first).

The time window defaults to the **last 24 hours**. Narrow or shift it with RFC3339 timestamps:

```bash
    --start-time 2025-08-13T14:00:00Z \
    --end-time   2025-08-13T18:00:00Z
```

Required IAM (CloudWatch source):

```json
{
  "Effect": "Allow",
  "Action": ["logs:FilterLogEvents"],
  "Resource": "arn:aws:logs:<AWS REGION>:<AWS ACCOUNT ID>:log-group:<LOG GROUP NAME>:*"
}
```

## Re-runs

Each unique client is keyed by `clientID|topic|role|auth|principal`, so re-running against more logs (from either source) only appends clients that don't already match an existing key. You can safely run the S3 source for a historical sweep and the CloudWatch source for a recent top-up against the same state file.
