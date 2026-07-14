//go:build integration

// This file is the LIVE integration harness for the native-ACL MSK→Confluent
// Cloud migration plane (design doc §9: "Integration testing", the
// highest-priority part of that work). It is Tier 2 of the two-tier test
// strategy (§9.0): Tier 1 is the hermetic, exhaustive, no-cluster suite in
// internal/migrate/acls and internal/migrate/serviceaccounts (make
// test-migrate-acls); this file is the smaller, representative-and-boundary
// live suite that proves the same behaviour against a REAL MSK source and a
// REAL Confluent Cloud target (make test-migrate-acls-live).
//
// Env-gating: this file adds no new env vars — it reuses loadCloudConfig
// (migrate_cloud_test.go), the same CC_*/MSK_* gate every other live test in
// this package uses. Every test here calls loadCloudConfig(t) first and
// therefore SKIPS cleanly (not fails) whenever those vars are unset — e.g.
// under `make test-go`, `make test-migrate` (the docker matrix), or any CI
// run without live creds.
//
// The oracle (design §9.6 — avoid tautology): every scenario's expected
// outcome is either (a) a literal, hand-decided classification — "this
// specific seeded tuple is created verbatim" / "dropped entirely" / "host
// normalized to *" — reasoned from the design doc, not from calling
// NormalizeForCC and checking it against itself; or (b) for the
// principal-shape/SA-naming matrix, computed by oracleDeriveDisplayName
// below, a SEPARATE re-implementation of design §6's naming rule written
// directly from the design text. Neither path calls into
// internal/migrate/acls' NormalizeForCC or internal/migrate/serviceaccounts'
// DeriveDisplayName/naming.go. The harness seeds the source, runs the real
// `kcp migrate apply` (via runKCP, migrate_clusterlink_test.go), and reads
// back the ACTUAL result from Confluent Cloud (Kafka REST v3 ACLs, IAM v2
// service accounts) via the same client packages the product uses to talk to
// those wire APIs (macls.ACLClient, msa.CCClient) — reusing an HTTP client is
// not reusing the logic under test.
//
// Isolation & teardown (design §9.9): every seeded principal is built from
// uniqueACLToken(), a per-call, per-process-run unique token, and
// spec.acls.include scopes each manifest to just that token's principal —
// so concurrent/repeated live runs never sweep in another run's (or another
// pre-existing) ACL. Teardown is deferred immediately after each resource is
// known to exist (before the run, where possible), so it executes even on
// test failure, and it deletes exactly what this test created: the seeded
// source ACL(s), any created CC ACL(s), and any created CC service account —
// never anything pre-existing.
package migrate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/IBM/sarama"
	macls "github.com/confluentinc/kcp/internal/migrate/acls"
	msa "github.com/confluentinc/kcp/internal/migrate/serviceaccounts"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
	"github.com/confluentinc/kcp/internal/targets"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Independent oracle: SA display-name derivation (design §6 / §9.4 / §9.6).
//
// This is a SEPARATE re-implementation of the naming rule, transcribed
// directly from the design document's prose — it does NOT call
// internal/migrate/serviceaccounts.DeriveDisplayName (naming.go). Comparing
// against the product's own function would only prove the product agrees
// with itself; comparing against an independently-written transcription of
// the same written spec is what can actually catch naming.go diverging from
// the design (e.g. a wrong truncation length, a hash computed over the wrong
// substring, a charset typo). The two implementations are necessarily
// similar in SHAPE — they both encode the same documented algorithm — but
// neither line of this file was copied from, or calls into, naming.go.
// ---------------------------------------------------------------------------

var (
	oracleValidNameRe = regexp.MustCompile(`^[A-Za-z0-9]([A-Za-z0-9._:-]*[A-Za-z0-9])?$`)
	oracleDisallowed  = regexp.MustCompile(`[^A-Za-z0-9._:-]`)
	oracleEdgeTrim    = regexp.MustCompile(`^[^A-Za-z0-9]+|[^A-Za-z0-9]+$`)
)

// oracleIsValidCCName reports whether s (already stripped of "User:") is a
// valid Confluent Cloud service-account display_name verbatim: starts and
// ends alphanumeric, interior chars restricted to [A-Za-z0-9._:-], length <=
// 64.
func oracleIsValidCCName(s string) bool {
	return s != "" && len(s) <= 64 && oracleValidNameRe.MatchString(s)
}

// oracleHash8 is the independent 8-hex-char (32-bit) suffix: sha256 of the
// FULL principal string exactly as passed in (i.e. including its "User:"
// prefix, per design §6's "computed over the FULL original principal").
func oracleHash8(principal string) string {
	sum := sha256.Sum256([]byte(principal))
	return hex.EncodeToString(sum[:])[:8]
}

