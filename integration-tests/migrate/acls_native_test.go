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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
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
// read-back and teardown calls. hc/auth (the ACL client's creds) are built
// from the SAME target-creds.yaml (and so the same auth) buildACLReconcilers
// gives the product's own ACL client (cmd/migrate/apply/cmd_migrate_apply.go)
// — proving the two independently observe the same target state, not sharing
// any translation logic. saHC/saAuth (the service-account helper client's
// creds) are DIFFERENT: IAM v2 (api.confluent.cloud/iam/v2/...) rejects a
// Kafka cluster API key with "invalid API key: make sure you're using a Cloud
// or Global API Key, and not a Cluster API Key", so they are built from the
// separate Cloud/Global creds file, mirroring buildACLReconcilers' own
// cluster-vs-cloud client split.
type aclTargetClients struct {
	hc           clusterlink.HTTPClient
	auth         clusterlink.Authenticator
	saHC         clusterlink.HTTPClient
	saAuth       clusterlink.Authenticator
	restEndpoint string
	clusterID    string
	// hasCloud reports whether saHC/saAuth are the real Cloud/Global creds (a
	// cloudCredsPath was supplied), rather than the cluster-creds fallback. The
	// legacy /service_accounts endpoint numericToResourceID reads rejects a
	// Kafka cluster key, so read-back principal normalization is only attempted
	// when this is true; a mapping/validation-only test (cloudCredsPath == "")
	// never needs it and must not 401 against that endpoint.
	hasCloud bool
}

// newACLTargetClients builds an aclTargetClients. targetCredsPath is always
// the CLUSTER creds file (Kafka REST v3 ACLs accept a cluster key).
// cloudCredsPath is the separate Cloud/Global creds file for the
// service-account helper client (findSA/tryFindSA/createSA and the SA delete
// in cleanupSAAndItsACLs); pass "" when the caller never exercises those
// helpers (mirrors buildACLReconcilers' harmless cluster-creds fallback for
// manifests that never call IAM v2) — saHC/saAuth then fall back to the
// cluster creds, which is safe only because they go unused in that case.
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
	return aclTargetClients{hc: hc, auth: creds.Authenticator(), saHC: saHC, saAuth: saAuth, restEndpoint: cfg.ccRestEndpoint, clusterID: cfg.ccClusterID, hasCloud: cloudCredsPath != ""}
}

// numericToResourceID builds the harness's OWN, INDEPENDENT numeric-id ->
// "sa-" resource-id map by reading the CC LEGACY /service_accounts endpoint
// directly (the only endpoint that exposes a service account's internal numeric
// id). It deliberately does NOT call the product's
// serviceaccounts.CCClient.NumericToResourceID, keeping this oracle independent
// of the code under test (file header §9.6): Confluent Cloud accepts an ACL
// created with principal "User:sa-..." but returns it on read-back as that
// service account's numeric id (e.g. User:1267635), so the harness applies the
// SAME numeric->sa- rewrite before diffing a read-back ACL set against a
// "User:sa-..." oracle. Uses the Cloud/Global creds (saHC/saAuth) — the legacy
// endpoint, like IAM v2, rejects a Kafka cluster key.
func (c aclTargetClients) numericToResourceID(ctx context.Context) (map[string]string, error) {
	out := map[string]string{}
	next := msa.DefaultBaseURL + "/service_accounts?page_size=100"
	for next != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, next, nil)
		if err != nil {
			return nil, err
		}
		c.saAuth.Apply(req)
		res, err := c.saHC.Do(req)
		if err != nil {
			return nil, err
		}
		body, err := io.ReadAll(res.Body)
		_ = res.Body.Close()
		if err != nil {
			return nil, err
		}
		if res.StatusCode < 200 || res.StatusCode >= 300 {
			return nil, fmt.Errorf("unexpected status %d reading legacy CC service accounts: %s", res.StatusCode, body)
		}
		var page struct {
			Users []struct {
				ID             int64  `json:"id"`
				ResourceID     string `json:"resource_id"`
				ServiceAccount bool   `json:"service_account"`
			} `json:"users"`
			PageInfo struct {
				NextPageToken string `json:"next_page_token"`
			} `json:"page_info"`
		}
		if err := json.Unmarshal(body, &page); err != nil {
			return nil, fmt.Errorf("unmarshalling legacy CC service account page: %w", err)
		}
		for _, u := range page.Users {
			if !u.ServiceAccount || u.ResourceID == "" {
				continue
			}
			out[strconv.FormatInt(u.ID, 10)] = u.ResourceID
		}
		if page.PageInfo.NextPageToken == "" {
			break
		}
		next = msa.DefaultBaseURL + "/service_accounts?page_size=100&page_token=" + url.QueryEscape(page.PageInfo.NextPageToken)
	}
	return out, nil
}

