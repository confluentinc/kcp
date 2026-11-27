# migplan-reversible live integration tests

LIVE, local-only integration tests for the `reversibleStaticMigration` reconcile
path (`migplan.Reconcile` and `execute --dry-run`'s plan) against two
`cp-server` clusters joined by a bidirectional cluster link. Gated behind
`//go:build e2e`. Requires Docker and a Go toolchain. There is no gateway: each
test reads a static gateway CR fixture from `testdata/`.

It is its own compose project, on its own ports, so its bidirectional link
can't affect the one-way link of `integration-tests/migplan`.

## Environment

| Cluster | Kafka (PLAINTEXT) | REST v3                  | cluster_id               |
| ------- | ----------------- | ------------------------ | ------------------------ |
| source  | `localhost:19192` | `http://localhost:18190` | `6ub6fPVJRzKjE4i-REkq-A` |
| dest    | `localhost:29192` | `http://localhost:28190` | `LKsbYRvfTM-TVXKjdjgdxA` |

`setup.sh` provisions:

- `rev.a` and `rev.b` (3 partitions) on the source.
- `rev-link`, created on both clusters with `link.mode=BIDIRECTIONAL`,
  `consumer.offset.sync.enable=true`, `consumer.offset.sync.ms=5000` and the
  single `*` `LOCAL_MIRROR` group filter. Dest's copy reaches the source at
  `source:29092`, the source's copy reaches dest at `dest:39092`.
- `rev.a` and `rev.b` mirrored on dest, ACTIVE; the source copy has no rows.
- 100 records on `rev.a`, and group `g-rev` committed part way on the source.
- `rev-half`, created on dest only (the source keeps just a nameless
  `UNMANAGED_SOURCE` placeholder).

The manifest (`testdata/manifest.yaml`) maps domain `cp` to the source and `cc`
to dest. The gateway fixtures bind the static route to `cp` (unfenced or
fenced) or `cc`.

## Tests

Each test that changes cluster state (a reversal, a link config, a group, a
topic) restores it in `t.Cleanup` and waits for the mirror rows to settle, so
the tests run in any order.

| Test | Covers |
| ---- | ------ |
| `TestReversiblePlanTowardTargetLive` | a never-reversed link, route on `cp`: reverse both on the target copy; the real filter value passes |
| `TestReversibleBackOutLive` | `rev.a` reversed, fenced route on `cp`, `writableOn: cp`: reverse `rev.a` back on the source copy, switch `rev.b` |
| `TestReversiblePlanTowardSourceLive` | both reversed, route on `cc`, `writableOn: cp`: reverse both on the source copy |
| `TestReversibleRefusesSwappedDomainsLive` | swapped domain names fail the clients-on-the-writable-copy check |
| `TestReversibleRefusesHalfCreatedLinkLive` | `rev-half` was never created on the source cluster |
| `TestReversibleRefusesOffsetSyncOffLive` | offset sync off on the source copy |
| `TestReversibleRefusesNonStarFilterLive` | a prefixed group filter on the source copy |
| `TestReversibleRefusesActiveGroupOnEnteredSideLive` | `g-rev` has members directly on dest |
| `TestReversibleRefusesOffLinkCommitsLive` | `g-offlink` commits on a source topic off the link |

## Running

```bash
make test-migplan-reversible   # setup, run, teardown
# or by hand:
bash integration-tests/migplan-reversible/setup.sh
go test -tags e2e -count=1 -v ./integration-tests/migplan-reversible/...
bash integration-tests/migplan-reversible/teardown.sh
```