// oracleDeriveDisplayName computes the expected CC service-account
// display_name for a source principal per design §6's check-then-hash rule.
func oracleDeriveDisplayName(principal string) string {
	name := strings.TrimPrefix(principal, "User:")
	if oracleIsValidCCName(name) {
		return name
	}
	const maxLen = 64
	base := oracleEdgeTrim.ReplaceAllString(oracleDisallowed.ReplaceAllString(name, "-"), "")
	if len(base) > maxLen-9 { // 9 = "-" + 8 hex
		base = strings.TrimRight(base[:maxLen-9], "._:-")
	}
	if base == "" {
		base = "sa"
	}
	return base + "-" + oracleHash8(principal)
}

// oracleDescriptionFor is the audit/collision-check description every
// auto-created service account must carry (design §6), written here as a
// literal rather than imported from serviceaccounts.descriptionFor.
func oracleDescriptionFor(principal string) string {
	return "kcp:source-principal=" + principal
}

// ---------------------------------------------------------------------------
// Isolation: unique tokens for principals/resources (design §9.9).
// ---------------------------------------------------------------------------

// aclTokenSeq hands out monotonic, concurrency-safe sequence numbers for this
// file's unique tokens, mirroring linkSeqCh/uniqueLinkName
// (migrate_clusterlink_test.go).
var aclTokenSeq = func() chan int {
	ch := make(chan int)
	go func() {
		for i := 1; ; i++ {
			ch <- i
		}
	}()
	return ch
}()

// uniqueACLToken returns a fresh token, unique for this run (runID) and this
// call, kept deliberately SHORT (no scenario name embedded) so that
// principal-shape scenarios asserting a verbatim (no-hash) display name stay
// comfortably under the 64-char CC limit regardless of which scenario calls
// it. It is alphanumeric-and-hyphen, so it is always itself a valid CC
// display-name fragment.
func uniqueACLToken() string {
	return fmt.Sprintf("kcpacl%s-%d", runID, <-aclTokenSeq)
}

// ---------------------------------------------------------------------------
// MSK-side plumbing: seed/delete a native ACL with an arbitrary principal
// STRING (design §9.0 — no mTLS PKI needed to exercise principal shapes).
// ---------------------------------------------------------------------------

// seedNativeACL creates one native ACL on the MSK source via the IAM admin
// principal (broad kafka-cluster:* rights, same admin cloud_msk_admin_test.go
// uses). Works for ANY principal string, including DN-shaped ones: a Kafka
// ACL principal is just a string, no certificate or identity needs to exist
// for it to be created or read back.
func seedNativeACL(t *testing.T, admin sarama.ClusterAdmin, rt sarama.AclResourceType, resourceName string, pt sarama.AclResourcePatternType, principal, host string, op sarama.AclOperation, perm sarama.AclPermissionType) {
	t.Helper()
	res := sarama.Resource{ResourceType: rt, ResourceName: resourceName, ResourcePatternType: pt}
	acl := sarama.Acl{Principal: principal, Host: host, Operation: op, PermissionType: perm}
	require.NoError(t, admin.CreateACL(res, acl), "seed native ACL %s %s host=%s on %s %q for %s", perm.String(), op.String(), host, rt.String(), resourceName, principal)
}

// deleteNativeACL removes exactly the one native-ACL tuple seedNativeACL
// created, via a precise (never wildcard) filter, so teardown cannot touch
// any other ACL on the shared live MSK cluster. Best-effort: logs rather than
// fails, so teardown always completes even if the ACL is already gone.
func deleteNativeACL(t *testing.T, admin sarama.ClusterAdmin, rt sarama.AclResourceType, resourceName string, pt sarama.AclResourcePatternType, principal, host string, op sarama.AclOperation, perm sarama.AclPermissionType) {
	t.Helper()
	filter := sarama.AclFilter{
		ResourceType:              rt,
		ResourceName:              &resourceName,
		ResourcePatternTypeFilter: pt,
		Principal:                 &principal,
		Host:                      &host,
		Operation:                 op,
		PermissionType:            perm,
	}
	if _, err := admin.DeleteACL(filter, false); err != nil {
		t.Logf("cleanup: delete native ACL %s %s on %s %q for %s: %v", perm.String(), op.String(), rt.String(), resourceName, principal, err)
	}
}

// canonicalACL builds the canonical types.Acls tuple sarama's own enum
// String() methods produce for this ACL — the same titlecase shape
// ReadNativeACLs (internal/migrate/acls/read.go) emits. Scenarios use it to
// state their expected post-migration tuple (host already normalized,
// principal already rewritten) by hand, independent of calling
// NormalizeForCC.
func canonicalACL(rt sarama.AclResourceType, resourceName string, pt sarama.AclResourcePatternType, principal, host string, op sarama.AclOperation, perm sarama.AclPermissionType) types.Acls {
	return types.Acls{
		ResourceType:        rt.String(),
		ResourceName:        resourceName,
		ResourcePatternType: pt.String(),
		Principal:           principal,
		Host:                host,
		Operation:           op.String(),
		PermissionType:      perm.String(),
	}
}

