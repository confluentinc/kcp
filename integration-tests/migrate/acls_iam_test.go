//go:build integration

// This file is the LIVE (Tier 2) integration test for the AWS MSK IAM
// authorization plane of the MSK→Confluent Cloud ACL migration (Phase 1B
// design, explicit principalArns mode — task-7 brief). It complements
// acls_native_test.go (the native-ACL plane's live suite): the same house
// pattern applies here — env-gated via loadCloudConfig/requireCloudKeys, a
// run-unique scoped seed, a real `kcp migrate apply`, assertion of the
// command's OWN reported summary, a scoped read-back of concrete facts, and
// re-apply idempotency — but the "native ACL on MSK" seed is replaced by a
// real AWS IAM role carrying a "kafka-cluster:*" inline policy, and the
// "read" side exercises internal/migrate/acls' ReadIAMACLs/translateStatements
// instead of ReadNativeACLs.
//
// Slice scope: only the explicit principalArns path (spec.acls.iam.principalArns)
// is exercised. discoverAllRoles (enumeration) and verifyEffectiveAccess
// (SimulatePrincipalPolicy) are Phase 1B Slice 2 and return a
// "not yet implemented" error from cmd/migrate/apply — out of scope here.
//
// Env-gating: loadCloudConfig + requireCloudKeys, the same CC_*/MSK_* gate as
// the rest of the live suite, PLUS a new MSK_CLUSTER_ARN (this file's own
// gate, requireMSKClusterArn) — no existing cloudConfig field carries a full
// MSK cluster ARN, and this test must never invent one; the operator running
// it live supplies the playground cluster's real ARN (e.g. via
// `aws kafka list-clusters-v2 --cluster-name-filter kcpplaygroundpublic
// --region us-east-1 --query 'ClusterInfoList[0].ClusterArn'`). Every other
// live test in this package is unaffected: MSK_CLUSTER_ARN is read by this
// file alone and defaults to "" (skip) everywhere else, including
// `make test-go`.
package migrate

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Env-gating for this file's one extra input (see file doc comment).
// ---------------------------------------------------------------------------

// requireMSKClusterArn reads the playground MSK cluster's ARN from
// MSK_CLUSTER_ARN and SKIPS the test cleanly when unset, mirroring
// requireCloudKeys' separate-gate pattern (acls_native_test.go): a value this
// specific to one test file, with no existing carrier in cloudConfig, is
// gated on its own rather than forcing every other live test to also demand
// it (or, worse, guessing at an ARN).
func requireMSKClusterArn(t *testing.T) string {
	t.Helper()
	arn := os.Getenv("MSK_CLUSTER_ARN")
	if arn == "" {
		t.Skip("cloud suite: MSK_CLUSTER_ARN not set; skipping live IAM-plane ACL migration test")
	}
	return arn
}

// ---------------------------------------------------------------------------
// AWS IAM plumbing: seed/tear down a real throwaway role (design: mirrors
// seedNativeACL/deleteNativeACL's role in acls_native_test.go, but on the AWS
// IAM control plane rather than the Kafka ACL API).
// ---------------------------------------------------------------------------

// iamTrustPolicy is a minimal, inert AssumeRolePolicyDocument. IAM requires
// one on every role; this test never assumes the role (KCP's IAM read path
// only fetches its INLINE policies, GetRolePolicy — see internal/services/iam),
// so the trust principal's identity is immaterial, but IAM still validates the
// document's shape at CreateRole time.
const iamTrustPolicy = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Principal":{"Service":"ec2.amazonaws.com"},"Action":"sts:AssumeRole"}]}`

// newTestIAMClient builds an AWS IAM client from the default credential chain
// — the same chain client.NewIAMClient (internal/client/iam.go) uses when the
// real `kcp migrate apply` run (via cmd/migrate/apply's newIAMFetcher) reads
// this role's policies back. This is a SEPARATE client instance built only to
// seed/tear down the throwaway role; KCP builds its own when it runs.
func newTestIAMClient(t *testing.T) *iam.Client {
	t.Helper()
	cfg, err := config.LoadDefaultConfig(context.Background())
	require.NoError(t, err, "load AWS config for IAM role seeding (default credential chain)")
	return iam.NewFromConfig(cfg)
}