// normalizeReadBackPrincipals rewrites every "User:<numeric>" principal in acls
// to "User:<sa->" using m (numeric-id -> "sa-" resource-id). Principals not of
// that shape, or whose numeric id is absent from m, are left unchanged. Returns
// a new slice; the input is not mutated. This is the harness counterpart to the
// product's acls.Reconciler.normalizeExistingPrincipal, written independently.
func normalizeReadBackPrincipals(acls []types.Acls, m map[string]string) []types.Acls {
	if len(m) == 0 {
		return acls
	}
	out := make([]types.Acls, len(acls))
	for i, a := range acls {
		if numeric, ok := strings.CutPrefix(a.Principal, "User:"); ok {
			if sa, ok := m[numeric]; ok {
				a.Principal = "User:" + sa
			}
		}
		out[i] = a
	}
	return out
}

func (c aclTargetClients) listACLs(t *testing.T) []types.Acls {
	t.Helper()
	acls, err := macls.NewACLClient(c.restEndpoint, c.clusterID, c.hc, c.auth).List(context.Background())
	require.NoError(t, err)
	// Normalize read-back principals CC reports as "User:<numeric>" back to
	// "User:sa-..." so the oracle can diff against a "User:sa-..." expectation
	// (see numericToResourceID). Only when Cloud/Global creds are in hand; a
	// mapping/validation-only test (hasCloud == false) never needs it and must
	// not call the legacy endpoint with a cluster key.
	if c.hasCloud {
		m, err := c.numericToResourceID(context.Background())
		require.NoError(t, err)
		acls = normalizeReadBackPrincipals(acls, m)
	}
	return acls
}

func (c aclTargetClients) findSA(t *testing.T, displayName string) *msa.ServiceAccount {
	t.Helper()
	sa, err := msa.NewCCClient(msa.DefaultBaseURL, c.saHC, c.saAuth).FindByDisplayName(context.Background(), displayName)
	require.NoError(t, err)
	return sa
}

// tryFindSA is the cleanup-safe counterpart to findSA: on a read error it
// logs and reports failure via ok=false instead of asserting, so a single
// transient CC read error during a DEFERRED cleanup call never aborts the
// rest of that call (e.g. skips the SA delete) via t.FailNow()/Goexit(). Only
// call this from teardown/defer/t.Cleanup paths — assertions on the main
// (non-cleanup) path must keep using findSA.
func (c aclTargetClients) tryFindSA(t *testing.T, displayName string) (sa *msa.ServiceAccount, ok bool) {
	t.Helper()
	sa, err := msa.NewCCClient(msa.DefaultBaseURL, c.saHC, c.saAuth).FindByDisplayName(context.Background(), displayName)
	if err != nil {
		t.Logf("cleanup: find service account %q failed, continuing: %v", displayName, err)
		return nil, false
	}
	return sa, true
}

// tryListACLs is the cleanup-safe counterpart to listACLs: on a read error it
// logs and reports failure via ok=false instead of asserting, for the same
// reason as tryFindSA above. Only call this from teardown/defer/t.Cleanup
// paths — assertions on the main (non-cleanup) path must keep using listACLs.
func (c aclTargetClients) tryListACLs(t *testing.T) (acls []types.Acls, ok bool) {
	t.Helper()
	acls, err := macls.NewACLClient(c.restEndpoint, c.clusterID, c.hc, c.auth).List(context.Background())
	if err != nil {
		t.Logf("cleanup: list CC acls failed, continuing: %v", err)
		return nil, false
	}
	// Best-effort normalization (see listACLs): a numeric-map read failure in a
	// teardown/defer path must not abort cleanup — fall back to the raw (numeric)
	// principals rather than failing.
	if c.hasCloud {
		if m, err := c.numericToResourceID(context.Background()); err != nil {
			t.Logf("cleanup: map CC service-account numeric ids failed, continuing without normalization: %v", err)
		} else {
			acls = normalizeReadBackPrincipals(acls, m)
		}
	}
	return acls, true
}