// ---------------------------------------------------------------------------
// CC-side plumbing: read-back (List/FindByDisplayName, the product's own
// wire clients used only to READ, never to compute an expectation) and
// teardown (raw REST calls the product deliberately has no Delete method
// for — ACLClient and CCClient are additive-only by design).
// ---------------------------------------------------------------------------

// aclTargetClients bundles the CC target's REST endpoints for this file's
// read-back and teardown calls. Built from the SAME target-creds.yaml (and so
// the same auth) buildACLReconcilers gives the product's own clients
// (cmd/migrate/apply/cmd_migrate_apply.go) — proving the two indepedently
// observe the same target state, not sharing any translation logic.
type aclTargetClients struct {
	hc           clusterlink.HTTPClient
	restEndpoint string
	clusterID    string
}

func newACLTargetClients(t *testing.T, cfg cloudConfig, targetCredsPath string) aclTargetClients {
	t.Helper()
	creds, err := targets.LoadCredentials(targetCredsPath)
	require.NoError(t, err)
	hc, err := creds.HTTPClient()
	require.NoError(t, err)
	return aclTargetClients{hc: hc, restEndpoint: cfg.ccRestEndpoint, clusterID: cfg.ccClusterID}
}

func (c aclTargetClients) listACLs(t *testing.T) []types.Acls {
	t.Helper()
	acls, err := macls.NewACLClient(c.restEndpoint, c.clusterID, c.hc).List(context.Background())
	require.NoError(t, err)
	return acls
}

func (c aclTargetClients) findSA(t *testing.T, displayName string) *msa.ServiceAccount {
	t.Helper()
	sa, err := msa.NewCCClient(msa.DefaultBaseURL, c.hc).FindByDisplayName(context.Background(), displayName)
	require.NoError(t, err)
	return sa
}

func (c aclTargetClients) createSA(t *testing.T, displayName, description string) *msa.ServiceAccount {
	t.Helper()
	sa, err := msa.NewCCClient(msa.DefaultBaseURL, c.hc).Create(context.Background(), displayName, description)
	require.NoError(t, err)
	return sa
}

// cleanupSAAndItsACLs deletes every CC ACL referencing "User:"+the found
// service account's id, then the service account itself. A no-op (not an
// error) if no service account with displayName exists — safe to call
// unconditionally from a defer at the top of a test, before the run that
// might create it.
func (c aclTargetClients) cleanupSAAndItsACLs(t *testing.T, displayName string) {
	t.Helper()
	sa := c.findSA(t, displayName)
	if sa == nil {
		return
	}
	principal := "User:" + sa.ID
	for _, a := range aclsForPrincipal(c.listACLs(t), principal) {
		if err := deleteCCACL(context.Background(), c.hc, c.restEndpoint, c.clusterID, a); err != nil {
			t.Logf("cleanup: delete CC acl %+v: %v", a, err)
		}
	}
	if err := deleteCCServiceAccount(context.Background(), c.hc, sa.ID); err != nil {
		t.Logf("cleanup: delete CC service account %s (%s): %v", sa.ID, displayName, err)
	}
}

func aclsForPrincipal(all []types.Acls, principal string) []types.Acls {
	var out []types.Acls
	for _, a := range all {
		if a.Principal == principal {
			out = append(out, a)
		}
	}
	return out
}

func aclsForResourceName(all []types.Acls, resourceName string) []types.Acls {
	var out []types.Acls
	for _, a := range all {
		if a.ResourceName == resourceName {
			out = append(out, a)
		}
	}
	return out
}

// Teardown-only wire-enum maps for the Kafka REST v3 ACL delete-by-filter
// call. Deliberately duplicated (not imported) from internal/migrate/acls'
// private tables: the product's ACLClient (Task 8/9) has no Delete method by
// design (strictly additive, design §12 decision 1) — teardown must talk to
// the wire API directly.
var (
	teardownResourceTypeWire = map[string]string{"Topic": "TOPIC", "Group": "GROUP", "Cluster": "CLUSTER", "TransactionalID": "TRANSACTIONAL_ID"}
	teardownPatternTypeWire  = map[string]string{"Literal": "LITERAL", "Prefixed": "PREFIXED"}
	teardownOperationWire    = map[string]string{
		"Read": "READ", "Write": "WRITE", "Create": "CREATE", "Delete": "DELETE", "Alter": "ALTER",
		"Describe": "DESCRIBE", "ClusterAction": "CLUSTER_ACTION", "DescribeConfigs": "DESCRIBE_CONFIGS",
		"AlterConfigs": "ALTER_CONFIGS", "IdempotentWrite": "IDEMPOTENT_WRITE",
	}
	teardownPermissionWire = map[string]string{"Allow": "ALLOW", "Deny": "DENY"}
)

