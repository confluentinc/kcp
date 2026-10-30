# Running a cutover

## Execution model

This manifest drives a resumable migration workflow:

- **`execute`** validates the manifest and live infrastructure, reads the live initial gateway CR to check that the route's mode matches the declared migration type (see [Declaring the migration type](overview.md#declaring-the-migration-type)) — and, on a static route, to derive the bootstrap server id from the target domain — and carries the migration forward through fence → promote → switch (a conversion: fence → verify → sync offsets → switch). Every run reconciles live from the manifest and the current cluster state, so an interrupted run is safely continued by re-running the same command — it resumes from whatever the live world already reflects. It re-reads the manifest (topology and policy) on every invocation.
  - **`--dry-run`** runs only the reconcile step, against the live gateway CR, the source and destination Kafka clusters and the cluster link, and prints the plan: each precondition (the route exists and has the expected mode, the gateway CR is set up the way that mode needs, the cluster link mirrors from the source cluster and `spec.target.clusterId` matches the destination) and a verdict for every selected topic. Nothing is changed and no migration steps run. It exits non-zero if the plan is refused. Flag overrides are applied and validated first, so a dry run rejects an invalid override exactly as a real run would. Useful for iterating on the manifest and your infrastructure before scheduling a live cutover.
- **`lag-check`** is an interactive terminal view of replication lag for every mirror topic on the cluster link, independent of `execute`. It uses only the destination REST leg (`spec.clusterLink.linkCredentials`), so it works before `execute` has ever run.

Every `execute` reconciles the current manifest live against the cluster. A completed migration re-reconciles to nothing to do — `execute` then runs no migration steps and reports that — and an interrupted one continues from the live state. To migrate a different topology, edit the manifest and run `execute` again. `metadata.name` is only a label (it appears in logs and the completion message); changing it does not change what is reconciled.

**Resume an interrupted migration with the same topic set (dynamic routes).**
On a dynamic route, kcp recognises the fence it added by its exact shape: a
`rules.fencing` entry with only `topics` (the migration's exact topic set) and
`blocked: true`. Any other entry, including one over the same topics that also
sets `trafficType` or `topicPatterns`, is an operator's, and kcp leaves it in
place. If a run is interrupted after fencing, resume it with a manifest that
resolves to the same topics. Resuming with a different set — a topic added or
removed, or a `topicPatterns` entry that now also matches a topic created on
the source since the interrupted run — is a different migration: kcp fences
the new set, treats the earlier fence as operator-authored, and leaves it on
the route, so those topics stay blocked after switchover. Finish the
interrupted migration first, then migrate the changed set. If this has already
happened, remove the leftover `blocked` entry from the route's `rules.fencing`
by hand.

**Resume an interrupted conversion.** A route conversion keeps no state
between runs either. kcp recognises its conversion fence by its exact shape:
a `rules.fencing` entry with only `topicPatterns: ['.*']` and `blocked: true`.
If a conversion is interrupted, re-run the same manifest: kcp finds its fence
on the route, re-checks everything and carries on. See
[Interruptions](route-conversion.md#interruptions).

**A static route must not carry someone else's fence.** A static route has a
single route-level `fence`. kcp recognises its own by its exact value,
`{scope: ALL, errorCode: BROKER_NOT_AVAILABLE}`, and refuses a route that
already carries any other fence, because the switchover and a rollback would
remove it: remove that fence before migrating. `scope: NONE` counts as no
fence. An operator's plain `scope: ALL` fence reads back with the same
defaulted `errorCode`, so kcp cannot tell it from its own and would remove it
too; remove it before migrating.

Each field under the declared section's `policies` is a default that a matching
CLI flag can override for a single run, without editing the file.

## Before you run it

- **Connections.** Every `execute` run, including `--dry-run`, connects to the
  source Kafka cluster, the destination Kafka cluster, the cluster-link REST
  API and the Kubernetes API, using the credentials and kubeconfig named in the
  manifest. A `--dry-run` only reads: on Kubernetes it reads the Gateway CR
  (and, for a static migration, checks that the staged Secrets exist). Every
  run, including a dry run, also checks that the gateway pods serve the Gateway
  CR before deciding anything: it reads the CR's status, the gateway Deployment
  and its pods, and, on a gateway with hot reload on, each pod's `/config`
  endpoint. kcp refuses the run when the pods may not match the CR, and shows
  what it observed. The permissions to patch the Gateway and write cluster-link
  configs are first used after the fence, so a real run is the first to find
  one missing. See [Permissions](../../technical-guides/migrations/gateway/permissions.md).
- **Producers must use the gateway.** The fence blocks traffic that goes
  through the gateway. Producers writing to the source directly are not
  blocked, and the check that catches them (`detectUnroutedProducersDuration`)
  is **off by default**. A route conversion does not use
  `detectUnroutedProducersDuration` at all; it has its own
  `detectUnroutedCommitsDuration` instead (see
  [What it does](route-conversion.md#what-it-does)): it watches for consumers committing to the
  source directly, and like the producer check it is **off by default**.
- **What is fenced.** A static route is fenced and switched as a whole — the
  topic selection only decides which mirrors are promoted and lag-checked. A
  dynamic route fences and switches only the selected topics.
- **Promote is the point of no return.** If a step fails after the fence is up
  but before the promote step starts, kcp removes the fence and restores offset
  sync so traffic returns to the source. Once promote starts, kcp never removes
  the fence, even if no topic was promoted yet — doing so would split those
  clients from the target. The same applies from the start of a re-run that
  finds a topic already promoted or promoting. Fix the cause and re-run
  `execute` to roll forward. A
  conversion promotes nothing; its point of no return is the switch (see
  [What it does](route-conversion.md#what-it-does)).
- **Interrupting kcp leaves the fence up.** kcp has no signal handler, so
  Ctrl-C does not remove the fence; the gateway stays fenced until you re-run
  `execute`.
- **No deadline by default.** `rolloutTimeout` defaults to no deadline, so a
  rollout that never converges waits indefinitely, with traffic already fenced.
- **A refused plan changes nothing.** If reconcile refuses the plan (for
  example a selected topic is not a mirror topic on the link), the whole run is
  refused before anything is fenced or changed. `--dry-run` shows the plan and
  the reasons.
