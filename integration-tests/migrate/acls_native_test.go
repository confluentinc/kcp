//go:build integration

// This file is the LIVE (Tier 2) integration suite for the native-ACL
// MSK→Confluent Cloud migration plane (design §9). Tier 1 is the hermetic suite
// in internal/migrate/{acls,serviceaccounts}; this proves the same behaviour
// against a REAL MSK source and CC target (make test-migrate-acls-live),
// following the house pattern of migrate_topics_new_test.go. Each scenario:
// builds an acls-only manifest scoped to its own seeded ACL(s); runs the real
// `kcp migrate apply` and asserts the command's OWN reported summary
// ("serviceAccounts: N created, ..." / "acls: N created, 0 unchanged, 0 drift,
// 0 failed", counts hardcoded per scenario); re-applies and asserts "0 created,
// N unchanged" — the idempotency that proves the product recognises its own
// previously-created (CC-numeric) ACLs, so the TEST never resolves numeric↔sa;
// reads concrete facts back off the target scoped to the run's unique resource
// name(s), asserting translation properties (host→"*", DENY preserved,
// LITERAL/PREFIXED preserved, dropped ACLs absent) without asserting the
// numeric read-back principal; and asserts the expected service account exists
// by DESCRIPTION contract (kcp:source-principal=<principal>), never by
// re-deriving the display name.
//
// Env-gating: every test calls loadCloudConfig, the same CC_*/MSK_* gate as the
// rest of the live suite, so all tests SKIP cleanly when unset (e.g. test-go).
// Isolation & teardown (design §9.9): every seeded principal and scopable
// resource carries uniqueACLToken(); teardown is deferred before the run (so it
// executes on failure too) and deletes exactly what the test created (SA deletes
// go through the CLOUD-creds client — IAM v2 rejects a Kafka cluster key).
package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
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
// call. It is kept short (no scenario name embedded) so a principal-shape
// scenario asserting a verbatim (no-hash) display name stays under the 64-char
// CC limit, and is alphanumeric-and-hyphen so it is always itself a valid CC
// display-name fragment. Its "kcpacl" prefix is the marker TestACLsLive_SweepResidue
// sweeps by.
func uniqueACLToken() string {
	return fmt.Sprintf("kcpacl%s-%d", runID, <-aclTokenSeq)
}

// sourcePrincipalDescription is the audit/collision-check description KCP writes
// on every auto-created service account (design §6). It is the SA lookup key
// this file matches on — a stable contract label, not the display-name
// derivation under test.
func sourcePrincipalDescription(principal string) string {
	return "kcp:source-principal=" + principal
}

// scenarioResourceName makes a seed's resource name run-unique so the read-back
// can be scoped by resource name alone. The topic-wildcard "*" and the fixed
// "kafka-cluster" resource are Kafka semantic literals that cannot be
// uniquified; scenarios using them fall back to the command summary + SA
// (non-)existence for their signal (isScopableResource reports which).
func scenarioResourceName(stem, token string) string {
	if !isScopableResource(stem) {
		return stem
	}
	return stem + "-" + token
}

func isScopableResource(stem string) bool { return stem != "*" && stem != "kafka-cluster" }

// ---------------------------------------------------------------------------
// MSK-side plumbing: seed/delete a native ACL with an arbitrary principal
// STRING (design §9.0 — no mTLS PKI needed to exercise principal shapes).
// ---------------------------------------------------------------------------

// seedNativeACL creates one native ACL on the MSK source via the IAM admin
// principal. Works for ANY principal string, including DN-shaped ones: a Kafka
// ACL principal is just a string, no certificate or identity needs to exist.
func seedNativeACL(t *testing.T, admin sarama.ClusterAdmin, rt sarama.AclResourceType, resourceName string, pt sarama.AclResourcePatternType, principal, host string, op sarama.AclOperation, perm sarama.AclPermissionType) {
	t.Helper()
	res := sarama.Resource{ResourceType: rt, ResourceName: resourceName, ResourcePatternType: pt}
	acl := sarama.Acl{Principal: principal, Host: host, Operation: op, PermissionType: perm}
	require.NoError(t, admin.CreateACL(res, acl), "seed native ACL %s %s host=%s on %s %q for %s", perm.String(), op.String(), host, rt.String(), resourceName, principal)
}

// deleteNativeACL removes exactly the one native-ACL tuple seedNativeACL
// created, via a precise (never wildcard) filter, so teardown cannot touch any
// other ACL on the shared live MSK cluster. Best-effort: logs rather than fails.
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

// ---------------------------------------------------------------------------
// CC-side plumbing: read-back (the product's own wire clients, used only to
// READ) and teardown (raw REST — ACLClient and CCClient are additive-only by
// design and have no Delete method).
// ---------------------------------------------------------------------------

// aclTargetClients bundles the CC target's REST clients for this file's
// read-back and teardown. hc/auth are the CLUSTER-key creds the Kafka REST v3
// ACL surface accepts; saHC/saAuth are the CLOUD/Global-key creds IAM v2 (and
// the legacy service-accounts list) requires — IAM v2 rejects a Kafka cluster
// key. This split mirrors the product's own buildACLReconcilers.
type aclTargetClients struct {
	hc           clusterlink.HTTPClient
	auth         clusterlink.Authenticator
	saHC         clusterlink.HTTPClient
	saAuth       clusterlink.Authenticator
	restEndpoint string
	clusterID    string
}

// newACLTargetClients builds an aclTargetClients. targetCredsPath is the CLUSTER
// creds file; cloudCredsPath is the separate Cloud/Global creds file for the SA
// client — pass "" when the caller never touches IAM v2 (saHC/saAuth then
// harmlessly fall back to the cluster creds, which go unused in that case).
func newACLTargetClients(t *testing.T, cfg cloudConfig, targetCredsPath, cloudCredsPath string) aclTargetClients {
	t.Helper()
	creds, err := targets.LoadCredentials(targetCredsPath)
	require.NoError(t, err)
	hc, err := creds.HTTPClient()
	require.NoError(t, err)
	saHC, saAuth := hc, creds.Authenticator()
	if cloudCredsPath != "" {
		cloudCreds, err := targets.LoadCredentials(cloudCredsPath)
		require.NoError(t, err)
		cloudHC, err := cloudCreds.HTTPClient()
		require.NoError(t, err)
		saHC, saAuth = cloudHC, cloudCreds.Authenticator()
	}
	return aclTargetClients{hc: hc, auth: creds.Authenticator(), saHC: saHC, saAuth: saAuth, restEndpoint: cfg.ccRestEndpoint, clusterID: cfg.ccClusterID}
}