// deleteCCACL removes one ACL from the CC target via the Kafka REST v3 bulk
// delete-by-filter endpoint (DELETE .../acls?resource_type=...&...).
func deleteCCACL(ctx context.Context, hc clusterlink.HTTPClient, restEndpoint, clusterID string, a types.Acls) error {
	q := url.Values{}
	q.Set("resource_type", teardownResourceTypeWire[a.ResourceType])
	q.Set("resource_name", a.ResourceName)
	q.Set("pattern_type", teardownPatternTypeWire[a.ResourcePatternType])
	q.Set("principal", a.Principal)
	q.Set("host", a.Host)
	q.Set("operation", teardownOperationWire[a.Operation])
	q.Set("permission", teardownPermissionWire[a.PermissionType])
	u := strings.TrimRight(restEndpoint, "/") + "/kafka/v3/clusters/" + url.PathEscape(clusterID) + "/acls?" + q.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, res.Body); _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("unexpected status %d deleting CC acl %+v: %s", res.StatusCode, a, body)
	}
	return nil
}

// deleteCCServiceAccount removes one CC service account via IAM v2
// (msa.CCClient likewise has no Delete method — additive-only by design).
func deleteCCServiceAccount(ctx context.Context, hc clusterlink.HTTPClient, id string) error {
	u := msa.DefaultBaseURL + "/iam/v2/service-accounts/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	res, err := hc.Do(req)
	if err != nil {
		return err
	}
	defer func() { _, _ = io.Copy(io.Discard, res.Body); _ = res.Body.Close() }()
	if res.StatusCode != http.StatusOK && res.StatusCode != http.StatusNoContent && res.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(res.Body)
		return fmt.Errorf("unexpected status %d deleting CC service account %s: %s", res.StatusCode, id, body)
	}
	return nil
}

// ---------------------------------------------------------------------------
// Manifest generation for an acls-bearing migration (no clusterLink/topics
// section — spec.acls alone satisfies runApply's "something to apply" gate).
// ---------------------------------------------------------------------------

// aclManifestOpts configures spec.serviceAccounts + spec.acls for one
// scenario's manifest.
type aclManifestOpts struct {
	autoCreate bool
	mapping    map[string]string
	include    []string
	exclude    []string
}

// writeACLManifest writes a source-read-only (no cluster link) MSK→CC
// migration manifest with a spec.acls (+ spec.serviceAccounts) section, and
// returns its path. sourceBootstrap/sourceCreds are the LIVE MSK read
// connection (IAM, per writeCloudCreds(..., "iam")); targetCreds is the CC
// api_key/api_secret creds file.
func writeACLManifest(t *testing.T, dir, name string, cfg cloudConfig, sourceBootstrap []string, sourceCreds, targetCreds string, opts aclManifestOpts) string {
	t.Helper()
	// Double-quoted YAML scalars throughout: principals under test can carry
	// YAML-significant characters (":", "="), and quoting once, uniformly,
	// avoids ever having to reason about which values are "safe" unquoted.
	yq := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	yamlList := func(ss []string) string {
		if len(ss) == 0 {
			return "[]"
		}
		quoted := make([]string, len(ss))
		for i, s := range ss {
			quoted[i] = yq(s)
		}
		return "[" + strings.Join(quoted, ",") + "]"
	}

	var b strings.Builder
	b.WriteString("apiVersion: kcp.confluent.io/v1alpha1\nkind: Migration\nmetadata:\n  name: " + name + "\nspec:\n")
	b.WriteString("  source:\n    type: msk\n    bootstrapServers: " + yamlList(sourceBootstrap) + "\n    credentials: " + sourceCreds + "\n")
	b.WriteString("  target:\n    type: confluent-cloud\n    clusterId: " + cfg.ccClusterID + "\n    credentials: " + targetCreds + "\n    kafka:\n      restEndpoint: " + cfg.ccRestEndpoint + "\n")
	b.WriteString(fmt.Sprintf("  serviceAccounts:\n    autoCreate: %v\n", opts.autoCreate))
	if len(opts.mapping) > 0 {
		b.WriteString("    mapping:\n")
		keys := make([]string, 0, len(opts.mapping))
		for k := range opts.mapping {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			b.WriteString("      " + yq(k) + ": " + yq(opts.mapping[k]) + "\n")
		}
	}
	b.WriteString("  acls:\n    include: " + yamlList(opts.include) + "\n")
	if len(opts.exclude) > 0 {
		b.WriteString("    exclude: " + yamlList(opts.exclude) + "\n")
	}

	mf := filepath.Join(dir, name+".yaml")
	require.NoError(t, os.WriteFile(mf, []byte(b.String()), 0600))
	return mf
}

// ---------------------------------------------------------------------------
// Data-driven native-ACL + principal-shape scenario runner (design §9.2/§9.4).
// ---------------------------------------------------------------------------

// nativeACL is one seeded native-ACL tuple (principal is supplied separately
// by the scenario runner, since every seed entry in a scenario shares it).
type nativeACL struct {
	ResourceType sarama.AclResourceType
	ResourceName string
	PatternType  sarama.AclResourcePatternType
	Operation    sarama.AclOperation
	Permission   sarama.AclPermissionType
	Host         string
}

