package iam

import (
	"context"
	"fmt"
	"net/url"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// filterIncludesManagedPolicyTypes models the real AWS
// GetAccountAuthorizationDetails behaviour this test relies on: the
// response's top-level Policies list is populated with managed-policy
// documents only when the request Filter asks for them (LocalManagedPolicy
// and/or AWSManagedPolicy). A Filter of just [Role] — the pre-c7b55bbf bug —
// comes back with an empty Policies list, per the AWS API docs for
// GetAccountAuthorizationDetails.
func filterIncludesManagedPolicyTypes(filter []iamtypes.EntityType) bool {
	for _, f := range filter {
		if f == iamtypes.EntityTypeLocalManagedPolicy || f == iamtypes.EntityTypeAWSManagedPolicy {
			return true
		}
	}
	return false
}

func encodePolicyDoc(jsonDoc string) *string {
	return aws.String(url.QueryEscape(jsonDoc))
}

// TestGetAllRolePolicies_ManagedPolicyFilterRegression is a regression test
// for c7b55bbf: GetAllRolePolicies must request LocalManagedPolicy and
// AWSManagedPolicy (not just Role) in its GetAccountAuthorizationDetails
// Filter, or every attached managed policy fails the ARN join and is
// silently dropped. The fake models AWS's real Filter-driven behaviour
// (filterIncludesManagedPolicyTypes above), so this test only passes because
// production sends the right Filter AND performs the join correctly —
// regressing either one makes it fail.
func TestGetAllRolePolicies_ManagedPolicyFilterRegression(t *testing.T) {
	t.Run("attached-only role: managed grant resolved only when Filter requests managed policies", func(t *testing.T) {
		const roleArn = "arn:aws:iam::123456789012:role/kafka-migration-role"
		const policyArn = "arn:aws:iam::123456789012:policy/KafkaClusterAccess"
		const docJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:*","Resource":"*"}]}`
		wantDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:*", "Resource": "*"},
			},
		}

		callCount := 0
		fake := &fakeIAMAPI{
			getAccountAuthorizationDetailsFn: func(_ context.Context, params *iam.GetAccountAuthorizationDetailsInput) (*iam.GetAccountAuthorizationDetailsOutput, error) {
				callCount++
				output := &iam.GetAccountAuthorizationDetailsOutput{
					RoleDetailList: []iamtypes.RoleDetail{
						{
							Arn:      aws.String(roleArn),
							RoleName: aws.String("kafka-migration-role"),
							AttachedManagedPolicies: []iamtypes.AttachedPolicy{
								{PolicyArn: aws.String(policyArn), PolicyName: aws.String("KafkaClusterAccess")},
							},
						},
					},
				}
				// This is the crux of the regression test: AWS only returns
				// the managed-policy document in Policies when the caller's
				// Filter asked for it.
				if filterIncludesManagedPolicyTypes(params.Filter) {
					output.Policies = []iamtypes.ManagedPolicyDetail{
						{
							Arn:              aws.String(policyArn),
							DefaultVersionId: aws.String("v1"),
							PolicyVersionList: []iamtypes.PolicyVersion{
								{VersionId: aws.String("v1"), IsDefaultVersion: true, Document: encodePolicyDoc(docJSON)},
							},
						},
					}
				}
				return output, nil
			},
		}

		got, err := GetAllRolePolicies(context.Background(), fake)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if callCount != 1 {
			t.Fatalf("expected exactly 1 page fetched, got %d", callCount)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 principal, got %d: %#v", len(got), got)
		}

		principal := got[0]
		if len(principal.InlinePolicies) != 0 {
			t.Fatalf("expected no inline policies, got %#v", principal.InlinePolicies)
		}
		if len(principal.AttachedPolicies) != 1 {
			t.Fatalf("expected 1 attached policy (the managed grant resolved), got %d: %#v", len(principal.AttachedPolicies), principal.AttachedPolicies)
		}

		attached := principal.AttachedPolicies[0]
		if attached.PolicyName != "KafkaClusterAccess" {
			t.Fatalf("unexpected attached policy name: %s", attached.PolicyName)
		}
		if attached.PolicyArn != policyArn {
			t.Fatalf("unexpected attached policy arn: %s", attached.PolicyArn)
		}
		if !reflect.DeepEqual(attached.PolicyDocument, wantDoc) {
			t.Fatalf("unexpected attached policy document: %#v", attached.PolicyDocument)
		}
	})

	t.Run("role with both inline and attached-managed policies, resolved across a 2-page response", func(t *testing.T) {
		const roleArn = "arn:aws:iam::123456789012:role/kafka-connect-role"
		const policyArn = "arn:aws:iam::123456789012:policy/KafkaClusterAccess2"
		const inlineDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:AlterCluster","Resource":"*"}]}`
		const managedDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:*","Resource":"*"}]}`

		wantInlineDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:AlterCluster", "Resource": "*"},
			},
		}
		wantManagedDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:*", "Resource": "*"},
			},
		}

		callCount := 0
		fake := &fakeIAMAPI{
			getAccountAuthorizationDetailsFn: func(_ context.Context, params *iam.GetAccountAuthorizationDetailsInput) (*iam.GetAccountAuthorizationDetailsOutput, error) {
				callCount++
				switch callCount {
				case 1:
					if params.Marker != nil {
						t.Fatalf("expected nil marker on first page request, got %q", aws.ToString(params.Marker))
					}
					// Page 1: the role (inline + a reference to the attached
					// managed policy) but NOT yet the managed policy's own
					// document — that arrives on page 2. This forces
					// GetAllRolePolicies to accumulate across both pages
					// before joining, rather than joining per-page.
					return &iam.GetAccountAuthorizationDetailsOutput{
						RoleDetailList: []iamtypes.RoleDetail{
							{
								Arn:      aws.String(roleArn),
								RoleName: aws.String("kafka-connect-role"),
								RolePolicyList: []iamtypes.PolicyDetail{
									{PolicyName: aws.String("KafkaConnectInline"), PolicyDocument: encodePolicyDoc(inlineDocJSON)},
								},
								AttachedManagedPolicies: []iamtypes.AttachedPolicy{
									{PolicyArn: aws.String(policyArn), PolicyName: aws.String("KafkaClusterAccess2")},
								},
							},
						},
						IsTruncated: true,
						Marker:      aws.String("page-2-marker"),
					}, nil
				case 2:
					if aws.ToString(params.Marker) != "page-2-marker" {
						t.Fatalf("expected marker %q on second page request, got %q", "page-2-marker", aws.ToString(params.Marker))
					}
					output := &iam.GetAccountAuthorizationDetailsOutput{
						IsTruncated: false,
					}
					if filterIncludesManagedPolicyTypes(params.Filter) {
						output.Policies = []iamtypes.ManagedPolicyDetail{
							{
								Arn:              aws.String(policyArn),
								DefaultVersionId: aws.String("v1"),
								PolicyVersionList: []iamtypes.PolicyVersion{
									{VersionId: aws.String("v1"), IsDefaultVersion: true, Document: encodePolicyDoc(managedDocJSON)},
								},
							},
						}
					}
					return output, nil
				default:
					return nil, fmt.Errorf("unexpected page request #%d — the maxPages guard or pagination loop is misbehaving", callCount)
				}
			},
		}

		got, err := GetAllRolePolicies(context.Background(), fake)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		// Exactly 2 pages were needed and no more — proves the loop
		// terminated on IsTruncated=false rather than tripping the maxPages
		// guard (which would require exhausting 10000 pages, not 2).
		if callCount != 2 {
			t.Fatalf("expected exactly 2 pages fetched, got %d", callCount)
		}
		if len(got) != 1 {
			t.Fatalf("expected 1 principal, got %d: %#v", len(got), got)
		}

		principal := got[0]
		if len(principal.InlinePolicies) != 1 {
			t.Fatalf("expected 1 inline policy, got %d: %#v", len(principal.InlinePolicies), principal.InlinePolicies)
		}
		if principal.InlinePolicies[0].PolicyName != "KafkaConnectInline" {
			t.Fatalf("unexpected inline policy name: %s", principal.InlinePolicies[0].PolicyName)
		}
		if !reflect.DeepEqual(principal.InlinePolicies[0].PolicyDocument, wantInlineDoc) {
			t.Fatalf("unexpected inline policy document: %#v", principal.InlinePolicies[0].PolicyDocument)
		}

		if len(principal.AttachedPolicies) != 1 {
			t.Fatalf("expected 1 attached policy (managed grant resolved across pages), got %d: %#v", len(principal.AttachedPolicies), principal.AttachedPolicies)
		}
		attached := principal.AttachedPolicies[0]
		if attached.PolicyName != "KafkaClusterAccess2" {
			t.Fatalf("unexpected attached policy name: %s", attached.PolicyName)
		}
		if !reflect.DeepEqual(attached.PolicyDocument, wantManagedDoc) {
			t.Fatalf("unexpected attached policy document: %#v", attached.PolicyDocument)
		}
	})
}