// listACLs reads every ACL on the CC cluster via the product's own ACL client.
// It returns principals verbatim as CC reports them (auto-created SAs come back
// as "User:<numeric>") — this file never rewrites them; the product's own
// numeric normalization is what idempotency proves.
func (c aclTargetClients) listACLs(t *testing.T) []types.Acls {
	t.Helper()
	acls, err := macls.NewACLClient(c.restEndpoint, c.clusterID, c.hc, c.auth).List(context.Background())
	require.NoError(t, err)
	return acls
}

// tryListACLs is the cleanup-safe counterpart to listACLs: on a read error it
// logs and reports ok=false instead of asserting, so a transient CC read error
// in a deferred teardown never trips t.FailNow()/Goexit() and aborts the rest
// of that cleanup. Only call it from teardown/defer paths.
func (c aclTargetClients) tryListACLs(t *testing.T) (acls []types.Acls, ok bool) {
	t.Helper()
	acls, err := macls.NewACLClient(c.restEndpoint, c.clusterID, c.hc, c.auth).List(context.Background())
	if err != nil {
		t.Logf("cleanup: list CC acls failed, continuing: %v", err)
		return nil, false
	}
	return acls, true
}

// aclsOnResource returns the CC ACLs whose resource name matches exactly — the
// run-scoping the whole file relies on (resource names carry uniqueACLToken()).
func (c aclTargetClients) aclsOnResource(t *testing.T, resourceName string) []types.Acls {
	t.Helper()
	return aclsForResourceName(c.listACLs(t), resourceName)
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

// findSAForPrincipal returns the CC service account KCP auto-created for
// principal, located by its DESCRIPTION contract (kcp:source-principal=...) —
// a concrete fact, never by re-deriving the display name. nil if none exists
// (the correct state for a principal whose every ACL was dropped).
func (c aclTargetClients) findSAForPrincipal(t *testing.T, principal string) *msa.ServiceAccount {
	t.Helper()
	sas, err := listAllServiceAccounts(context.Background(), c.saHC, c.saAuth)
	require.NoError(t, err)
	return findByDescription(sas, sourcePrincipalDescription(principal))
}

// tryFindSAForPrincipal is the cleanup-safe counterpart to findSAForPrincipal.
func (c aclTargetClients) tryFindSAForPrincipal(t *testing.T, principal string) (*msa.ServiceAccount, bool) {
	t.Helper()
	sas, err := listAllServiceAccounts(context.Background(), c.saHC, c.saAuth)
	if err != nil {
		t.Logf("cleanup: list CC service accounts failed, continuing: %v", err)
		return nil, false
	}
	return findByDescription(sas, sourcePrincipalDescription(principal)), true
}

func findByDescription(sas []msa.ServiceAccount, description string) *msa.ServiceAccount {
	for i := range sas {
		if sas[i].Description == description {
			return &sas[i]
		}
	}
	return nil
}

func (c aclTargetClients) createSA(t *testing.T, displayName, description string) *msa.ServiceAccount {
	t.Helper()
	sa, err := msa.NewCCClient(msa.DefaultBaseURL, c.saHC, c.saAuth).Create(context.Background(), displayName, description)
	require.NoError(t, err)
	return sa
}

// Teardown-only wire-enum maps for the Kafka REST v3 ACL delete-by-filter call.
// Deliberately duplicated (not imported) from internal/migrate/acls' private
// tables: the product's ACLClient has no Delete method by design (strictly
// additive, design §12 decision 1) — teardown must talk to the wire API directly.
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

// deleteCCACL removes one ACL from the CC target via the Kafka REST v3
// delete-by-filter endpoint. Pass the CLUSTER creds authenticator (c.auth),
// matching the client that legitimately talks to Kafka REST v3 ACLs.
func deleteCCACL(ctx context.Context, hc clusterlink.HTTPClient, auth clusterlink.Authenticator, restEndpoint, clusterID string, a types.Acls) error {
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
	auth.Apply(req)
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

// deleteCCServiceAccount removes one CC service account via IAM v2. auth MUST be
// the CLOUD/Global creds authenticator (c.saAuth) paired with saHC — IAM v2
// rejects a Kafka cluster key.
func deleteCCServiceAccount(ctx context.Context, hc clusterlink.HTTPClient, auth clusterlink.Authenticator, id string) error {
	u := msa.DefaultBaseURL + "/iam/v2/service-accounts/" + url.PathEscape(id)
	req, err := http.NewRequestWithContext(ctx, http.MethodDelete, u, nil)
	if err != nil {
		return err
	}
	auth.Apply(req)
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

// requireCloudKeys skips the calling test cleanly when the CC Cloud/Global API
// key env vars are unset. Service-account auto-create drives IAM v2, which
// rejects a Kafka cluster key, so those tests need a separate Cloud/Global key.
// Mapping/validation-only tests never call IAM v2 and so don't call this.
func requireCloudKeys(t *testing.T, cfg cloudConfig) {
	t.Helper()
	if cfg.ccCloudKey == "" || cfg.ccCloudSecret == "" {
		t.Skip("cloud suite: CC_CLOUD_KEY/CC_CLOUD_SECRET not set; skipping service-account auto-create tests")
	}
}

// writeCloudCredsFile writes the CC Cloud/Global API key creds file used by
// spec.target.cloudCredentials and returns its path — a DIFFERENT file from
// writeCloudCreds' cc.yaml (the Kafka cluster key).
func writeCloudCredsFile(t *testing.T, dir string, cfg cloudConfig) string {
	t.Helper()
	q := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	p := filepath.Join(dir, "cc-cloud.yaml")
	require.NoError(t, os.WriteFile(p, []byte("api_key: "+q(cfg.ccCloudKey)+"\napi_secret: "+q(cfg.ccCloudSecret)+"\n"), 0600))
	return p
}

// aclManifestOpts configures spec.serviceAccounts + spec.acls for one scenario.
type aclManifestOpts struct {
	autoCreate bool
	mapping    map[string]string
	include    []string
	exclude    []string
	// cloudCreds, when non-empty, is written verbatim as
	// spec.target.cloudCredentials. Any acls→confluent-cloud manifest requires
	// it (validation now demands cloudCredentials for spec.acls on a CC target,
	// because the acls reconciler needs the Cloud/Global key to reconcile CC's
	// numeric ACL principals for idempotency), so writeACLManifest ALWAYS emits
	// a cloudCredentials line — falling back to a freshly written Cloud/Global
	// creds file (from cfg) when the caller leaves this empty.
	cloudCreds string
}

// writeACLManifest writes a source-read-only (no cluster link) MSK→CC migration
// manifest with a spec.acls (+ spec.serviceAccounts) section and returns its
// path. sourceBootstrap/sourceCreds are the LIVE MSK read connection (IAM);
// targetCreds is the CC api_key/api_secret creds file.
func writeACLManifest(t *testing.T, dir, name string, cfg cloudConfig, sourceBootstrap []string, sourceCreds, targetCreds string, opts aclManifestOpts) string {
	t.Helper()
	// Double-quoted YAML scalars throughout: principals under test can carry
	// YAML-significant characters (":", "="), and quoting uniformly avoids
	// reasoning about which values are "safe" unquoted.
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
	b.WriteString("  target:\n    type: confluent-cloud\n    clusterId: " + cfg.ccClusterID + "\n    clusterCredentials: " + targetCreds + "\n")
	// Any acls→CC manifest requires cloudCredentials (see aclManifestOpts): emit
	// it unconditionally, falling back to a freshly written Cloud/Global creds
	// file when the caller didn't pass one.
	cloudCreds := opts.cloudCreds
	if cloudCreds == "" {
		cloudCreds = writeCloudCredsFile(t, dir, cfg)
	}
	b.WriteString("    cloudCredentials: " + cloudCreds + "\n")
	b.WriteString("    kafka:\n      restEndpoint: " + cfg.ccRestEndpoint + "\n")
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

// nativeACL is one seeded native-ACL tuple (the principal is supplied by the
// runner, shared across a scenario's seed entries).
type nativeACL struct {
	ResourceType sarama.AclResourceType
	ResourceName string
	PatternType  sarama.AclResourcePatternType
	Operation    sarama.AclOperation
	Permission   sarama.AclPermissionType
	Host         string
}

// aclScenario is one row of the native-ACL / principal-shape matrix: seed a
// single principal's native ACL(s) and declare which of them survive migration
// (expectSeedIdx) — every other seeded index is expected to be dropped.
// principalSuffix is appended after a fresh uniqueACLToken() so principal-shape
// scenarios can control the exact shape (DN-like, email-like, >64 chars, ...)
// while native-ACL scenarios leave it empty (a plain, always-verbatim token).
//
// expectDropped marks a scenario whose EVERY seeded ACL is dropped
// (expectSeedIdx therefore empty): no surviving ACL means no principal is ever
// resolved, so no service account is created for it.
type aclScenario struct {
	name            string
	principalSuffix string
	seed            []nativeACL
	expectSeedIdx   []int
	expectDropped   bool
}

// nativeMatrixScenarios covers design §9.2: resource types (Topic, Group,
// Cluster, TransactionalID), pattern types (LITERAL, PREFIXED, the LITERAL-"*"
// all-of-type wildcard), operations including invalid-on-CC combos, ALLOW and
// DENY, host normalization, operation-implication de-dup, and two drop-list
// items (ClusterAction here; the broker-principal drop is
// TestACLsLive_NativeMatrix_BrokerPrincipalDropped, which needs a principal
// SHAPE rather than a plain token).
var nativeMatrixScenarios = []aclScenario{
	{
		name:          "topic_literal_read_allow_host_star",
		seed:          []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx: []int{0},
	},
	{
		name: "topic_literal_write_allow_specific_host_normalizes",
		// Source host is a specific IP; the migrated ACL must normalize host to "*".
		seed:          []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationWrite, Permission: sarama.AclPermissionAllow, Host: "203.0.113.5"}},
		expectSeedIdx: []int{0},
	},
	{
		name: "topic_dedup_describe_redundant_dropped",
		// Describe is implied by Read → deduped; only the Read ACL survives.
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"},
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationDescribe, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0},
	},
	{
		name: "topic_dedup_alterconfigs_describeconfigs_dropped",
		// DescribeConfigs is implied by AlterConfigs → deduped.
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationAlterConfigs, Permission: sarama.AclPermissionAllow, Host: "*"},
			{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicA", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationDescribeConfigs, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		expectSeedIdx: []int{0},
	},
	{
		name: "topic_prefixed_deny_specific_host_normalizes",
		// DENY preserved 1:1; PREFIXED preserved; host normalized to "*".
		seed:          []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "kcpacl-topicPre", PatternType: sarama.AclPatternPrefixed, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionDeny, Host: "203.0.113.9"}},
		expectSeedIdx: []int{0},
	},
	{
		name:          "topic_literal_wildcard_star_allow",
		seed:          []nativeACL{{ResourceType: sarama.AclResourceTopic, ResourceName: "*", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx: []int{0},
	},
	{
		name:          "group_read_allow_valid",
		seed:          []nativeACL{{ResourceType: sarama.AclResourceGroup, ResourceName: "kcpacl-group1", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx: []int{0},
	},
	{
		name: "group_write_invalid_on_cc_dropped",
		// Write is not in Group's CC-valid set {Read, Describe, Delete}: dropped.
		seed:          []nativeACL{{ResourceType: sarama.AclResourceGroup, ResourceName: "kcpacl-group2", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationWrite, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectDropped: true,
	},
	{
		name:          "transactionalid_write_allow_valid",
		seed:          []nativeACL{{ResourceType: sarama.AclResourceTransactionalID, ResourceName: "kcpacl-txn1", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationWrite, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectSeedIdx: []int{0},
	},
	{
		name: "transactionalid_read_invalid_on_cc_dropped",
		// Read is not in TransactionalID's CC-valid set {Describe, Write}: dropped.
		seed:          []nativeACL{{ResourceType: sarama.AclResourceTransactionalID, ResourceName: "kcpacl-txn2", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationRead, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectDropped: true,
	},
	{
		name: "cluster_clusteraction_droplist_dropped",
		// ClusterAction is drop-list (design §5 rule 4): inter-broker only.
		seed:          []nativeACL{{ResourceType: sarama.AclResourceCluster, ResourceName: "kafka-cluster", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationClusterAction, Permission: sarama.AclPermissionAllow, Host: "*"}},
		expectDropped: true,
	},
}

// principalMatrixScenarios covers the naming half of design §9.4 — the simple
// "created, correctly named" cases. The collision pair, the User:*/ANONYMOUS
// skip, and the mapping overrides each need bespoke setup and are their own
// dedicated tests below. Every row uses the SAME fixed ACL shape
// (Topic/LITERAL/Read/Allow/*) so the only variable is the principal shape.
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

// runACLScenario seeds s's native ACL(s) under a fresh unique principal (and
// unique resource names), applies an acls-only manifest scoped to just that
// principal, and asserts: the command's reported serviceAccounts/acls create
// counts; re-apply idempotency; the concrete translation properties of each
// surviving ACL read back off the target (scoped to the run's own resources);
// and the expected SA's existence-by-description (or absence, when dropped).
// Teardown is deferred before the run so it always executes.
func runACLScenario(t *testing.T, cfg cloudConfig, cc aclTargetClients, admin sarama.ClusterAdmin, dir, target, cloudCreds, sourceRead string, s aclScenario) {
	t.Helper()
	token := uniqueACLToken()
	principal := "User:" + token + s.principalSuffix

	for _, sd := range s.seed {
		sd := sd
		rn := scenarioResourceName(sd.ResourceName, token)
		seedNativeACL(t, admin, sd.ResourceType, rn, sd.PatternType, principal, sd.Host, sd.Operation, sd.Permission)
		defer deleteNativeACL(t, admin, sd.ResourceType, rn, sd.PatternType, principal, sd.Host, sd.Operation, sd.Permission)
	}

	// Teardown (before the run, so it survives failure): delete the SA KCP
	// auto-created and, via its resolved sa- principal, the CC ACLs it authored
	// (delete-by-filter is symmetric with the create — KCP creates ACLs with
	// "User:sa-...", not the numeric form CC reports on read). A dropped
	// scenario created no SA, so the lookup no-ops.
	defer func() {
		sa, ok := cc.tryFindSAForPrincipal(t, principal)
		if !ok || sa == nil {
			return
		}
		for _, idx := range s.expectSeedIdx {
			sd := s.seed[idx]
			a := types.Acls{
				ResourceType:        sd.ResourceType.String(),
				ResourceName:        scenarioResourceName(sd.ResourceName, token),
				ResourcePatternType: sd.PatternType.String(),
				Principal:           "User:" + sa.ID,
				Host:                "*", // created ACLs always carry the normalized host
				Operation:           sd.Operation.String(),
				PermissionType:      sd.Permission.String(),
			}
			if err := deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a); err != nil {
				t.Logf("cleanup: delete CC acl %+v: %v", a, err)
			}
		}
		if err := deleteCCServiceAccount(context.Background(), cc.saHC, cc.saAuth, sa.ID); err != nil {
			t.Logf("cleanup: delete CC service account %s: %v", sa.ID, err)
		}
	}()

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-"+s.name), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		include:    []string{"User:" + token + "*"},
	})

	wantACLs := len(s.expectSeedIdx)
	wantSAs := 0
	if wantACLs > 0 {
		wantSAs = 1
	}

	// 1. Command summary — KCP's own reported create counts.
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, fmt.Sprintf("serviceAccounts: %d created, 0 unchanged, 0 drift, 0 failed", wantSAs), out)
	require.Contains(t, out, fmt.Sprintf("acls: %d created, 0 unchanged, 0 drift, 0 failed", wantACLs), out)

	if s.expectDropped {
		// No surviving ACL → no principal resolved → no service account, and
		// nothing on the (scopable) seeded resources. (The cluster "kafka-cluster"
		// resource is not run-unique, so its absence is carried by "acls: 0
		// created" above rather than an unscoped re-scan.)
		require.Nil(t, cc.findSAForPrincipal(t, principal),
			"scenario %q: every seeded ACL is dropped, so no service account should exist for %q", s.name, principal)
		for _, sd := range s.seed {
			if isScopableResource(sd.ResourceName) {
				require.Empty(t, cc.aclsOnResource(t, scenarioResourceName(sd.ResourceName, token)),
					"scenario %q: dropped ACL must be absent on target", s.name)
			}
		}
		// Idempotent re-apply: still nothing to create.
		out, err = runKCP(t, mf)
		require.NoError(t, err, out)
		require.Contains(t, out, "acls: 0 created, 0 unchanged, 0 drift, 0 failed", out)
		return
	}

	// 2. The auto-created service account exists, located by its description
	// contract (not by re-deriving the display name).
	sa := cc.findSAForPrincipal(t, principal)
	require.NotNil(t, sa, "scenario %q: expected a service account for principal %q (description %q)", s.name, principal, sourcePrincipalDescription(principal))

	// 3. Concrete read-back translation facts, scoped to this run's unique
	// resource name(s). Group surviving seeds by resource so a dedup scenario
	// (two seeds, one survivor) asserts the redundant op was NOT created.
	survivingByResource := map[string][]nativeACL{}
	for _, idx := range s.expectSeedIdx {
		sd := s.seed[idx]
		if !isScopableResource(sd.ResourceName) {
			continue // e.g. the LITERAL "*" wildcard: covered by the summary + SA above
		}
		rn := scenarioResourceName(sd.ResourceName, token)
		survivingByResource[rn] = append(survivingByResource[rn], sd)
	}
	for rn, survivors := range survivingByResource {
		got := cc.aclsOnResource(t, rn)
		require.Len(t, got, len(survivors), "scenario %q: unexpected ACL count on resource %q (redundant/dropped ops must not be created)", s.name, rn)
		for _, sd := range survivors {
			a := findACLByOperation(got, sd.Operation.String())
			require.NotNil(t, a, "scenario %q: surviving %s ACL missing on resource %q", s.name, sd.Operation.String(), rn)
			require.Equal(t, "*", a.Host, "host must normalize to * (source host was %q)", sd.Host)
			require.Equal(t, sd.PatternType.String(), a.ResourcePatternType, "pattern type must be preserved")
			require.Equal(t, sd.Permission.String(), a.PermissionType, "permission (ALLOW/DENY) must be preserved")
			require.NotEmpty(t, a.Principal, "read-back ACL must carry a resolved principal")
		}
	}

	// 4. Idempotency — re-apply is a full no-op. This is what proves KCP
	// recognises its own previously-created (CC-numeric) ACLs; the test itself
	// never resolves numeric↔sa.
	out, err = runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "serviceAccounts: 0 created, 1 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, fmt.Sprintf("acls: 0 created, %d unchanged, 0 drift, 0 failed", wantACLs), out)
}

// findACLByOperation returns the first ACL with the given (titlecase) operation,
// or nil. Within one run-unique resource there is at most one surviving ACL per
// operation, so this uniquely identifies a survivor.
func findACLByOperation(acls []types.Acls, operation string) *types.Acls {
	for i := range acls {
		if acls[i].Operation == operation {
			return &acls[i]
		}
	}
	return nil
}

func TestACLsLive_NativeMatrix(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	for _, s := range nativeMatrixScenarios {
		s := s
		t.Run(s.name, func(t *testing.T) {
			runACLScenario(t, cfg, cc, admin, dir, target, cloudCreds, sourceRead, s)
		})
	}
}

func TestACLsLive_PrincipalMatrix(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	for _, s := range principalMatrixScenarios {
		s := s
		t.Run(s.name, func(t *testing.T) {
			runACLScenario(t, cfg, cc, admin, dir, target, cloudCreds, sourceRead, s)
		})
	}
}

// TestACLsLive_CleanUsernameVerbatim proves a clean (already CC-valid) username
// is used as the service-account display name VERBATIM — no sanitize, no hash.
// It asserts a direct equality between the created SA's display_name and the
// plain principal (a known literal), not a re-derivation of the naming rule.
func TestACLsLive_CleanUsernameVerbatim(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	token := uniqueACLToken()
	plainName := token + "-app-consumer" // already CC-valid: alnum/hyphen, < 64 chars
	principal := "User:" + plainName
	resource := "kcpacl-verbatim-" + token
	seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer cleanupSAForPrincipal(t, cc, principal, resource, sarama.AclResourceTopic, sarama.AclPatternLiteral, sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-verbatim"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		include:    []string{"User:" + token + "*"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "serviceAccounts: 1 created, 0 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, "acls: 1 created, 0 unchanged, 0 drift, 0 failed", out)

	sa := cc.findSAForPrincipal(t, principal)
	require.NotNil(t, sa)
	require.Equal(t, plainName, sa.DisplayName, "a clean username must be the display name verbatim (no sanitize/hash)")
}

// cleanupSAForPrincipal deletes the SA KCP auto-created for principal (found by
// description) and the single CC ACL it authored on resource, reconstructed
// from the known seed tuple. Best-effort; safe to call from a defer before the
// run (the lookup no-ops when nothing was created).
func cleanupSAForPrincipal(t *testing.T, cc aclTargetClients, principal, resource string, rt sarama.AclResourceType, pt sarama.AclResourcePatternType, op sarama.AclOperation, perm sarama.AclPermissionType) {
	t.Helper()
	sa, ok := cc.tryFindSAForPrincipal(t, principal)
	if !ok || sa == nil {
		return
	}
	a := types.Acls{
		ResourceType:        rt.String(),
		ResourceName:        resource,
		ResourcePatternType: pt.String(),
		Principal:           "User:" + sa.ID,
		Host:                "*",
		Operation:           op.String(),
		PermissionType:      perm.String(),
	}
	if err := deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a); err != nil {
		t.Logf("cleanup: delete CC acl %+v: %v", a, err)
	}
	if err := deleteCCServiceAccount(context.Background(), cc.saHC, cc.saAuth, sa.ID); err != nil {
		t.Logf("cleanup: delete CC service account %s: %v", sa.ID, err)
	}
}

// TestACLsLive_NativeMatrix_BrokerPrincipalDropped covers the second drop-list
// item from design §9.2: an MSK-broker-shaped principal (the broker's own mTLS
// identity, recognized by internal/migrate/acls' isBrokerPrincipal) is dropped
// entirely. The token has to live INSIDE the broker-shaped principal (a prefix
// would break the broker regex), so this needs bespoke setup.
func TestACLsLive_NativeMatrix_BrokerPrincipalDropped(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	token := uniqueACLToken()
	principal := "User:CN=b-1." + token + ".kafka.us-east-1.amazonaws.com"
	// The resource name carries the token too so the read-back "must be absent"
	// assertion is immune to residue on a static name from another run.
	resourceName := "kcpacl-broker-topic-" + token

	seedNativeACL(t, admin, sarama.AclResourceTopic, resourceName, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourceName, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-broker"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		include:    []string{"*" + token + "*"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "acls: 0 created, 0 unchanged, 0 drift, 0 failed", out)
	require.Empty(t, cc.aclsOnResource(t, resourceName), "an MSK-broker-shaped principal's ACL must be dropped entirely, never migrated")
	require.Nil(t, cc.findSAForPrincipal(t, principal), "no service account for a fully-dropped broker principal")
}

// TestACLsLive_PrincipalMatrix_CollisionPair covers the collision case from
// design §9.4: two DISTINCT principals that sanitize+truncate to the SAME base
// must still resolve to two DISTINCT service accounts (distinct hash suffixes) —
// never merged.
func TestACLsLive_PrincipalMatrix_CollisionPair(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	token := uniqueACLToken()
	// Two principals sharing a long common DN prefix (so their sanitized+
	// truncated bases collide) but differing in the tail.
	longCommon := strings.Repeat("z", 60)
	principalA := "User:" + token + "-CN=" + longCommon + "-alpha"
	principalB := "User:" + token + "-CN=" + longCommon + "-beta"

	resource := "kcpacl-collision-topic-" + token
	for _, p := range []string{principalA, principalB} {
		p := p
		seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, p, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
		defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, p, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	}
	defer cleanupSAForPrincipal(t, cc, principalA, resource, sarama.AclResourceTopic, sarama.AclPatternLiteral, sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer cleanupSAForPrincipal(t, cc, principalB, resource, sarama.AclResourceTopic, sarama.AclPatternLiteral, sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-collision"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		include:    []string{"User:" + token + "*"},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "serviceAccounts: 2 created, 0 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, "acls: 2 created, 0 unchanged, 0 drift, 0 failed", out)

	saA := cc.findSAForPrincipal(t, principalA)
	saB := cc.findSAForPrincipal(t, principalB)
	require.NotNil(t, saA)
	require.NotNil(t, saB)
	require.NotEqual(t, saA.ID, saB.ID, "distinct principals must never resolve to the same service account")
	require.NotEqual(t, saA.DisplayName, saB.DisplayName, "colliding truncated bases must still derive distinct display names (distinct hash suffix)")
}

// TestACLsLive_PrincipalMatrix_WildcardAndAnonymousSkip covers the
// unmappable-by-nature principals from design §9.4/§12 decision 3: "User:*" and
// "User:ANONYMOUS" warn-and-skip automatically — no service account, no ACL.
// These are fixed Kafka-semantic literals, so — unlike every other scenario —
// their principal can't carry a token; isolation comes from each seeded ACL's
// run-unique resource name, which the manifest includes by exact literal.
func TestACLsLive_PrincipalMatrix_WildcardAndAnonymousSkip(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	starResource := "kcpacl-skip-star-" + uniqueACLToken()
	anonResource := "kcpacl-skip-anon-" + uniqueACLToken()

	seedNativeACL(t, admin, sarama.AclResourceTopic, starResource, sarama.AclPatternLiteral, "User:*", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, starResource, sarama.AclPatternLiteral, "User:*", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, "User:ANONYMOUS", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, "User:ANONYMOUS", "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	// Scope by exact literal resource name, NOT by principal: "User:*" as an
	// include glob would match EVERY "User:"-prefixed principal on the shared
	// live cluster. starResource/anonResource are run-unique literals, so the
	// include only ever selects this test's own two seeded ACLs.
	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-skip"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		include:    []string{starResource, anonResource},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "serviceAccounts: 0 created, 0 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, "acls: 0 created, 0 unchanged, 0 drift, 0 failed", out)
	require.Empty(t, cc.aclsOnResource(t, starResource), "User:* must never get an ACL on CC (unmappable, warn-and-skip)")
	require.Empty(t, cc.aclsOnResource(t, anonResource), "User:ANONYMOUS must never get an ACL on CC (unmappable, warn-and-skip)")
}

// TestACLsLive_MappingOverride proves `mapping` (a) resolves a normal principal
// straight to a pre-existing service account with NO autoCreate, and (b)
// overrides the automatic ANONYMOUS skip — both landing on the SAME pre-existing
// sa- (design §6/§12 decision 3).
func TestACLsLive_MappingOverride(t *testing.T) {
	cfg := loadCloudConfig(t)
	// Needs the Cloud/Global key on two counts even though the manifest uses
	// mapping, not autoCreate: creating/tearing down the pre-existing SA uses the
	// SA helper client (IAM v2), and the apply itself now builds the numeric ACL
	// principal map (for idempotency) via the Cloud/Global key.
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	token := uniqueACLToken()
	normalPrincipal := "User:" + token + "-app"
	anonPrincipal := "User:ANONYMOUS"

	preexisting := cc.createSA(t, "kcpacl-preexisting-"+token, "kcpacl-test:preexisting")
	defer func() { _ = deleteCCServiceAccount(context.Background(), cc.saHC, cc.saAuth, preexisting.ID) }()

	normalResource := "kcpacl-mapping-normal-" + token
	anonResource := "kcpacl-mapping-anon-" + token
	seedNativeACL(t, admin, sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, normalPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, normalPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, anonPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, anonPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	// Delete the created CC ACLs by their run-unique resource names (best-effort,
	// via the read-back tuples so principals match whatever form CC reports).
	defer func() {
		if acls, ok := cc.tryListACLs(t); ok {
			for _, a := range acls {
				if a.ResourceName == normalResource || a.ResourceName == anonResource {
					_ = deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a)
				}
			}
		}
	}()

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-mapping"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false, // mapping-only: resolution works with no autoCreate
		cloudCreds: cloudCreds,
		mapping: map[string]string{
			normalPrincipal: preexisting.ID,
			anonPrincipal:   preexisting.ID,
		},
		// anonResource (run-unique literal), not the fixed "User:ANONYMOUS"
		// principal, for the same shared-literal reason as the skip test above.
		include: []string{"User:" + token + "*", anonResource},
	})
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	// Both source principals map to the SAME pre-existing service account, so
	// the reconciler reports one "unchanged" line per principal (2), none created.
	require.Contains(t, out, "serviceAccounts: 0 created, 2 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, "acls: 2 created, 0 unchanged, 0 drift, 0 failed", out)

	// Both ACLs landed, each host-normalized, and both under the SAME resolved
	// principal — the mapping's whole point. (The read-back principal is CC's
	// numeric form; we assert the two ACLs SHARE it rather than its exact value.)
	normal := cc.aclsOnResource(t, normalResource)
	anon := cc.aclsOnResource(t, anonResource)
	require.Len(t, normal, 1, "mapped normal principal must get exactly one ACL")
	require.Len(t, anon, 1, "mapping-overridden ANONYMOUS principal must get exactly one ACL")
	require.Equal(t, "*", normal[0].Host)
	require.Equal(t, "*", anon[0].Host)
	require.NotEmpty(t, normal[0].Principal)
	require.Equal(t, normal[0].Principal, anon[0].Principal, "both mapped principals must land on the same pre-existing service account")

	// Idempotent re-apply.
	out, err = runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "acls: 0 created, 2 unchanged, 0 drift, 0 failed", out)
}

// TestACLsLive_MappingFormats_UAndPoolPrefixesAccepted proves KCP's OWN
// mapping-value validation (manifest.Validate, "sa-|u-|pool-") accepts u- and
// pool- ids like sa- ids, and hands a mapped principal straight to the
// ACL-create call with no client-side lookup. It does NOT assert whether CC
// itself validates the mapped id against a real identity (an open live-behaviour
// question) — only that KCP does not reject the id on its own account.
func TestACLsLive_MappingFormats_UAndPoolPrefixesAccepted(t *testing.T) {
	cfg := loadCloudConfig(t)
	// The manifest carries cloudCredentials (validation requires it for spec.acls
	// on a CC target, and apply builds the numeric-principal map via the
	// Cloud/Global key), so this needs the cloud key.
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	// The test's OWN read-back client never touches the SA/IAM v2 surface (its
	// mapping values are only asserted via the cluster-creds ACL client), so
	// saHC/saAuth harmlessly fall back to the cluster creds here.
	cc := newACLTargetClients(t, cfg, target, "")

	token := uniqueACLToken()
	uPrincipal := "User:" + token + "-u"
	poolPrincipal := "User:" + token + "-pool"
	resourceU := "kcpacl-uprefix-" + token
	resourcePool := "kcpacl-poolprefix-" + token
	seedNativeACL(t, admin, sarama.AclResourceTopic, resourceU, sarama.AclPatternLiteral, uPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourceU, sarama.AclPatternLiteral, uPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, resourcePool, sarama.AclPatternLiteral, poolPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resourcePool, sarama.AclPatternLiteral, poolPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	// Best-effort teardown by run-unique resource name (whatever principal form
	// CC stored the ACLs under).
	defer func() {
		if acls, ok := cc.tryListACLs(t); ok {
			for _, a := range acls {
				if a.ResourceName == resourceU || a.ResourceName == resourcePool {
					_ = deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a)
				}
			}
		}
	}()

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-uprefix"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false,
		mapping: map[string]string{
			uPrincipal:    "u-" + token,
			poolPrincipal: "pool-" + token,
		},
		include: []string{"User:" + token + "*"},
	})
	out, err := runKCP(t, mf)
	if err != nil {
		require.NotContains(t, out, "must be a User:sa-/u-/pool- id", "u-/pool- prefixes must pass KCP's own mapping validation")
		require.NotContains(t, out, "have no Confluent Cloud service account", "a mapped principal must never hit the unmapped-principal error path")
		t.Logf("apply failed past KCP's own validation and resolution (an upstream CC rejection of the non-existent mapped id is the open, unverified-live question this test surfaces): %v\n%s", err, out)
		return
	}
	t.Logf("CC accepted ACLs for non-existent mapped u-/pool- ids (no principal-existence check at ACL-create time): %s", out)
}

// TestACLsLive_MappingInvalidPrefix_ValidationError proves an invalid
// mapping-value prefix is rejected at manifest validation, before any live call.
func TestACLsLive_MappingInvalidPrefix_ValidationError(t *testing.T) {
	cfg := loadCloudConfig(t)
	// The manifest carries valid cloudCredentials so the new "cloudCredentials
	// required for spec.acls on a CC target" rule doesn't mask the intended
	// invalid-mapping-prefix validation error.
	requireCloudKeys(t, cfg)
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
// (not User:*/ANONYMOUS) principal with no mapping entry. Unlike the validation
// test above, this exercises the live MSK read: the principal is only
// discovered, and only fails, once ReadNativeACLs has listed it from the source.
func TestACLsLive_AutoCreateFalse_UnmappedPrincipal_HardError(t *testing.T) {
	cfg := loadCloudConfig(t)
	// The manifest carries valid cloudCredentials (required for spec.acls on a CC
	// target) so validation passes and the run reaches the intended runtime
	// hard-error on the unmapped principal, not a validation error.
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")

	token := uniqueACLToken()
	principal := "User:" + token + "-unmapped"
	resource := "kcpacl-noautocreate-" + token
	seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-noautocreate"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false,
		include:    []string{"User:" + token + "*"},
	})
	out, err := runKCP(t, mf)
	require.Error(t, err, out)
	require.Contains(t, out, "have no Confluent Cloud service account")
	require.Contains(t, out, principal)
}

// TestACLsLive_DryRunThenApplyThenIdempotent proves --dry-run mutates nothing, a
// real apply creates exactly one service account and one ACL, and a second apply
// is a full no-op (idempotent).
func TestACLsLive_DryRunThenApplyThenIdempotent(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	token := uniqueACLToken()
	principal := "User:" + token + "-app"
	resource := "kcpacl-dryrun-" + token
	seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer cleanupSAForPrincipal(t, cc, principal, resource, sarama.AclResourceTopic, sarama.AclPatternLiteral, sarama.AclOperationRead, sarama.AclPermissionAllow)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-dryrun"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		include:    []string{"User:" + token + "*"},
	})

	dryOut, err := runKCP(t, mf, "--dry-run")
	require.NoError(t, err, dryOut)
	require.Nil(t, cc.findSAForPrincipal(t, principal), "dry-run must not create a service account")
	require.Empty(t, cc.aclsOnResource(t, resource), "dry-run must not create an acl")

	applyOut, err := runKCP(t, mf)
	require.NoError(t, err, applyOut)
	require.Contains(t, applyOut, "serviceAccounts: 1 created, 0 unchanged, 0 drift, 0 failed", applyOut)
	require.Contains(t, applyOut, "acls: 1 created, 0 unchanged, 0 drift, 0 failed", applyOut)

	sa := cc.findSAForPrincipal(t, principal)
	require.NotNil(t, sa)
	got := cc.aclsOnResource(t, resource)
	require.Len(t, got, 1)
	require.Equal(t, "*", got[0].Host)
	require.Equal(t, "Read", got[0].Operation)
	require.NotEmpty(t, got[0].Principal)

	idemOut, err := runKCP(t, mf)
	require.NoError(t, err, idemOut)
	require.Contains(t, idemOut, "serviceAccounts: 0 created, 1 unchanged, 0 drift, 0 failed", idemOut)
	require.Contains(t, idemOut, "acls: 0 created, 1 unchanged, 0 drift, 0 failed", idemOut)
}

