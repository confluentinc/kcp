# Comment reference table

Verdicts from manual review; the standard is `.claude/rules/code-comments.md`. Add rows as reviews continue.

## Good

| Location | Comment | Why it's fine |
|---|---|---|
| `cmd/create_asset/migration_infra/cmd_create_asset_migration_infra.go:745` | `// Strip the port suffix (e.g., ":9096" or ":9094")` | Decodes `strings.Cut` with discarded results; example values help |
| `internal/types/state.go:50` | `// Copy existing MSK data or initialize empty` | The `make` + `copy` deep-copy idiom isn't obvious at a glance |
| `internal/sources/osk/osk_source.go:26` | `// Type returns the source type` | Exported method: a one-line, name-first doc follows Go convention |
| `internal/migrate/acls/reconciler.go:57` | (one-line doc) | One line |
| `internal/services/gateway/gateway_test.go:430` | `// Surge in progress: old pod still there + a new ready pod; Deployment not settled.` | Explains a niche, in-the-moment test scenario |
| `internal/services/report/report_service_test.go:241` | (one line) | Fine |
| `integration-tests/migrate/acls_native_test.go:691` | (brief) | Says why those test lines exist |
| `internal/services/hcl/reverse_proxy_hcl_service.go:102` | `// Internet gateway data source` | A few words signposting a clear block of config |
| `internal/client/kafka_admin.go:171` | `// MSK presents Amazon's public CA, so no custom CA pool — just honor skip-verify.` | A real WHY for the `nil` argument; trim to `// MSK presents Amazon's public CA, so no custom CA pool.` |
| `utils/transaction-discovery/internal/txnlog/decode.go:23` | `// Key is a decoded __transaction_state record key.` | Exported type, name-first per Go convention; names the Kafka internal topic it decodes |
| `internal/services/clusterlink/clusterlink.go:273` | `// ValidateTopics validates that all specified topics exist in cluster link` | Exported method, name-first per Go convention |
| `internal/migrate/source_test.go:62` | `// Mirrors the clusterLink reconciler: CheckPreconditions probes, Plan reads.` | Explains why ClusterID is deliberately called twice |
| `internal/manifest/schemagen/schemagen_test.go:204` | `// assertNoKey fails if key appears anywhere in the nested map/slice structure.` | "Anywhere in nested" says it recurses; drop the name |
| `integration-tests/migration/migration_e2e_test.go:492` | `// mirrorTopicStatus is a single entry from the cluster link's mirrors listing.` | Says where the data comes from; drop the name |
| `internal/manifest/gateway.go:155` | 3-line field doc | Shorten to 2: `// Port of the gateway's /config endpoint, polled to confirm a revision applied.` / `// 0 uses the persisted value, else the gateway default (9180).` The zero value and default are a contract |
| `internal/services/hcl/confluent/private_link_attachment.go:24` | 2-line doc | Shorten to 1: `// Enterprise clusters use this instead of GeneratePrivateLinkAttachmentResource.` Keeps the when-to-use fact |
| `internal/services/plan/engine_inputs.go:81` | 2-line doc on a one-liner | Shorten to 1: `// Every key a customer can set in plan-inputs.yaml.` |
| `integration-tests/migrate/migrate_topics_mirror_destination_test.go:919` | `// Case 14 — DATA FLOW: produce → mirror → consume` | The label gives a readable at-a-glance context per test; drop the dashed rule lines |
| `internal/targets/credentials_test.go:240` | 2-line doc restating `TestHTTPClient_NotDefaultClient` | A one-line test description is standard; say what the name doesn't: `// Its TLS settings must not leak into the process-wide default client.` |
| `internal/services/plan/engine/profile.go:48` | 3-line networking section comment | Shorten to 1: `// Networking: PublicEndpointsOK is read first, then the legacy fields; all default to private.` Plain wording ("is read first", not "wins over") |
| `internal/migrate/mirrortopics/reconciler.go:161` | 3 lines incl. `(§8.6)` | Shorten to 1: `// Report-only: fixing a broken mirror is a deliberate operator action.` |
| `integration-tests/migration/manifest_fixture_render_test.go:389` | 3-line test doc opening with the name | Shorten to 1: `// Keeps a secret-bearing file out of the process table and unreadable by group.` |
| `integration-tests/migration-hot-reload/hot_reload_e2e_test.go:37` | 2 lines on `convergeTimeout = 90s` | Shorten to 1: `// Convergence measured ~4s on 2 replicas; generous but still fails fast.` The measured basis of a magic number |
| `integration-tests/migration/migration_e2e_test.go:1063` | "…there is no separate init step anymore, so writeManifestToPod moves here" | Rewrite in current terms, don't delete: `// Execute with the pause flag; it registers the migration itself, so write the manifest here.` |
| `internal/services/migration/workflow.go:300` | 4 lines with "still needed" / "no longer done here" | Keep the fact, drop the history: `// Cluster link configs are needed for the PauseConsumerOffsetSync precondition below` / `// and for the offset-sync restore bookend's diff baseline.` |
| `cmd/discover/testhelpers_test.go:117` | `// ── stubEC2Service ───…` + "(1 method)" | Drop the banner and the count that will go stale: `// Implements ClusterDiscovererEC2Service.` |
| `internal/types/discovery_msk.go:24` | `// internal only - exclude from JSON output` above `json:"-"` | Keep: the tag's meaning isn't obvious to every reader |
| `internal/services/migration/state.go:93` | `// Source cluster configuration` / `// Destination cluster configuration` | Struct group labels; they also say the `Cluster*` fields are the destination |
| `integration-tests/migration/migration_e2e_test.go:1018` | 18-line doc with a "Scope note" essay | Shorten to 2, keeping the fact that protects the test: `// Proves the drain path runs end-to-end, not that it reduces duplicates:` / `// that depends on the link's async sync timing, which this harness can't observe.` |
| `cmd/scan/self_managed_connectors/cmd_scan_self_managed_connectors_test.go:44` | `// --- abuse / error paths (R4): bad combinations must fail fast ---` | A one-line section header grouping the tests below; keep it, drop only `(R4)` |
| `integration-tests/migration/migration_e2e_test.go:749` | `// --- Step 2b: … ---` plus 3 lines | Keep the step header (it breaks up a long test); cut the rest to `// Success requires each mirror to reach STOPPED, not just an accepted promote.` |
| `integration-tests/migration/migration_e2e_test.go:1795` | `// --- Phase B: repair the drift, resume completes ---` | One line describing a test phase; fine |
| `internal/services/plan/observations_test.go:140` | 2-line test description | Fine as is; a one-liner lost too much |
| `cmd/create_asset/migration_infra/serverless_test.go:191` | 3 lines incl. "covers change 2" | Shorten to 1: `// Serverless only supports --type 5, because it offers SASL/IAM authentication only.` |
| `utils/transaction-discovery/internal/discovery/consumeroffsets_test.go:11` | 3-line test doc | Shorten to 1: `// Versions 0/1 are offset commits carrying a topic; version 2 is group metadata with none.` |
| `cmd/scan/self_managed_connectors/self_managed_connectors_scanner_test.go:383` | "(Task 1.3), so this no longer errors" | Present tense: `// No error: SetConnectClusterMetrics creates the ConnectCluster entry if missing.` |
| `utils/transaction-discovery/internal/discovery/accumulator.go:9` | 2-line type doc | Shorten to 1, name-first (exported): `// TxnFootprint is everything observed for one transactional id across all samples and sources.` |
| `internal/sources/interface.go:11` | One-line, name-first docs on each interface method | Exported interface methods follow Go convention; keep |