// mskResourceArn derives a Topic/Group resource ARN from the source cluster's
// ARN by swapping its "cluster/" restype segment for restype and appending
// the resource name (".../cluster/<name>/<uuid>" -> ".../topic/<name>/<uuid>/<resourceName>").
// Region/account are inherited verbatim from clusterArn — internal/migrate/acls'
// clusterArnMatches (iam_translate.go) keys only on cluster identity
// (name+uuid), deliberately excluding region/account from the comparison, so
// this never needs to invent either.
func mskResourceArn(clusterArn, restype, resourceName string) string {
	return strings.Replace(clusterArn, ":cluster/", ":"+restype+"/", 1) + "/" + resourceName
}

// seedIAMRole creates a run-unique throwaway IAM role carrying one inline
// policy: two "kafka-cluster:*" statements, one scoped to topicArn and one to
// groupArn — a realistic shape (kafka-cluster:* is how MSK IAM grants are
// commonly authored; see the reference generator in
// cmd/create_asset/migrate_acls/iam_acls) that also exercises the
// per-resource-type expansion translateStatements performs for it (fix 3,
// iam_translate.go doc comment). Returns the role's ARN exactly as AWS
// assigns it (never reconstructed by hand). Teardown is registered via
// t.Cleanup in the correct IAM order (inline policy deleted before the role —
// t.Cleanup runs LIFO, so registering DeleteRole first and
// DeleteRolePolicy second is what makes that ordering hold), and is
// log-and-continue so a transient AWS error here never masks the test's own
// assertions.
func seedIAMRole(t *testing.T, iamClient *iam.Client, roleName, topicArn, groupArn string) (roleArn string) {
	t.Helper()
	ctx := context.Background()

	createOut, err := iamClient.CreateRole(ctx, &iam.CreateRoleInput{
		RoleName:                 aws.String(roleName),
		AssumeRolePolicyDocument: aws.String(iamTrustPolicy),
		Description:              aws.String("kcp live-test throwaway role (Phase 1B IAM-plane ACL migration, explicit mode); safe to delete"),
	})
	require.NoError(t, err, "create throwaway IAM role %s", roleName)
	roleArn = aws.ToString(createOut.Role.Arn)
	t.Cleanup(func() {
		if _, err := iamClient.DeleteRole(context.Background(), &iam.DeleteRoleInput{RoleName: aws.String(roleName)}); err != nil {
			t.Logf("cleanup: delete IAM role %s: %v", roleName, err)
		}
	})

	policyDoc, err := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{
			{"Effect": "Allow", "Action": "kafka-cluster:*", "Resource": []string{topicArn}},
			{"Effect": "Allow", "Action": "kafka-cluster:*", "Resource": []string{groupArn}},
		},
	})
	require.NoError(t, err, "marshal throwaway role's inline policy document")

	const policyName = "kcp-live-test-policy"
	_, err = iamClient.PutRolePolicy(ctx, &iam.PutRolePolicyInput{
		RoleName:       aws.String(roleName),
		PolicyName:     aws.String(policyName),
		PolicyDocument: aws.String(string(policyDoc)),
	})
	require.NoError(t, err, "attach inline policy to throwaway IAM role %s", roleName)
	t.Cleanup(func() {
		if _, err := iamClient.DeleteRolePolicy(context.Background(), &iam.DeleteRolePolicyInput{RoleName: aws.String(roleName), PolicyName: aws.String(policyName)}); err != nil {
			t.Logf("cleanup: delete IAM role policy %s/%s: %v", roleName, policyName, err)
		}
	})

	waitForRolePolicyVisible(t, iamClient, roleName, policyName)
	return roleArn
}