func (c aclTargetClients) createSA(t *testing.T, displayName, description string) *msa.ServiceAccount {
	t.Helper()
	sa, err := msa.NewCCClient(msa.DefaultBaseURL, c.saHC, c.saAuth).Create(context.Background(), displayName, description)
	require.NoError(t, err)
	return sa
}

// cleanupSAAndItsACLs deletes every CC ACL referencing "User:"+the found
// service account's id, then the service account itself. A no-op (not an
// error) if no service account with displayName exists — safe to call
// unconditionally from a defer at the top of a test, before the run that
// might create it.
//
// Every read here goes through the try* (log-and-continue) variants, never
// require.NoError: this whole function runs from a defer, so a transient CC
// read error must never trip t.FailNow()/runtime.Goexit() and abort the rest
// of this cleanup call (e.g. skip the service-account delete) — that would
// leave residue on the shared live CC cluster, contradicting this file's
// "cleanup always completes" invariant (see the file header). If the SA
// lookup itself fails we don't know the SA id and so can't compute its
// principal to target ACL deletes either, but a listACLs failure must not
// stop us from still deleting the service account below.
func (c aclTargetClients) cleanupSAAndItsACLs(t *testing.T, displayName string) {
	t.Helper()
	sa, ok := c.tryFindSA(t, displayName)
	if !ok || sa == nil {
		return
	}
	principal := "User:" + sa.ID
	if acls, ok := c.tryListACLs(t); ok {
		for _, a := range aclsForPrincipal(acls, principal) {
			if err := deleteCCACL(context.Background(), c.hc, c.auth, c.restEndpoint, c.clusterID, a); err != nil {
				t.Logf("cleanup: delete CC acl %+v: %v", a, err)
			}
		}
	}
	if err := deleteCCServiceAccount(context.Background(), c.saHC, c.saAuth, sa.ID); err != nil {
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

// aclsForResourceNames scopes a raw CC ACL read-back to only the given
// resource names — the run-scoping half of Fix 1 (see runACLScenario's call
// site): filtering out everything the CURRENT scenario didn't itself seed
// keeps the oracle diff immune to residue other/earlier runs left on the
// shared live CC cluster under a reused literal resource name.
func aclsForResourceNames(all []types.Acls, resourceNames []string) []types.Acls {
	set := make(map[string]bool, len(resourceNames))
	for _, n := range resourceNames {
		set[n] = true
	}
	var out []types.Acls
	for _, a := range all {
		if set[a.ResourceName] {
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
// delete-by-filter endpoint (DELETE .../acls?resource_type=...&...). auth is
// applied to the outgoing request exactly like the product's own clients do
// (hc carries transport only, never an Authorization header — see
// aclTargetClients' doc comment above) — pass the CLUSTER creds authenticator
// (c.auth/cc.auth), matching the client that legitimately talks to Kafka REST
// v3 ACLs.
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

// deleteCCServiceAccount removes one CC service account via IAM v2
// (msa.CCClient likewise has no Delete method — additive-only by design).
// auth is applied to the outgoing request (see deleteCCACL's doc comment for
// why that's required at all): IAM v2 rejects a Kafka cluster API key, so
// EVERY call site here must pass the CLOUD/Global creds authenticator
// (c.saAuth/cc.saAuth) paired with the same-creds saHC — never the cluster
// creds — or the delete 401s with "make sure you're using a Cloud or Global
// API Key, and not a Cluster API Key" (design §9.9/teardown must always
// complete: this is the fix for exactly that failure).
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
// key env vars are unset. Service-account auto-create (spec.serviceAccounts.
// autoCreate) drives the IAM v2 API, which — unlike the /kafka/v3 REST surface —
// rejects a Kafka cluster API key, so those tests need a separate Cloud/Global
// key. Mapping/validation-only tests never call IAM v2 and so don't call this.
func requireCloudKeys(t *testing.T, cfg cloudConfig) {
	t.Helper()
	if cfg.ccCloudKey == "" || cfg.ccCloudSecret == "" {
		t.Skip("cloud suite: CC_CLOUD_KEY/CC_CLOUD_SECRET not set; skipping service-account auto-create tests")
	}
}

// writeCloudCredsFile writes the CC Cloud/Global API key creds file (api_key/
// api_secret) used by spec.target.cloudCredentials and returns its path. It is
// a DIFFERENT file from writeCloudCreds' cc.yaml (the Kafka cluster key), since
// the two key types are distinct on Confluent Cloud.
func writeCloudCredsFile(t *testing.T, dir string, cfg cloudConfig) string {
	t.Helper()
	q := func(s string) string {
		return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(s) + `"`
	}
	p := filepath.Join(dir, "cc-cloud.yaml")
	require.NoError(t, os.WriteFile(p, []byte("api_key: "+q(cfg.ccCloudKey)+"\napi_secret: "+q(cfg.ccCloudSecret)+"\n"), 0600))
	return p
}

// aclManifestOpts configures spec.serviceAccounts + spec.acls for one
// scenario's manifest.
type aclManifestOpts struct {
	autoCreate bool
	mapping    map[string]string
	include    []string
	exclude    []string
	// cloudCreds, when non-empty, is written as spec.target.cloudCredentials
	// (the CC Cloud/Global API key file). autoCreate manifests must set it —
	// IAM v2 rejects the Kafka cluster key; mapping/validation-only manifests
	// leave it empty.
	cloudCreds string
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
	b.WriteString("  target:\n    type: confluent-cloud\n    clusterId: " + cfg.ccClusterID + "\n    clusterCredentials: " + targetCreds + "\n")
	if opts.cloudCreds != "" {
		b.WriteString("    cloudCredentials: " + opts.cloudCreds + "\n")
	}
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
//
// expectDropped marks a scenario whose EVERY seeded ACL is expected to be
// dropped by NormalizeForCC (expectSeedIdx is therefore empty). When every
// ACL for a principal is dropped, there is no surviving ACL to author a CC
// ACL from, and so — correctly — no principal is ever resolved and no
// service account gets created for it. runACLScenario branches on this field
// to assert that absence, instead of (wrongly) expecting a service account to
// exist for a principal whose only ACL was, by design, thrown away.
type aclScenario struct {
	name            string
	principalSuffix string
	seed            []nativeACL
	expectSeedIdx   []int
	expectDropped   bool
}

// seedResourceNames returns the distinct resource names s's seed entries
// target, in seed order. runACLScenario uses this to scope its CC ACL
// read-back to exactly the resources THIS scenario touched (Fix 1).
func (s aclScenario) seedResourceNames() []string {
	seen := make(map[string]bool, len(s.seed))
	var out []string
	for _, sd := range s.seed {
		if !seen[sd.ResourceName] {
			seen[sd.ResourceName] = true
			out = append(out, sd.ResourceName)
		}
	}
	return out
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
		expectDropped: true,
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
		expectDropped: true,
	},
	{
		name: "cluster_clusteraction_droplist_dropped",
		seed: []nativeACL{
			{ResourceType: sarama.AclResourceCluster, ResourceName: "kafka-cluster", PatternType: sarama.AclPatternLiteral, Operation: sarama.AclOperationClusterAction, Permission: sarama.AclPermissionAllow, Host: "*"},
		},
		// ClusterAction is drop-list (design §5 rule 4): inter-broker only.
		expectDropped: true,
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
func runACLScenario(t *testing.T, cfg cloudConfig, cc aclTargetClients, admin sarama.ClusterAdmin, dir, target, cloudCreds, sourceRead string, s aclScenario) {
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
		cloudCreds: cloudCreds,
		include:    []string{"User:" + base + "*"},
	})

	out, err := runKCP(t, mf)
	require.NoError(t, err, out)

	if s.expectDropped {
		// Every seeded ACL is expected to be dropped by NormalizeForCC: no
		// surviving ACL means no principal is ever resolved, so (b) no
		// service account must exist for it. (a) — no CC ACL was created for
		// it — follows from (b) by construction (the reconciler always
		// resolves/creates the service account BEFORE it can author an ACL
		// under that principal's id; TestACLsLive_DryRunThenApplyThenIdempotent
		// already proves that 1:1 create-count invariant), so we deliberately
		// do NOT additionally re-scan the CC ACL list by resource name here:
		// on this shared live cluster a resource name like "kafka-cluster" is
		// exactly the kind of unscoped, not-this-run key that picks up
		// "extra elements" left by other principals/runs — the same failure
		// mode Fix 1 (below) removes from the create-and-verify path.
		require.Nil(t, cc.findSA(t, displayName),
			"scenario %q: every seeded ACL is expected to be dropped, so no service account should exist for principal %q", s.name, principal)
		return
	}

	sa := cc.findSA(t, displayName)
	require.NotNil(t, sa, "expected a service account named %q for principal %q", displayName, principal)
	require.Equal(t, oracleDescriptionFor(principal), sa.Description)

	resolved := "User:" + sa.ID
	var expected []types.Acls
	for _, idx := range s.expectSeedIdx {
		sd := s.seed[idx]
		expected = append(expected, canonicalACL(sd.ResourceType, sd.ResourceName, sd.PatternType, resolved, "*", sd.Operation, sd.Permission))
	}
	// Scope the read-back to THIS run's own resources before diffing against
	// the oracle: several scenarios in nativeMatrixScenarios/
	// principalMatrixScenarios reuse the same literal resource name (e.g.
	// "kcpacl-topicA") across rows, and the shared live CC cluster
	// accumulates ACLs from other/earlier runs (see the file header's
	// teardown-always-completes invariant, and the fix for the SA-delete 401
	// above) — an unscoped list would let those show up as "extra elements"
	// against this scenario's oracle. Restricting to the resource name(s)
	// THIS scenario actually seeded, on top of the exact-principal filter
	// below (resolved is this run's freshly created service account, never
	// reused), makes the comparison immune to that residue.
	scoped := aclsForResourceNames(cc.listACLs(t), s.seedResourceNames())
	actual := aclsForPrincipal(scoped, resolved)
	require.ElementsMatch(t, expected, actual, "CC ACL set for principal %q (scenario %q)", principal, s.name)
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
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	token := uniqueACLToken()
	principal := "User:CN=b-1." + token + ".kafka.us-east-1.amazonaws.com"
	// resourceName carries the token too (not a bare literal): the read-back
	// assertion below scopes by exact resourceName alone (no principal to
	// filter by, since the ACL is expected to be dropped entirely) — Fix 1's
	// same shared-cluster-residue concern applies here, so the resource name
	// itself must be run-unique or a static name could accumulate an
	// unrelated ACL from another run and fail this test's require.Empty.
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

	require.Empty(t, aclsForResourceName(cc.listACLs(t), resourceName), "an MSK-broker-shaped principal's ACL must be dropped entirely, never migrated")
}

// TestACLsLive_PrincipalMatrix_CollisionPair covers the collision case from
// design §9.4: two DISTINCT principals that sanitize+truncate to the SAME
// 55-char base must still resolve to two DISTINCT service accounts (distinct
// hash8 suffixes) — never merged.
func TestACLsLive_PrincipalMatrix_CollisionPair(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

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
		cloudCreds: cloudCreds,
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

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-skip"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
		// Scoped by resourceName, NOT by principal: "User:*" as an include
		// glob (filterACLs/path.Match semantics) matches EVERY principal
		// starting with "User:" — i.e. virtually every native ACL on the
		// shared live MSK cluster, not just this test's own literal "User:*"
		// principal. That unscoped sweep is what let a run of this suite
		// report far more ACLs migrated than the scenario count. starResource
		// and anonResource are exact literal matches (no glob metacharacters)
		// unique to this run, so include only ever selects this test's own
		// two seeded ACLs.
		include: []string{starResource, anonResource},
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
	// This test creates and tears down a service account via the SA helper
	// client (createSA/deleteCCServiceAccount below), which talks to IAM v2
	// and so needs the Cloud/Global key, same as requireCloudKeys' doc
	// explains for autoCreate tests — even though this manifest itself uses
	// mapping, not autoCreate.
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	base := uniqueACLToken()
	normalPrincipal := "User:" + base + "-app"
	anonPrincipal := "User:ANONYMOUS"

	preexisting := cc.createSA(t, "kcpacl-preexisting-"+base, "kcpacl-test:preexisting")
	defer func() { _ = deleteCCServiceAccount(context.Background(), cc.saHC, cc.saAuth, preexisting.ID) }()

	normalResource := "kcpacl-mapping-normal-" + base
	anonResource := "kcpacl-mapping-anon-" + base
	seedNativeACL(t, admin, sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, normalPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, normalResource, sarama.AclPatternLiteral, normalPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	seedNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, anonPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, anonResource, sarama.AclPatternLiteral, anonPrincipal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	defer func() {
		// tryListACLs, not listACLs: this runs from a defer, so a transient
		// read error here must not abort the rest of teardown (see
		// cleanupSAAndItsACLs' doc comment above for why).
		if acls, ok := cc.tryListACLs(t); ok {
			for _, a := range aclsForPrincipal(acls, "User:"+preexisting.ID) {
				_ = deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a)
			}
		}
	}()

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-mapping"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: false, // mapping-only: proves resolution works with no autoCreate involved
		mapping: map[string]string{
			normalPrincipal: preexisting.ID,
			anonPrincipal:   preexisting.ID,
		},
		// anonResource (a run-unique literal, no glob metacharacters), not the
		// literal "User:ANONYMOUS" principal itself: unlike normalPrincipal
		// (already scoped via base), "User:ANONYMOUS" is a fixed Kafka
		// literal shared by every run of this suite (and any pre-existing
		// ANONYMOUS ACLs on the shared live cluster), so including it by
		// principal would sweep in ACLs this test never seeded. Scoping by
		// this run's resourceName instead keeps the include set to exactly
		// this test's own two seeded ACLs.
		include: []string{"User:" + base + "*", anonResource},
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
	// No cloud creds path: this test's mapping values (u-/pool- ids) are never
	// looked up or created via the service-account helper client, only
	// asserted against via the cluster-creds ACL client, so saHC/saAuth
	// harmlessly fall back to the cluster creds (see newACLTargetClients).
	cc := newACLTargetClients(t, cfg, target, "")

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
		// tryListACLs, not listACLs: this runs from a defer, so a transient
		// read error here must not abort the rest of teardown (see
		// cleanupSAAndItsACLs' doc comment above for why).
		acls, ok := cc.tryListACLs(t)
		if !ok {
			return
		}
		for _, id := range []string{dummyU, dummyPool} {
			for _, a := range aclsForPrincipal(acls, "User:"+id) {
				_ = deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a)
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
	requireCloudKeys(t, cfg)
	admin := newMSKIAMAdmin(t, splitCSV(cfg.mskIAMBootstrap), cfg.mskRegion)
	defer func() { _ = admin.Close() }()
	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	base := uniqueACLToken()
	principal := "User:" + base + "-app"
	resource := "kcpacl-dryrun-" + base
	seedNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)
	defer deleteNativeACL(t, admin, sarama.AclResourceTopic, resource, sarama.AclPatternLiteral, principal, "*", sarama.AclOperationRead, sarama.AclPermissionAllow)

	displayName := oracleDeriveDisplayName(principal)
	defer cc.cleanupSAAndItsACLs(t, displayName)

	mf := writeACLManifest(t, dir, uniqueLinkName("kcpacl-dryrun"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, aclManifestOpts{
		autoCreate: true,
		cloudCreds: cloudCreds,
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

// ---------------------------------------------------------------------------
// Opt-in residue sweep (shared-cluster hygiene, not a scenario/oracle test).
//
// Every scenario in this file tears down exactly what it created, but a long
// enough history of a broken teardown path (the SA-delete-via-cluster-key 401
// this file's fix above addresses) has left the shared live MSK cluster and
// CC target with accumulated orphans: uniqueACLToken()'s "kcpacl" prefix is
// the ONLY marker they all share. TestACLsLive_SweepResidue is a manually
// invoked, best-effort cleanup pass over exactly that prefix — never part of
// the normal suite (see the KCP_ACL_SWEEP gate below).
// ---------------------------------------------------------------------------

// listAllServiceAccounts lists every CC IAM v2 service account, paginating via
// the standard CC "metadata.next" full-URL cursor. There is no List method on
// msa.CCClient (find-by-name and create are all the product needs), so this
// talks to the wire API directly, mirroring deleteCCServiceAccount above.
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

// TestACLsLive_SweepResidue deletes every leftover CC service account, CC
// ACL, and MSK native ACL whose name/resource/principal starts with
// "kcpacl" — the shared prefix every uniqueACLToken() carries (see its doc
// comment above). It is idempotent and log-and-continue throughout, so a
// partial failure on one item never aborts the sweep of the rest, and
// reports how many of each kind it deleted via t.Logf.
//
// Gating: this test SKIPS unless KCP_ACL_SWEEP=1 is set, IN ADDITION to the
// normal loadCloudConfig/requireCloudKeys cloud-creds gate every other test
// in this file already needs — so it never runs as part of `make
// test-migrate-acls-live` (or any other invocation of this suite) unless
// that env var is explicitly set. It is meant to be run by hand, once, to
// clean up accumulated orphans.
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
	saDeleted := 0
	saMatched := 0
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