## Bad

| Location | Problem | Better |
|---|---|---|
| `internal/services/migration/workflow.go:766` | 10 lines narrating behaviour across scenarios and code paths | At most one WHY line |
| `internal/services/migration/tbm/workflow.go:421` | Talks about `config.Topics`, which isn't in the code below | Delete; a comment must be about the code it sits on |
| `internal/utils/tls.go:10`, `:56` | Doc comments describing several scenarios | One line or none |
| `internal/types/credentials_migrate.go:157` | Half the first line repeats the method name; 3 more lines restate it | Delete; the name says it |
| `cmd/migration/execute/cmd_migration_execute_test.go:254` | `// --- drift check (§13) ---`: plan section reference, unclear referent | Delete |
| `integration-tests/migrate/acls_native_test.go` (file header) | Cites "Design §9"; huge opening block | Condense; no plan references |
| `internal/services/gateway/gateway_test.go:685` | First line is the test name; 4 lines describe the test below; review-ticket reference | Keep only the stakes: "A false rejection costs an investigation; a false acceptance reports a refused CR as migrated." |
| `internal/migrate/mirrortopics/reconciler_test.go:550` (and neighbours) | Describes the whole test, which the name and code already show | Delete unless the scenario is genuinely niche |
| `internal/manifest/validate.go:80`, `:113` | 12-line doc on a self-explanatory function; "Finding 2(a) / task-7"; a test should carry the edge cases | Delete |
| `internal/migrate/acls/iam_effective.go:13`, `:35` | ~90 lines: documentation-sized design rationale in a doc comment | Delete; rationale goes in the PR/design doc |
| `cmd/migration/execute/cmd_migration_execute.go:471` | 5 lines of change history ("now resolves", "no special case remains") | Delete |
| `internal/services/hcl/confluent/schema_test.go:49` (and neighbours) | `// Verify depends_on ordering: …` says exactly what the assert below does | Delete all "Verify X" lines |
| `internal/services/migration/gateway_transition_test.go:323` | Restates the configId contract, also stated in the next row and on `NewConfigID` | Delete; state it once, on `NewConfigID` |
| `internal/services/gateway/configid_test.go:37` | Same contract again; the subtest name says it | Delete |
| `cmd/migration/execute/cmd_migration_execute_test.go:300` | Restates the test name; narrates the bug's history | Delete; history goes in the fix's commit message |
| `cmd/ui/frontend/src/hooks/useRegionCostsData.ts:264` | `// Filter table data by the selected service` above a plain `.filter` | Delete; the code reads itself |
| `cmd/ui/frontend/src/types/api/metrics.ts:67` | 3-line JSDoc restating `MetricsQueryParams` | Collapse to one line, `/** Query parameters for the metrics API. */`, or delete |
| `cmd/create_asset/migration_infra/cmd_create_asset_migration_infra_test.go:59` | `// R3/R6: legacy cc value no longer accepted (clean break).` (Claude-co-authored, plan IDs) | Delete; the case name `"legacy cc rejected"` says it |

