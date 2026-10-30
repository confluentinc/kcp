# Converting a dynamic route to static (`spec.dynamicToStaticConversion`)

A topic-based migration leaves its route dynamic: each migrated topic is
routed to the destination by a routing condition, and consumer-group
coordination stays pinned to the source. Once every topic on the cluster link
is migrated, `spec.dynamicToStaticConversion` replaces the dynamic route with a static route
bound to the destination and copies the consumer groups' committed offsets
there, so group coordination moves too. Run it when the last batch has
switched.

```yaml
spec:
  dynamicToStaticConversion:
    route:
      name: migration-route
      targetStreamingDomain: confluent-cloud
```

A conversion selects no topics: the cluster link is its scope. The rest of
the manifest (`source`, `target`, `clusterLink`, `gateway`) is the topic-based
migration's; see the [conversion example](examples.md#route-conversion).

## What is checked

`execute` and `--dry-run` check, against the live gateway CR, both clusters
and the cluster link:

- **The route.** It is dynamic and binds exactly two streaming domains, with
  `coordination.group` on the source; `targetStreamingDomain` is one of them
  and its binding carries a `bootstrapServerId`; the route carries
  `security.cluster.<targetStreamingDomain>` auth; it has no fence kcp didn't
  write and no route-level fence. The cluster link's consumer offset sync is
  disabled.
- **The cluster link is the scope.** A route pairs with one link, so the link
  must be the one every batch on the route used: topics migrated over another
  link are outside the scope, and a group that commits on one refuses the
  conversion. Every mirror topic on the link must be
  promoted (`STOPPED`), named as on the source (no `cluster.link.prefix`),
  present on the destination with the same partition count as on the source,
  and routed to the destination by the dynamic route. At least one topic must
  be promoted.
- **Consumer groups.** A source group whose committed offsets are all on link
  topics is in scope, and its offsets are copied. A group with any committed
  offset on a topic that is not on the link refuses the conversion: after the
  switch that topic is not reachable through the route, or the group belongs
  to a client outside it. An in-scope group with live members on the
  destination refuses too; one that exists there with no members gets a
  warning, because its offsets will be overwritten. Source topics that are not
  on the link and that no group commits on get a warning: after the switch the
  route sends their traffic to the destination.
- **Credentials.** The source and destination credentials must be able to
  list every consumer group and describe every topic (a credential that sees
  part of a cluster would hide a group or a topic from these checks), and the
  destination credential must be able to write consumer-group offsets (`READ`
  on the groups and the topics). On an MSK IAM source, listing every group and
  describing every topic need the `kafka-cluster:DescribeGroup` and
  `kafka-cluster:DescribeTopic` actions on every group and topic resource.

Keep the cluster link and the source topics until the conversion has run. The
link's mirror topics are the conversion's only record of what to move, so a
deleted link leaves it nothing to read and the run fails with an error reading
the link's mirror topics rather than a purpose-written refusal. Deleting a source
topic purges its groups' committed offsets, so there is nothing to copy for it.

## What it does

1. **Fence.** kcp adds its conversion fence, `{topicPatterns: ['.*'],
   blocked: true}`, at the head of the route's `rules.fencing`, blocking every
   topic on the route.
2. **Verify.** On data read after the fence, kcp re-runs the route checks
   that decide whether the route can be converted (dynamic, two domains,
   `coordination.group` on the source, `targetStreamingDomain` one of them,
   consumer offset sync disabled), confirms its own conversion fence is still
   on the route, and re-runs the credential, cluster-link scope and
   consumer-group checks. It does not repeat the checks on the route's
   contents (the target binding's `bootstrapServerId`, the
   `security.cluster.<targetStreamingDomain>` auth, no fence kcp didn't write,
   no route-level fence) or the warnings about topics outside the link; those
   run before the fence. It then takes two snapshots of every source group's
   committed offsets, `detectUnroutedCommitsDuration` apart, but only when it
   is set (minimum `10s`; the default, `0s`, skips the check and the first
   snapshot is the one synced). A change means a client is committing to the
   source directly, bypassing the gateway, and the run fails.