// waitForRolePolicyVisible polls GetRolePolicy until the just-written inline
// policy is readable, bounding IAM's eventual-consistency window before the
// real `kcp migrate apply` run tries to read it — a read landing inside that
// window would silently see zero IAM-derived ACLs (not an error), which would
// otherwise flake this test rather than fail it loudly.
func waitForRolePolicyVisible(t *testing.T, iamClient *iam.Client, roleName, policyName string) {
	t.Helper()
	deadline := time.Now().Add(30 * time.Second)
	var lastErr error
	for time.Now().Before(deadline) {
		_, lastErr = iamClient.GetRolePolicy(context.Background(), &iam.GetRolePolicyInput{RoleName: aws.String(roleName), PolicyName: aws.String(policyName)})
		if lastErr == nil {
			return
		}
		time.Sleep(2 * time.Second)
	}
	t.Fatalf("inline policy %s/%s on role %s never became visible via GetRolePolicy: %v", policyName, roleName, roleName, lastErr)
}

// ---------------------------------------------------------------------------
// Manifest generation: spec.acls.iam (explicit principalArns mode).
// ---------------------------------------------------------------------------

// iamACLManifestOpts configures spec.serviceAccounts + spec.acls (incl. the
// "iam:" block) for this file's one live scenario. A deliberately narrower
// sibling of acls_native_test.go's aclManifestOpts: this scenario never needs
// native-ACL knobs (mapping/exclude), only autoCreate, the include glob, and
// the IAM block.
type iamACLManifestOpts struct {
	autoCreate    bool
	cloudCreds    string
	include       []string
	clusterArn    string
	principalArns []string
}

// writeIAMACLManifest writes a source-read-only (no cluster link) MSK→CC
// migration manifest with a spec.acls.iam block and returns its path. Mirrors
// writeACLManifest (acls_native_test.go) field-for-field, plus the nested
// "iam:" block (manifest.ACLsIAM).
func writeIAMACLManifest(t *testing.T, dir, name string, cfg cloudConfig, sourceBootstrap []string, sourceCreds, targetCreds string, opts iamACLManifestOpts) string {
	t.Helper()
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
	// Any acls→CC manifest requires cloudCredentials (validate.go: "required for
	// spec.acls on a confluent-cloud target"); fall back to a freshly written
	// Cloud/Global creds file when the caller left this empty.
	cloudCreds := opts.cloudCreds
	if cloudCreds == "" {
		cloudCreds = writeCloudCredsFile(t, dir, cfg)
	}
	b.WriteString("    cloudCredentials: " + cloudCreds + "\n")
	b.WriteString("    kafka:\n      restEndpoint: " + cfg.ccRestEndpoint + "\n")
	b.WriteString(fmt.Sprintf("  serviceAccounts:\n    autoCreate: %v\n", opts.autoCreate))
	b.WriteString("  acls:\n    include: " + yamlList(opts.include) + "\n")
	b.WriteString("    iam:\n      clusterArn: " + yq(opts.clusterArn) + "\n      principalArns: " + yamlList(opts.principalArns) + "\n")

	mf := filepath.Join(dir, name+".yaml")
	require.NoError(t, os.WriteFile(mf, []byte(b.String()), 0600))
	return mf
}

// opsOf extracts the Operation field of each ACL, for an order-independent
// require.ElementsMatch comparison against a deterministically-predicted set.
func opsOf(acls []types.Acls) []string {
	out := make([]string, len(acls))
	for i, a := range acls {
		out[i] = a.Operation
	}
	return out
}

// ---------------------------------------------------------------------------
// The live scenario.
// ---------------------------------------------------------------------------