// TestGetPrincipalPolicies_UserAndRoleBranches locks in GetPrincipalPolicies'
// user-branch (arn:...:user/...) support alongside its pre-existing
// role-branch support, asserting each ARN type drives only its own set of
// AWS calls (the fake's unset methods for the other branch return "not
// expected" errors, so a misrouted call fails the test loudly) and that the
// resolved inline + attached policies come back correctly.
func TestGetPrincipalPolicies_UserAndRoleBranches(t *testing.T) {
	t.Run("user principal (:user/) uses the user-branch methods only", func(t *testing.T) {
		const userArn = "arn:aws:iam::123456789012:user/adrian-style"
		const userName = "adrian-style"
		const attachedArn = "arn:aws:iam::123456789012:policy/AdrianAttached"
		const inlineDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:Connect","Resource":"*"}]}`
		const attachedDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:DescribeCluster","Resource":"*"}]}`

		wantInlineDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:Connect", "Resource": "*"},
			},
		}
		wantAttachedDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:DescribeCluster", "Resource": "*"},
			},
		}

		fake := &fakeIAMAPI{
			listUserPoliciesFn: func(_ context.Context, params *iam.ListUserPoliciesInput) (*iam.ListUserPoliciesOutput, error) {
				if aws.ToString(params.UserName) != userName {
					return nil, fmt.Errorf("unexpected user name: %s", aws.ToString(params.UserName))
				}
				return &iam.ListUserPoliciesOutput{PolicyNames: []string{"AdrianInline"}}, nil
			},
			getUserPolicyFn: func(_ context.Context, params *iam.GetUserPolicyInput) (*iam.GetUserPolicyOutput, error) {
				if aws.ToString(params.PolicyName) != "AdrianInline" {
					return nil, fmt.Errorf("unexpected policy name: %s", aws.ToString(params.PolicyName))
				}
				return &iam.GetUserPolicyOutput{
					PolicyName:     aws.String("AdrianInline"),
					UserName:       aws.String(userName),
					PolicyDocument: encodePolicyDoc(inlineDocJSON),
				}, nil
			},
			listAttachedUserPoliciesFn: func(_ context.Context, params *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error) {
				if aws.ToString(params.UserName) != userName {
					return nil, fmt.Errorf("unexpected user name: %s", aws.ToString(params.UserName))
				}
				return &iam.ListAttachedUserPoliciesOutput{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyArn: aws.String(attachedArn), PolicyName: aws.String("AdrianAttached")},
					},
				}, nil
			},
			getPolicyFn: func(_ context.Context, params *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
				if aws.ToString(params.PolicyArn) != attachedArn {
					return nil, fmt.Errorf("unexpected policy arn: %s", aws.ToString(params.PolicyArn))
				}
				return &iam.GetPolicyOutput{
					Policy: &iamtypes.Policy{
						Arn:              aws.String(attachedArn),
						PolicyName:       aws.String("AdrianAttached"),
						DefaultVersionId: aws.String("v1"),
						Description:      aws.String("adrian's attached policy"),
					},
				}, nil
			},
			getPolicyVersionFn: func(_ context.Context, params *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
				if aws.ToString(params.PolicyArn) != attachedArn || aws.ToString(params.VersionId) != "v1" {
					return nil, fmt.Errorf("unexpected policy version request: arn=%s version=%s", aws.ToString(params.PolicyArn), aws.ToString(params.VersionId))
				}
				return &iam.GetPolicyVersionOutput{
					PolicyVersion: &iamtypes.PolicyVersion{
						VersionId:        aws.String("v1"),
						IsDefaultVersion: true,
						Document:         encodePolicyDoc(attachedDocJSON),
					},
				}, nil
			},
			// Role-branch methods are deliberately left unset: if
			// GetPrincipalPolicies misroutes a :user/ ARN to the role
			// branch, the fake fails the call loudly instead of the test
			// silently passing.
		}

		got, err := GetPrincipalPolicies(context.Background(), fake, userArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.PrincipalType != "user" {
			t.Fatalf("expected principal type 'user', got %q", got.PrincipalType)
		}
		if got.PrincipalName != userName {
			t.Fatalf("unexpected principal name: %s", got.PrincipalName)
		}

		if len(got.InlinePolicies) != 1 || got.InlinePolicies[0].PolicyName != "AdrianInline" {
			t.Fatalf("unexpected inline policies: %#v", got.InlinePolicies)
		}
		if !reflect.DeepEqual(got.InlinePolicies[0].PolicyDocument, wantInlineDoc) {
			t.Fatalf("unexpected inline policy document: %#v", got.InlinePolicies[0].PolicyDocument)
		}

		if len(got.AttachedPolicies) != 1 || got.AttachedPolicies[0].PolicyName != "AdrianAttached" {
			t.Fatalf("unexpected attached policies: %#v", got.AttachedPolicies)
		}
		if got.AttachedPolicies[0].Description != "adrian's attached policy" {
			t.Fatalf("unexpected attached policy description: %s", got.AttachedPolicies[0].Description)
		}
		if !reflect.DeepEqual(got.AttachedPolicies[0].PolicyDocument, wantAttachedDoc) {
			t.Fatalf("unexpected attached policy document: %#v", got.AttachedPolicies[0].PolicyDocument)
		}
	})

	t.Run("role principal (:role/) uses the role-branch methods only", func(t *testing.T) {
		const roleArn = "arn:aws:iam::123456789012:role/kafka-migration-role"
		const roleName = "kafka-migration-role"
		const attachedArn = "arn:aws:iam::123456789012:policy/RoleAttached"
		const inlineDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:WriteData","Resource":"*"}]}`
		const attachedDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:ReadData","Resource":"*"}]}`

		wantInlineDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:WriteData", "Resource": "*"},
			},
		}
		wantAttachedDoc := map[string]any{
			"Version": "2012-10-17",
			"Statement": []any{
				map[string]any{"Effect": "Allow", "Action": "kafka-cluster:ReadData", "Resource": "*"},
			},
		}

		fake := &fakeIAMAPI{
			listRolePoliciesFn: func(_ context.Context, params *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error) {
				if aws.ToString(params.RoleName) != roleName {
					return nil, fmt.Errorf("unexpected role name: %s", aws.ToString(params.RoleName))
				}
				return &iam.ListRolePoliciesOutput{PolicyNames: []string{"RoleInline"}}, nil
			},
			getRolePolicyFn: func(_ context.Context, params *iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error) {
				if aws.ToString(params.PolicyName) != "RoleInline" {
					return nil, fmt.Errorf("unexpected policy name: %s", aws.ToString(params.PolicyName))
				}
				return &iam.GetRolePolicyOutput{
					PolicyName:     aws.String("RoleInline"),
					RoleName:       aws.String(roleName),
					PolicyDocument: encodePolicyDoc(inlineDocJSON),
				}, nil
			},
			listAttachedRolePoliciesFn: func(_ context.Context, params *iam.ListAttachedRolePoliciesInput) (*iam.ListAttachedRolePoliciesOutput, error) {
				if aws.ToString(params.RoleName) != roleName {
					return nil, fmt.Errorf("unexpected role name: %s", aws.ToString(params.RoleName))
				}
				return &iam.ListAttachedRolePoliciesOutput{
					AttachedPolicies: []iamtypes.AttachedPolicy{
						{PolicyArn: aws.String(attachedArn), PolicyName: aws.String("RoleAttached")},
					},
				}, nil
			},
			getPolicyFn: func(_ context.Context, params *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
				if aws.ToString(params.PolicyArn) != attachedArn {
					return nil, fmt.Errorf("unexpected policy arn: %s", aws.ToString(params.PolicyArn))
				}
				return &iam.GetPolicyOutput{
					Policy: &iamtypes.Policy{
						Arn:              aws.String(attachedArn),
						PolicyName:       aws.String("RoleAttached"),
						DefaultVersionId: aws.String("v1"),
						Description:      aws.String("role's attached policy"),
					},
				}, nil
			},
			getPolicyVersionFn: func(_ context.Context, params *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
				if aws.ToString(params.PolicyArn) != attachedArn || aws.ToString(params.VersionId) != "v1" {
					return nil, fmt.Errorf("unexpected policy version request: arn=%s version=%s", aws.ToString(params.PolicyArn), aws.ToString(params.VersionId))
				}
				return &iam.GetPolicyVersionOutput{
					PolicyVersion: &iamtypes.PolicyVersion{
						VersionId:        aws.String("v1"),
						IsDefaultVersion: true,
						Document:         encodePolicyDoc(attachedDocJSON),
					},
				}, nil
			},
			// User-branch methods deliberately left unset: if
			// GetPrincipalPolicies misroutes a :role/ ARN to the user
			// branch, the fake fails the call loudly instead of the test
			// silently passing.
		}

		got, err := GetPrincipalPolicies(context.Background(), fake, roleArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got.PrincipalType != "role" {
			t.Fatalf("expected principal type 'role', got %q", got.PrincipalType)
		}
		if got.PrincipalName != roleName {
			t.Fatalf("unexpected principal name: %s", got.PrincipalName)
		}

		if len(got.InlinePolicies) != 1 || got.InlinePolicies[0].PolicyName != "RoleInline" {
			t.Fatalf("unexpected inline policies: %#v", got.InlinePolicies)
		}
		if !reflect.DeepEqual(got.InlinePolicies[0].PolicyDocument, wantInlineDoc) {
			t.Fatalf("unexpected inline policy document: %#v", got.InlinePolicies[0].PolicyDocument)
		}

		if len(got.AttachedPolicies) != 1 || got.AttachedPolicies[0].PolicyName != "RoleAttached" {
			t.Fatalf("unexpected attached policies: %#v", got.AttachedPolicies)
		}
		if got.AttachedPolicies[0].Description != "role's attached policy" {
			t.Fatalf("unexpected attached policy description: %s", got.AttachedPolicies[0].Description)
		}
		if !reflect.DeepEqual(got.AttachedPolicies[0].PolicyDocument, wantAttachedDoc) {
			t.Fatalf("unexpected attached policy document: %#v", got.AttachedPolicies[0].PolicyDocument)
		}
	})
}