3. **Sync offsets.** kcp writes the in-scope groups' committed offsets, with
   their metadata, to the destination. It refuses the whole sync if an offset
   is past its destination partition's high-water mark.
4. **Switch.** kcp replaces the route with a static route bound to
   `targetStreamingDomain`, with no `rules` (so no fence) and no
   `streamingDomains`; `security` and the rest of the route are kept.

**The switch is the point of no return.** A failure before it removes the
fence and leaves the route as it was; offsets already written to the
destination have no effect until the switch. Each gateway change is confirmed
on every gateway pod, as in a migration.

## What clients see

- **A pause.** Every topic on the route is blocked from the fence to the
  switch: about `detectUnroutedCommitsDuration` (if set) plus two gateway reloads.
  Producers retry (give them a `delivery.timeout.ms` longer than that);
  consumers fetch nothing and can't commit.
- **A rejoin, and possibly a re-read.** After the switch, consumers' group
  coordinator is on the destination. Each consumer's first commit or heartbeat
  there fails (the destination doesn't know its member), so it
  rejoins its group and resumes from the copied offset. It may read again what
  it fetched between the switch and that first failed commit or heartbeat — up
  to one poll (`max.poll.records`) for a consumer that commits after every
  batch, and up to the backlog the fence built for one that auto-commits — and
  what it consumed just before the fence but couldn't commit. This is expected
  at-least-once behaviour; nothing is skipped.
- **KIP-848 consumers** (`group.protocol=consumer`) can't use a dynamic route
  at all; they work once the route is static.

## Refusals and how to fix them

| Refusal | Fix |
|---|---|
| A link topic's mirror is `ACTIVE`, not promoted | Migrate it in a topic-based migration batch, or promote it if nothing uses it. |
| A link topic's promotion is still in progress | Wait for it to reach `STOPPED`, then re-run. |
| The route sends a link topic to the source | Finish that topic's batch (re-run `execute` on its manifest) before converting. |
| A link topic has more partitions on the destination than on the source | Add partitions to the source copy to match, set the affected groups' offsets on the new partitions, then re-run. |
| A mirror is named differently on the destination | Conversions don't support a cluster link with `cluster.link.prefix`. |
| No topic on the link is promoted | Migrate the route's topics first: a conversion closes a topic-based migration. |
| A group commits on a topic that is not on the link | Migrate that topic; or delete the group's stale offsets on it (`kafka-consumer-groups --delete-offsets`), or the group; or stop or move the application outside this route that owns it. |
| An in-scope group has members on the destination | Stop the destination members first. |
| A credential can't list every group, describe every topic, or write offsets | Grant what the refusal names, then re-run. |
| The route has a fence kcp didn't write | Find out what it is for first. If it is an unfinished topic-based migration batch's, finish that batch (rerun `kcp migration cutover execute` on its manifest): removing its fence would send producers back to a source topic the link no longer mirrors. Otherwise remove it: the conversion would drop it. |
| The route has a route-level fence | Remove it: the static route would enforce it. |
| Committed offsets changed after the fence | A client commits to the source directly. Send it through the gateway, then re-run. |

A refusal before the fence changes nothing, and `--dry-run` shows it.

## Interruptions

- **A killed run leaves the fence up.** kcp has no signal handler, so the
  route keeps blocking every topic until you re-run the same manifest, which
  resumes.
- **A refusal with the fence up warns.** If a re-run is refused while kcp's
  fence is on the route, kcp says so: the route keeps blocking every topic
  until you fix the refusal and re-run, or remove kcp's entry from
  `rules.fencing` by hand.
- **A failed switch keeps the fence.** If the static route never reached the
  gateway CR, fix the cause and re-run to finish. If it reached the CR but
  couldn't be confirmed, check the Gateway (operator acceptance, pods) instead
  of re-running: a re-run would report nothing to do.
- **A finished conversion re-runs as nothing to do.**