// ---------------------------------------------------------------------------
// Opt-in residue sweep (shared-cluster hygiene, not a scenario test).
//
// Every scenario tears down what it created, but a long enough history of a
// broken teardown path can leave the shared live MSK cluster and CC target with
// orphans; uniqueACLToken()'s "kcpacl" prefix is the only marker they all share.
// TestACLsLive_SweepResidue is a manually invoked, best-effort cleanup pass over
// exactly that prefix — never part of the normal suite (see the KCP_ACL_SWEEP
// gate).
// ---------------------------------------------------------------------------

// listAllServiceAccounts lists every CC IAM v2 service account, paginating via
// the standard CC "metadata.next" cursor. msa.CCClient has no List method
// (find-by-name and create are all the product needs), so this talks to the
// wire API directly, mirroring deleteCCServiceAccount.
func listAllServiceAccounts(ctx context.Context, hc clusterlink.HTTPClient, auth clusterlink.Authenticator) ([]msa.ServiceAccount, error) {
	var all []msa.ServiceAccount
	next := msa.DefaultBaseURL + "/iam/v2/service-accounts?page_size=100"
	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		auth.Apply(req)
		res, err := hc.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, fmt.Errorf("unexpected status %d listing CC service accounts: %s", res.StatusCode, body)
		}
		var page struct {
			Data     []msa.ServiceAccount `json:"data"`
			Metadata struct {
				Next string `json:"next"`
			} `json:"metadata"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("unmarshalling CC service account list page: %w", err)
		}
		all = append(all, page.Data...)
		next = page.Metadata.Next
	}
	return all, nil
}

// TestACLsLive_SweepResidue deletes every leftover CC service account, CC ACL,
// and MSK native ACL whose name/resource/principal starts with "kcpacl" — the
// shared prefix every uniqueACLToken() carries. Idempotent and log-and-continue
// throughout, so a partial failure never aborts the sweep of the rest.
//
// Gating: SKIPS unless KCP_ACL_SWEEP=1, IN ADDITION to the normal cloud-creds
// gate — so it never runs as part of `make test-migrate-acls-live`. Meant to be
// run by hand to clean up accumulated orphans.
func TestACLsLive_SweepResidue(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	if os.Getenv("KCP_ACL_SWEEP") != "1" {
		t.Skip("opt-in only: set KCP_ACL_SWEEP=1 to sweep kcpacl-prefixed residue off the shared cluster (never runs as part of the normal suite)")
	}

	dir := t.TempDir()
	target, _, _ := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	// 1. CC service accounts whose display_name starts with "kcpacl".
	sas, err := listAllServiceAccounts(context.Background(), cc.saHC, cc.saAuth)
	require.NoError(t, err)
	saDeleted, saMatched := 0, 0
	for _, sa := range sas {
		if !strings.HasPrefix(sa.DisplayName, "kcpacl") {
			continue
		}
		saMatched++
		if err := deleteCCServiceAccount(context.Background(), cc.saHC, cc.saAuth, sa.ID); err != nil {
			t.Logf("sweep: delete CC service account %s (%s): %v", sa.ID, sa.DisplayName, err)
			continue
		}
		saDeleted++
	}
	t.Logf("sweep: deleted %d/%d kcpacl-prefixed CC service account(s) (of %d total)", saDeleted, saMatched, len(sas))

	// 2. CC ACLs whose resource_name starts with "kcpacl".
	aclDeleted, aclMatched := 0, 0
	if acls, ok := cc.tryListACLs(t); ok {
		for _, a := range acls {
			if !strings.HasPrefix(a.ResourceName, "kcpacl") {
				continue
			}
			aclMatched++
			if err := deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a); err != nil {
				t.Logf("sweep: delete CC acl %+v: %v", a, err)
				continue
			}
			aclDeleted++
		}
	}
	t.Logf("sweep: deleted %d/%d kcpacl-prefixed CC acl(s)", aclDeleted, aclMatched)

	// 3. MSK native ACLs whose principal OR resource-name starts with "kcpacl".
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	resourceAcls, err := admin.ListAcls(sarama.AclFilter{
		ResourceType:              sarama.AclResourceAny,
		ResourcePatternTypeFilter: sarama.AclPatternAny,
		Operation:                 sarama.AclOperationAny,
		PermissionType:            sarama.AclPermissionAny,
	})
	require.NoError(t, err)
	mskDeleted, mskMatched, mskSeen := 0, 0, 0
	for _, ra := range resourceAcls {
		ra := ra
		for _, a := range ra.Acls {
			mskSeen++
			principalName := strings.TrimPrefix(a.Principal, "User:")
			if !strings.HasPrefix(principalName, "kcpacl") && !strings.HasPrefix(ra.ResourceName, "kcpacl") {
				continue
			}
			mskMatched++
			filter := sarama.AclFilter{
				ResourceType:              ra.ResourceType,
				ResourceName:              &ra.ResourceName,
				ResourcePatternTypeFilter: ra.ResourcePatternType,
				Principal:                 &a.Principal,
				Host:                      &a.Host,
				Operation:                 a.Operation,
				PermissionType:            a.PermissionType,
			}
			if _, err := admin.DeleteACL(filter, false); err != nil {
				t.Logf("sweep: delete MSK acl %s %s on %s %q for %s: %v", a.PermissionType.String(), a.Operation.String(), ra.ResourceType.String(), ra.ResourceName, a.Principal, err)
				continue
			}
			mskDeleted++
		}
	}
	t.Logf("sweep: deleted %d/%d kcpacl-matching MSK native acl(s) (of %d scanned)", mskDeleted, mskMatched, mskSeen)
}