// aclScenario is one row of the native-ACL / principal-shape matrix: seed a
// single principal's native ACL(s) and declare, by hand, which of them
// survive migration to CC (expectSeedIdx) — every other seeded index is
// expected to be dropped entirely. principalSuffix is appended verbatim
// after a fresh uniqueACLToken() to build the seeded principal, letting
// principal-shape scenarios control the exact shape (DN-like, email-like,
// >64 chars, ...) while native-ACL scenarios leave it empty (a plain,
// always-verbatim token).
type aclScenario struct {
	name            string
	principalSuffix string
	seed            []nativeACL
	expectSeedIdx   []int
}

// nativeMatrixScenarios covers design §9.2: resource types (Topic, Group,
// Cluster, TransactionalID), pattern types (LITERAL, PREFIXED, the
// LITERAL-"*" all-of-type wildcard), operations including invalid-on-CC
// combos, ALLOW and DENY, host normalization, operation-implication de-dup,
// and two drop-list items (ClusterAction here; the broker-principal drop is
// covered by TestACLsLive_NativeMatrix_BrokerPrincipalDropped below, since it
// needs a principal SHAPE rather than a plain token).
var nativeMatrixScenarios = []aclScenario{
	{
		name: "topic_literal_read_allow_host_star",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0},
	},
	{
		name: "topic_literal_write_allow_specific_host_normalizes",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationWrite, Permission: sarama.AclPermissionAllow, Host: "203.0.113.5"},
		},
		expectSeedIdx: []int{0}, // host normalized to "*" in the expected tuple below
	},
	{
		name: "topic_dedup_describe_redundant_dropped",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"},
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationDescribe, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0}, // Describe dropped: implied by Read
	},
	{
		name: "topic_dedup_alterconfigs_describeconfigs_dropped",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationAlterConfigs, Permission: sarama.AclPermissionAllow, Host: "*"},
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationDescribeConfigs, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0}, // DescribeConfigs dropped: implied by AlterConfigs
	},
	{
		name: "topic_prefixed_deny_specific_host_normalizes",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicPre-", PatternType: sarama.AclPatternPrefixed, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionDeny, Host: "203.0.113.9"},
		},
		expectSeedIdx: []int{0}, // DENY preserved 1:1; host normalized to "*"
	},
	{
		name: "topic_literal_wildcard_star_allow",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "*", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0},
	},
	{
		name: "group_read_allow_valid",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceGroup, ResourceName: "kcpacl-group1", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0},
	},
	{
		name: "group_write_invalid_on_cc_dropped",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceGroup, ResourceName: "kcpacl-group2", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationWrite, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		// Write is not in Group's CC-valid set {Read, Describe, Delete}: dropped.
	},
	{
		name: "transactionalid_write_allow_valid",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTransactionalID, ResourceName: "kcpacl-txn1", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationWrite, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0},
	},
	{
		name: "transactionalid_read_invalid_on_cc_dropped",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTransactionalID, ResourceName: "kcpacl-txn2", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		// Read is not in TransactionalID's CC-valid set {Describe, Write}: dropped.
	},
	{
		name: "cluster_clusteraction_droplist_dropped",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceCluster, ResourceName: "kafka-cluster", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationClusterAction, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		// ClusterAction is drop-list (design §5 rule 4): inter-broker only.
	},
}

// principalMatrixScenarios covers the naming half of design §9.4 — the
// simple "created, correctly named" cases. The collision pair, the
// User:*/ANONYMOUS skip, and the mapping overrides each need bespoke setup
// (two related principals, or a fixed literal principal, or pre-existing
// identities) and so are their own dedicated tests below, not table rows
// here. Every row uses the SAME fixed ACL shape (Topic/LITERAL/Read/Allow/*)
// so the only variable under test is the principal shape.
var principalMatrixScenarios = []aclScenario{
	{
		name:            "clean_username_verbatim_no_hash",
		principalSuffix: "-app-consumer",
		seed:            []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-principalTopic", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx:   []int{0},
	},
	{
		name:            "clean_dotted_username_verbatim_no_hash",
		principalSuffix: ".app.consumer",
		seed:            []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-principalTopic", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx:   []int{0},
	},
	{
		name:            "mtls_dn_shaped_sanitized_and_hashed",
		principalSuffix: "-CN=payments,OU=svc,O=acme",
		seed:            []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-principalTopic", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx:   []int{0},
	},
	{
		name:            "kerberos_email_shaped_sanitized_and_hashed",
		principalSuffix: "-svcacct@REALM.EXAMPLE",
		seed:            []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-principalTopic", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx:   []int{0},
	},
	{
		name:            "over_64_chars_truncated_and_hashed",
		principalSuffix: "-" + strings.Repeat("x", 70),
		seed:            []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-principalTopic", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx:   []int{0},
	},
}