// TestACLsLive_IAMPlane_ExplicitPrincipal is Slice 1's live (Tier 2) proof of
// the AWS MSK IAM authorization plane (design Phase 1B, task-7 brief): it
// seeds a real AWS IAM role carrying a "kafka-cluster:*" inline policy scoped
// to the playground MSK cluster's topic+group resources, points
// spec.acls.iam at it (explicit principalArns mode — discoverAllRoles/
// verifyEffectiveAccess are Slice 2), runs the real `kcp migrate apply`, and
// asserts the migrated CC ACLs match the policy's grants exactly (after
// operation-implication dedup, same rule the native plane goes through) plus
// re-apply idempotency ("0 created, N unchanged") — the same idempotency
// contract acls_native_test.go proves for the native plane, now proven for
// the IAM plane's ReadIAMACLs/translateStatements path.
//
// Deterministic expected result (NormalizeForCC, internal/migrate/acls/normalize.go):
//   - A "kafka-cluster:*" grant on ONE topic ARN expands (translateStatements'
//     arnDenotedResourceType, matchAll=false) to all 8 Topic-typed AclMap
//     entries: Alter, AlterConfigs, Create, Delete, Describe, DescribeConfigs,
//     Read, Write. Topic is CC-unrestricted (ccValidOperations has no "Topic"
//     entry) so all 8 pass stage2; stage4 drops Describe (implied by the
//     co-present Read/Write/Alter/Delete) and DescribeConfigs (implied by the
//     co-present AlterConfigs) — 6 survive: Alter, AlterConfigs, Create,
//     Delete, Read, Write.
//   - The same grant on ONE group ARN expands to Group's 3 AclMap entries:
//     AlterGroup->Read, DeleteGroup->Delete, DescribeGroup->Describe. Group's
//     CC-valid set is exactly {Read, Describe, Delete} (ccValidOperations), so
//     all 3 pass stage2; stage4 drops Describe (implied by the co-present
//     Read/Delete) — 2 survive: Read, Delete.
//   - Total: 8 ACLs, 1 service account (for "User:<roleName>", per
//     translateStatements' principalFromArn — the role ARN's own name, not
//     KCP's SA display-name derivation, since the SA lookup here is by
//     description contract, never by re-deriving the display name).
func TestACLsLive_IAMPlane_ExplicitPrincipal(t *testing.T) {
	cfg := loadCloudConfig(t)
	requireCloudKeys(t, cfg)
	clusterArn := requireMSKClusterArn(t)

	dir := t.TempDir()
	target, _, sourceRead := writeCloudCreds(t, dir, cfg, "iam")
	cloudCreds := writeCloudCredsFile(t, dir, cfg)
	cc := newACLTargetClients(t, cfg, target, cloudCreds)

	iamClient := newTestIAMClient(t)
	token := uniqueACLToken() // "kcpacl<run>-<seq>": alnum+hyphen, valid as an IAM RoleName verbatim.
	roleName := token
	topicName := "kcpacl-iam-topic-" + token
	groupName := "kcpacl-iam-group-" + token
	topicArn := mskResourceArn(clusterArn, "topic", topicName)
	groupArn := mskResourceArn(clusterArn, "group", groupName)

	roleArn := seedIAMRole(t, iamClient, roleName, topicArn, groupArn)
	principal := "User:" + roleName

	wantTopicOps := []string{"Alter", "AlterConfigs", "Create", "Delete", "Read", "Write"}
	wantGroupOps := []string{"Read", "Delete"}

	// CC-side teardown, deferred before the run (design §9.9 / house pattern:
	// so it executes even on failure): find the SA KCP auto-creates for this
	// principal, delete the exact ACLs it authors (reconstructed from the
	// deterministic expected op sets above — delete-by-filter is symmetric
	// with the create, which used "User:sa-...", not CC's numeric read-back
	// form), then the SA itself. A failed run before any SA exists makes this
	// a no-op via tryFindSAForPrincipal. The seeded AWS IAM role/policy are
	// torn down separately via t.Cleanup inside seedIAMRole.
	defer func() {
		sa, ok := cc.tryFindSAForPrincipal(t, principal)
		if !ok || sa == nil {
			return
		}
		for _, op := range wantTopicOps {
			a := types.Acls{ResourceType: "Topic", ResourceName: topicName, ResourcePatternType: "Literal", Principal: "User:" + sa.ID, Host: "*", Operation: op, PermissionType: "Allow"}
			if err := deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a); err != nil {
				t.Logf("cleanup: delete CC acl %+v: %v", a, err)
			}
		}
		for _, op := range wantGroupOps {
			a := types.Acls{ResourceType: "Group", ResourceName: groupName, ResourcePatternType: "Literal", Principal: "User:" + sa.ID, Host: "*", Operation: op, PermissionType: "Allow"}
			if err := deleteCCACL(context.Background(), cc.hc, cc.auth, cc.restEndpoint, cc.clusterID, a); err != nil {
				t.Logf("cleanup: delete CC acl %+v: %v", a, err)
			}
		}
		if err := deleteCCServiceAccount(context.Background(), cc.saHC, cc.saAuth, sa.ID); err != nil {
			t.Logf("cleanup: delete CC service account %s: %v", sa.ID, err)
		}
	}()

	mf := writeIAMACLManifest(t, dir, uniqueLinkName("kcpacl-iam"), cfg, splitCSV(cfg.mskIAMBootstrap), sourceRead, target, iamACLManifestOpts{
		autoCreate:    true,
		cloudCreds:    cloudCreds,
		include:       []string{"User:" + roleName + "*"},
		clusterArn:    clusterArn,
		principalArns: []string{roleArn},
	})

	// 1. Apply — command's own reported create counts.
	out, err := runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "serviceAccounts: 1 created, 0 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, "acls: 8 created, 0 unchanged, 0 drift, 0 failed", out)

	// 2. The auto-created service account exists, located by its description
	// contract (kcp:source-principal=<principal>), never by re-deriving the
	// display name.
	sa := cc.findSAForPrincipal(t, principal)
	require.NotNil(t, sa, "expected a service account for IAM principal %q (description %q)", principal, sourcePrincipalDescription(principal))

	// 3. Concrete read-back, scoped to this run's unique resource names: exact
	// op sets on each resource, deterministically predicted by NormalizeForCC's
	// implication dedup (see doc comment above) — this is what actually proves
	// the "kafka-cluster:*" per-resource-type expansion AND the dedup rule both
	// ran correctly against a REAL IAM policy document, not a hermetic fixture.
	topicACLs := cc.aclsOnResource(t, topicName)
	require.Len(t, topicACLs, len(wantTopicOps), "topic %q: unexpected ACL count (implication dedup must drop Describe+DescribeConfigs)", topicName)
	require.ElementsMatch(t, wantTopicOps, opsOf(topicACLs))
	for _, a := range topicACLs {
		require.Equal(t, "*", a.Host, "host must normalize to *")
		require.Equal(t, "Literal", a.ResourcePatternType, "pattern type must be Literal (resource ARN names one topic)")
		require.Equal(t, "Allow", a.PermissionType, "permission must be Allow (IAM Effect was Allow)")
		require.NotEmpty(t, a.Principal, "read-back ACL must carry a resolved principal")
	}

	groupACLs := cc.aclsOnResource(t, groupName)
	require.Len(t, groupACLs, len(wantGroupOps), "group %q: unexpected ACL count (implication dedup must drop Describe)", groupName)
	require.ElementsMatch(t, wantGroupOps, opsOf(groupACLs))
	for _, a := range groupACLs {
		require.Equal(t, "*", a.Host, "host must normalize to *")
		require.Equal(t, "Literal", a.ResourcePatternType, "pattern type must be Literal (resource ARN names one group)")
		require.Equal(t, "Allow", a.PermissionType, "permission must be Allow (IAM Effect was Allow)")
		require.NotEmpty(t, a.Principal, "read-back ACL must carry a resolved principal")
	}

	// 4. Idempotent re-apply: proves the product recognises its own
	// previously-created (CC-numeric) ACLs on the IAM plane too; the test
	// itself never resolves numeric<->sa.
	out, err = runKCP(t, mf)
	require.NoError(t, err, out)
	require.Contains(t, out, "serviceAccounts: 0 created, 1 unchanged, 0 drift, 0 failed", out)
	require.Contains(t, out, "acls: 0 created, 8 unchanged, 0 drift, 0 failed", out)
}
