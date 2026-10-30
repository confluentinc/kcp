# Migration Sections in the Migration Manifest

Each of `spec.staticMigration`, `spec.dynamicMigration`, and
`spec.dynamicToStaticConversion` names the route to fence and switch over and
the target streaming domain it switches to. The two migrations also carry the
topic selection. `<section>` below stands for whichever one the manifest
declares.

## `spec.<section>.route`

| Field                   | Type       | Required | Notes                                                                                                                              |
| ----------------------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------------------------------ |
| `name`{ #route-name }                  | string     | yes      | A `spec.routes[].name` in the initial Gateway custom resource (CR) to fence and switch over. Must be non-blank (checked when the manifest is read) and exist in the CR (checked at reconcile).                            |
| `targetStreamingDomain`{ #route-targetstreamingdomain } | string     | yes      | The streaming domain this route switches to. On a static route it must be declared in the initial CR's `spec.streamingDomains` with exactly one bootstrap server ID, and the route must already carry pre-staged `security.cluster.<domain>` auth for it (a `secretStore` and an `authentication` block, with the referenced Secrets present). On a dynamic route it must be one of the two domains the route binds. For a conversion (`dynamicToStaticConversion`) it must be one of the two domains the dynamic route binds, and that binding must carry a `bootstrapServerId`: the static route binds to it.    |

## `spec.<staticMigration|dynamicMigration>.topicGroup`

Required for both migrations. A `dynamicToStaticConversion` has none (the
cluster link is its scope). A list validated to exactly one entry
(one route, one migration per file).

| Field           | Type       | Required | Notes                                                                                                            |
| --------------- | ---------- | -------- | ------------------------------------------------------------------------------------------------------------------ |
| `topics`{ #topicgroup-topics }        | `[]string` | see note | A flat list of literal topic names, not globs. A glob is not rejected when the manifest is read. It fails at reconcile as "not found on the source".                                                          |
| `topicPatterns`{ #topicgroup-topicpatterns } | `[]string` | see note | A list of anchored full-match regular expressions (RE2), matched against the topic names on the source cluster. Case-sensitive, and the whole topic name must match. `.` matches any character, so escape it (`\.`) to match a literal dot. `['.*']` selects every source topic. |

**At least one of `topics` or `topicPatterns` is required.** When both are set,
the union is migrated: every literal name plus every source topic a pattern
matches. Every selected topic must be in a state kcp can migrate or resume: a topic
that is not on the source, not on the cluster link, present on the destination
without being a mirror, or whose mirror is in a failed or unexpected state
refuses the run, while a topic an earlier run already promoted or switched is
resumed or skipped. The refusal is all-or-nothing: nothing is fenced or
changed. If nothing is left to migrate, `execute` reports that and does
nothing. Omitting
`topics` does not select every topic: to migrate every source topic, write an
explicit match-all pattern, `topicPatterns: ['.*']`.

`topicPatterns` are resolved against the live source on every run, so the
selected set can change between runs without the manifest changing. On a
dynamic route, that matters when you resume an interrupted migration (see
[Execution model](running-a-cutover.md#execution-model)).

The route's mode is not declared separately: the section you declare
fixes the mode the route must have, and kcp reads the live CR on every execute
run to check it. It uses the route's own `mode` field when the CR sets one.
Otherwise, it infers the mode from the binding: a named singular `streamingDomain` ⇒
static, a non-empty plural `streamingDomains` ⇒ dynamic. A route with both or
neither is refused, as is a route whose mode disagrees with the declared
section. On a static route, the bootstrap server ID is derived from the
target domain's declaration in the live CR, not written in the manifest.
`kcp migration cutover execute` runs the workflow that matches the declared type: a
whole-route cutover for `staticMigration`, topic by topic for
`dynamicMigration`.

A dynamic route has two further requirements, both checked at reconcile (so
`--dry-run` reports them): it must bind exactly two streaming domains (the
source and the target), with `coordination.group` pinned to the source domain,
and the cluster link's consumer offset sync must already be disabled, which
is why `spec.dynamicMigration` has no `consumerOffsetSync` section.

`lag-check` ignores the topic selection entirely and always watches every mirror
topic.

## `spec.staticMigration.consumerOffsetSync`

Optional, static migrations only.

| Field      | Type   | Required     | Default | Notes |
| ---------- | ------ | ------------ | ------- | ----- |
| `pause`{ #consumeroffsetsync-pause }    | bool   | no           | `false` | Disable the link's `consumer.offset.sync.enable` right after fencing, and set it back to `baseline` after the switch. If a run stops before that restore, the restore is still owed and re-running `execute` performs it. It is also restored if a failure before promote rolls the fence back. |
| `baseline`{ #consumeroffsetsync-baseline } | string | when pausing | —       | `enabled` or `disabled`: the value `consumer.offset.sync.enable` is set back to after the switch. |