// runACLScenario seeds s's native ACL(s) under a fresh unique principal,
// applies an acls-only manifest scoped to just that principal, and asserts
// the resulting CC state (service account + ACL set) exactly matches s's
// hand-declared expectation. Teardown is deferred immediately (before the
// run), so it always executes.
func runACLScenario(t *testing.T, cfg cloudConfig, cc aclTargetClients, admin sarama.ClusterAdmin, dir, target, sourceRead string, s aclScenario) {
	t.Helper()
	base := uniqueACLToken()
	principal := "User:" + base + s.principalSuffix

	for _, sd := range s.seed {
		sd := sd
		seedNativeACL(t, admin, sd.ResourceType, sd.ResourceName, sd.PatternType, principal, sd.Host, sd.Operation, sd.Permission)
		defer deleteNativeACL(t, admin, sd.ResourceType, sd.ResourceName, sd.PatternType, principal, sd.Host, sd.Operation, sd.Permission)
	}

	displayName := oracleDeriveDisplayName(principal)
	defer cc.cleanupSAAndItsACLs(t, displayName)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-"+s.name), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		include:    []string{"User:" + base + "*"},
	})

	out, err := runKCP(t, mf)
	require.NoError(t, err, out)

	sa := cc.findSA(t, displayName)
	require.NotNil(t, sa, "expected a service account named %q for principal %q", displayName, principal)
	require.Equal(t, oracleDescriptionFor(principal), sa.Description)

	resolved := "User:" + sa.ID
	var expected []types.Acls
	for _, idx := range s.expectSeedIdx {
		sd := s.seed[idx]
		expected = append(expected, canonicalACL(sd.ResourceType, sd.ResourceName, sd.PatternType, resolved, "*", sd.Operation, sd.Permission))
	}
	actual := aclsForPrincipal(cc.listACLs(t), resolved)
	require.ElementsMatch(t, expected, actual, "CC ACL set for principal %q (scenario %q)", principal, s.name)
}

func TestACLsLive_NativeMatrix(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	for _, s := range nativeMatrixScenarios {
		s := s
		t.Run(s.name, func(t *testing.T) {
			runACLScenario(t, cfg, cc, admin, dir, target, sourceRead, s)
		})
	}
}

func TestACLsLive_PrincipalMatrix(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	for _, s := range principalMatrixScenarios {
		s := s
		t.Run(s.name, func(t *testing.T) {
			runACLScenario(t, cfg, cc, admin, dir, target, sourceRead, s)
		})
	}
}