## Patterns

- **The WHAT test**: a one-line WHAT is good when the code needs decoding (stdlib idioms, copies, regexes) or is a few words signposting a distinct config/setup block, and bad when the code reads itself (plain `if`/loops).
- **Scenario narration**: walking through behaviours per code path or input. The code shows that.
- **Detached comments**: describing things not in the code directly below.
- **Name restating**: exported functions, types and interface methods get a one-line, name-first doc (Go convention). Tests and unexported identifiers never open with their name. Re-explaining the name at length is always bad.
- **Test descriptions**: one line above a test is standard and fine if it adds what the name doesn't (usually the stakes). Never a paragraph, never the name again.
- **Plan leakage**: task, finding and review-ticket numbers, plan sections (`§13`, "Design §9"), requirement and acceptance IDs (`R3/R6`, `R2-H1`, `AE4`), "change 2". These IDs come from feature plans that aren't in the repo; strip the ID and keep the requirement sentence if it still says something. A test's own step labels (`Step 2b`, `Phase B`) are fine.
- **"Verify X" lines**: a comment saying what the assert below checks. Delete.
- **One fact, several places**: state a contract once, where it's enforced; tests and callers don't repeat it.
- **Banners**: no full-width rule lines (`// -----`, `// ── x ─────`). A one-line `// --- section ---` header that groups what follows is fine.
- **IDE hover**: method and type docs show on hover (gopls, GoLand, `go doc`), alongside the signature.
- **History → present tense**: if a history comment hides a fact that still matters, restate the fact as current behaviour rather than deleting it.
- **Non-obvious syntax counts**: explaining struct tags like `json:"-"` is decoding, not WHAT.
- **Plain wording**: literal phrases ("is read first") over idioms ("wins over").
- **TypeScript**: JSDoc is a convention, not a requirement; use the one-line `/** … */` form, and only when it says something.
- **Docs in code**: rationale long enough to be a document belongs in the PR or design doc.

## Next round

- `internal/services/plan/questions.go:26`: proposed `// A selectable answer; Token is the value written in plan-inputs.yaml, Label its display wording.`
- `integration-tests/migration/migration_e2e_test.go:1696`: paragraph above the Phase B test.