// TestACLsLive_NativeMatrix_BrokerPrincipalDropped covers the second
// drop-list item from design §9.2: an MSK-broker-shaped principal (the
// broker's own mTLS identity, recognized by internal/migrate/acls'
// isBrokerPrincipal) is dropped in its entirety, regardless of the ACL's
// operation/resource. The uniqueness token has to live INSIDE the
// broker-shaped principal (it can't be a prefix, or the regex
// `^User:CN=b-\d+\..*\.kafka\..*$` wouldn't match), so this needs its own
// setup rather than the generic principalSuffix-after-token shape.
func TestACLsLive_NativeMatrix_BrokerPrincipalDropped(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	token := uniqueACLToken()
	principal := "User:CN=b-1." + token + ".kafka.us-east-1.amazonaws.com"
	resourceName := "kcpacl-broker-topic"

	seedNativeACL(t, admin, sarama.AclResourceTopic, resourceName, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourceName, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-broker"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		include:    []string{"*" + token + "*"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)

	require.Empty(t, aclsForResourceName(cc.listACLs(t), resourceName), "an MSK-broker-shaped principal's ACL must be dropped entirely, never migrated")
}

// TestACLsLive_PrincipalMatrix_CollisionPair covers the collision case from
// design §9.4: two DISTINCT principals that sanitize+truncate to the SAME
// 55-char base must still resolve to two DISTINCT service accounts (distinct
// hash8 suffixes) — never merged.
func TestACLsLive_PrincipalMatrix_CollisionPair(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	base := uniqueACLToken()
	longCommon := strings.Repeat("z", 60)
	principalA := "User:" + base + "-CN=" + longCommon + "-alpha"
	principalB := "User:" + base + "-CN=" + longCommon + "-beta"

	nameA := oracleDeriveDisplayName(principalA)
	nameB := oracleDeriveDisplayName(principalB)
	require.Len(t, nameA, 64)
	require.Len(t, nameB, 64)
	require.Equal(t, nameA[:55], nameB[:55], "test setup: expected both derived names to share an identical truncated base (the collision this scenario is meant to exercise)")
	require.NotEqual(t, nameA, nameB, "distinct principals sharing a truncated base must still derive distinct display names (distinct hash8)")

	resourceName := "kcpacl-collision-topic"
	for _, p := range []string{principalA, principalB} {
		p := p
		seedNativeACL(t, admin, sarama.AclResourceTopic, resourceName, sarama.AclPatternLiteral, p, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
		defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourceName, sarama.AclPatternLiteral, p, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	}
	defer cc.cleanupSAAndItsACLs(t, nameA)
	defer cc.cleanupSAAndItsACLs(t, nameB)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-collision"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		include:    []string{"User:" + base + "*"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)

	saA := cc.findSA(t, nameA)
	saB := cc.findSA(t, nameB)
	require.NotNil(t, saA)
	require.NotNil(t, saB)
	require.NotEqual(t, saA.ID, saB.ID, "distinct principals must never resolve to the same service account")
	require.Equal(t, oracleDescriptionFor(principalA), saA.Description)
	require.Equal(t, oracleDescriptionFor(principalB), saB.Description)
}

// TestACLsLive_PrincipalMatrix_WildcardAndAnonymousSkip covers the
// unmappable-by-nature principals from design §9.4/§12 decision 3:
// "User:*" and "User:ANONYMOUS" warn-and-skip automatically — no service
// account, no ACL. These are fixed Kafka-semantic literals (not arbitrary
// strings we control), so — unlike every other scenario in this file — their
// principal can't carry a uniqueness token; isolation instead comes from
// each seeded ACL's resourceName, which the teardown filter matches exactly.
func TestACLsLive_PrincipalMatrix_WildcardAndAnonymousSkip(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	starResource := "kcpacl-skip-star-" + uniqueACLToken()
	anonResource := "kcpacl-skip-anon-" + uniqueACLToken()

	seedNativeACL(t, admin, sarama.AclResourceTopic, starResource, sarama.AclPatternLiteral, "User:*", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, starResource, sarama.AclPatternLiteral, "User:*", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, "User:ANONYMOUS", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, "User:ANONYMOUS", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-skip"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		include:    []string{"User:*", "User:ANONYMOUS"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)

	require.Empty(t, aclsForResourceName(cc.listACLs(t), starResource), "User:* must never get an ACL on CC (unmappable, warn-and-skip)")
	require.Empty(t, aclsForResourceName(cc.listACLs(t), anonResource), "User:ANONYMOUS must never get an ACL on CC (unmappable, warn-and-skip)")
}

// TestACLsLive_MappingOverride proves `mapping` (a) resolves a normal
// principal straight to a pre-existing service account with NO autoCreate
// involved, and (b) overrides the automatic ANONYMOUS skip — both landing on
// the SAME pre-existing sa-, per design §6/§12 decision 3 ("an explicit
// mapping can still override the skip").
func TestACLsLive_MappingOverride(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	base := uniqueACLToken()
	normalPrincipal := "User:" + base + "-app"
	anonPrincipal := "User:ANONYMOUS"

	preexisting := cc.createSA(t, "kcpacl-preexisting-"+base, "kcpacl-test:preexisting")
	defer func() { _ = deleteCCServiceAccount(context.Background(), cc.hc, preexisting.ID) }()

	normalResource := "kcpacl-mapping-normal-" + base
	anonResource := "kcpacl-mapping-anon-" + base
	seedNativeACL(t, admin, sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, normalPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, normalPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, anonPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, anonPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	defer func() {
		for _, a := range aclsForPrincipal(cc.listACLs(t), "User:"+preexisting.ID) {
			_ = deleteCCACL(context.Background(), cc.hc, cc.restEndpoint, cc.clusterID, a)
		}
	}()

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-mapping"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false, // mapping-only: proves resolution works with no autoCreate involved
		mapping: map[string]string{
			normalPrincipal: preexisting.ID,
			anonPrincipal:   preexisting.ID,
		},
		include: []string{"User:" + base + "*", "User:ANONYMOUS"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)

	resolved := "User:" + preexisting.ID
	expectedNormal := canonicalACL(sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, resolved, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	expectedAnon := canonicalACL(sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, resolved, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	actual := aclsForPrincipal(cc.listACLs(t), resolved)
	require.ElementsMatch(t, []types.Acls{expectedNormal, expectedAnon}, actual,
		"both the mapped normal principal and the mapping-overridden ANONYMOUS principal must land on the pre-existing service account")
}

// TestACLsLive_MappingFormats_UAndPoolPrefixesAccepted proves KCP's OWN
// mapping-value validation (manifest.Validate, "sa-|u-|pool-") accepts u- and
// pool- ids exactly like sa- ids, and that a mapped principal is handed
// straight to the ACL-create call with no further client-side lookup
// (serviceaccounts.Reconciler.Plan resolves a mapped principal without any
// CC API call). It deliberately does NOT assert whether Confluent Cloud's
// Kafka REST v3 ACL-create call itself validates that the mapped principal
// id corresponds to a real identity — that is a live-behaviour question this
// harness cannot resolve without creds (see the task report); this test only
// proves KCP does not reject the id on its own account.
func TestACLsLive_MappingFormats_UAndPoolPrefixesAccepted(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	base := uniqueACLToken()
	uPrincipal := "User:" + base + "-u"
	poolPrincipal := "User:" + base + "-pool"
	resourceU := "kcpacl-uprefix-" + base
	resourcePool := "kcpacl-poolprefix-" + base
	seedNativeACL(t, admin, sarama.AclResourceTopic, resourceU, sarama.AclPatternLiteral, uPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourceU, sarama.AclPatternLiteral, uPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, resourcePool, sarama.AclPatternLiteral, poolPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourcePool, sarama.AclPatternLiteral, poolPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	dummyU := "u-" + base
	dummyPool := "pool-" + base
	defer func() {
		for _, id := range []string{dummyU, dummyPool} {
			for _, a := range aclsForPrincipal(cc.listACLs(t), "User:"+id) {
				_ = deleteCCACL(context.Background(), cc.hc, cc.restEndpoint, cc.clusterID, a)
			}
		}
	}()

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-uprefix"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false,
		mapping: map[string]string{
			uPrincipal:    dummyU,
			poolPrincipal: dummyPool,
		},
		include: []string{"User:" + base + "*"},
	})
	out, err := runKCP(t, mf)
	if err != nil {
		require.NotContains(t, out, "must be a User:sa-/u-/pool- id", "u-/pool- prefixes must pass KCP's own mapping validation")
		require.NotContains(t, out, "no service-account mapping for principal", "a mapped principal must never hit the unmapped-principal error path")
		t.Logf("apply failed past KCP's own validation and resolution (an upstream CC rejection of the non-existent mapped id is the open, unverified-live question this test surfaces): %v\n%s", err, out)
		return
	}
	t.Logf("CC accepted ACLs for non-existent mapped u-/pool- ids (no principal-existence check at ACL-create time): %s", out)
}

// TestACLsLive_MappingInvalidPrefix_ValidationError proves an invalid
// mapping-value prefix is rejected at manifest validation, before any live
// call — a pure config-shape check, but gated the same way as every other
// test in this file for consistency.
func TestACLsLive_MappingInvalidPrefix_ValidationError(t *testing.T) {
	cfg := loadCloudConfig(t)
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-badmap"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false,
		mapping:    map[string]string{"User:app1": "role-not-a-valid-prefix"},
		include:    []string{"User:app1"},
	})
	out, err := runKCP(t, mf)
	require.Error(t, err, out)
	require.Contains(t, out, "must be a User:sa-/u-/pool- id")
}

// TestACLsLive_AutoCreateFalse_UnmappedPrincipal_HardError proves the ONE
// hard-error case (design §6/§12 decision 3): autoCreate:false and a normal
// (not User:*/ANONYMOUS) principal with no mapping entry. Unlike the
// validation test above, this genuinely exercises the live MSK read: the
// principal is only discovered, and only fails, once ReadNativeACLs has
// actually listed it from the source.
func TestACLsLive_AutoCreateFalse_UnmappedPrincipal_HardError(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")

	base := uniqueACLToken()
	principal := "User:" + base + "-unmapped"
	resource := "kcpacl-noautocreate-" + base
	seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-noautocreate"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false,
		include:    []string{"User:" + base + "*"},
	})
	out, err := runKCP(t, mf)
	require.Error(t, err, out)
	require.Contains(t, out, "no service-account mapping for principal")
	require.Contains(t, out, principal)
}

// TestACLsLive_DryRunThenApplyThenIdempotent covers both remaining
// cross-cutting requirements from design §9.4 in one scenario: --dry-run
// mutates nothing, a real apply creates exactly one service account and one
// ACL, and a second apply is a full no-op (idempotent).
func TestACLsLive_DryRunThenApplyThenIdempotent(t *testing.T) {
	cfg := loadCloudConfig(t)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cc := newACLTargetClients(t, cfg, target)

	base := uniqueACLToken()
	principal := "User:" + base + "-app"
	resource := "kcpacl-dryrun-" + base
	seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	displayName := oracleDeriveDisplayName(principal)
	defer cc.cleanupSAAndItsACLs(t, displayName)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-dryrun"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		include:    []string{"User:" + base + "*"},
	})

	dryOut, err := runKCP(t, mf, "--dry-run")
	require.NoError(t, err, dryOut)
	require.Nil(t, cc.findSA(t, displayName), "dry-run must not create a service account")
	require.Empty(t, aclsForResourceName(cc.listACLs(t), resource), "dry-run must not create an acl")

	applyOut, err := runKCP(t, mf)
	require.NoError(t, err, applyOut)
	require.Contains(t, applyOut, "serviceAccounts: 1 created")
	require.Contains(t, applyOut, "acls: 1 created")

	sa := cc.findSA(t, displayName)
	require.NotNil(t, sa)
	expected := []types.Acls{canonicalACL(sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, "User:"+sa.ID, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)}
	require.ElementsMatch(t, expected, aclsForPrincipal(cc.listACLs(t), "User:"+sa.ID))

	idemOut, err := runKCP(t, mf)
	require.NoError(t, err, idemOut)
	require.Contains(t, idemOut, "serviceAccounts: 0 created")
	require.Contains(t, idemOut, "acls: 0 created")
	// State on CC must be exactly the same after the idempotent re-run: no
	// duplicate service account, no duplicate ACL.
	require.ElementsMatch(t, expected, aclsForPrincipal(cc.listACLs(t), "User:"+sa.ID))
}
