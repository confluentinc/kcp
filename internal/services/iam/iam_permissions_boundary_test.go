package iam

import (
	"context"
	"fmt"
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

// TestGetPrincipalPermissionsBoundaryDoc_RoleAndUserBranches locks in
// GetPrincipalPermissionsBoundaryDoc's dual role/user support: a role
// principal resolves its boundary via GetRole, and — the fix this test
// guards — a user principal resolves its boundary via GetUser instead of
// being silently skipped. Each branch asserts only its own AWS calls fire
// (the fake's unset methods for the other branch return "not expected"
// errors, so a misrouted call fails the test loudly).
func TestGetPrincipalPermissionsBoundaryDoc_RoleAndUserBranches(t *testing.T) {
	const boundaryArn = "arn:aws:iam::123456789012:policy/KafkaBoundary"
	const boundaryDocJSON = `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka-cluster:Connect","Resource":"*"}]}`

	wantDoc := boundaryDocJSON

	t.Run("role WITH boundary returns the decoded doc via GetRole", func(t *testing.T) {
		const roleArn = "arn:aws:iam::123456789012:role/kafka-migration-role"
		const roleName = "kafka-migration-role"

		getRoleCalls := 0
		fake := &fakeIAMAPI{
			getRoleFn: func(_ context.Context, params *iam.GetRoleInput) (*iam.GetRoleOutput, error) {
				getRoleCalls++
				if aws.ToString(params.RoleName) != roleName {
					return nil, fmt.Errorf("unexpected role name: %s", aws.ToString(params.RoleName))
				}
				return &iam.GetRoleOutput{
					Role: &iamtypes.Role{
						RoleName: aws.String(roleName),
						Arn:      aws.String(roleArn),
						PermissionsBoundary: &iamtypes.AttachedPermissionsBoundary{
							PermissionsBoundaryArn: aws.String(boundaryArn),
						},
					},
				}, nil
			},
			getPolicyFn: func(_ context.Context, params *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
				if aws.ToString(params.PolicyArn) != boundaryArn {
					return nil, fmt.Errorf("unexpected policy arn: %s", aws.ToString(params.PolicyArn))
				}
				return &iam.GetPolicyOutput{
					Policy: &iamtypes.Policy{
						Arn:              aws.String(boundaryArn),
						DefaultVersionId: aws.String("v1"),
					},
				}, nil
			},
			getPolicyVersionFn: func(_ context.Context, params *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
				if aws.ToString(params.PolicyArn) != boundaryArn || aws.ToString(params.VersionId) != "v1" {
					return nil, fmt.Errorf("unexpected policy version request: arn=%s version=%s", aws.ToString(params.PolicyArn), aws.ToString(params.VersionId))
				}
				return &iam.GetPolicyVersionOutput{
					PolicyVersion: &iamtypes.PolicyVersion{
						VersionId:        aws.String("v1"),
						IsDefaultVersion: true,
						Document:         encodePolicyDoc(boundaryDocJSON),
					},
				}, nil
			},
			// getUserFn deliberately left unset: if the role branch
			// misroutes to GetUser, the fake fails loudly.
		}

		doc, present, err := GetPrincipalPermissionsBoundaryDoc(context.Background(), fake, roleArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !present {
			t.Fatal("expected present=true")
		}
		if !reflect.DeepEqual(doc, wantDoc) {
			t.Fatalf("unexpected doc: %#v", doc)
		}
		if getRoleCalls != 1 {
			t.Fatalf("expected exactly 1 GetRole call, got %d", getRoleCalls)
		}
	})

	t.Run("user WITH boundary returns the decoded doc via GetUser, not GetRole", func(t *testing.T) {
		const userArn = "arn:aws:iam::123456789012:user/adrian-style"
		const userName = "adrian-style"

		getUserCalls := 0
		fake := &fakeIAMAPI{
			getUserFn: func(_ context.Context, params *iam.GetUserInput) (*iam.GetUserOutput, error) {
				getUserCalls++
				if aws.ToString(params.UserName) != userName {
					return nil, fmt.Errorf("unexpected user name: %s", aws.ToString(params.UserName))
				}
				return &iam.GetUserOutput{
					User: &iamtypes.User{
						UserName: aws.String(userName),
						Arn:      aws.String(userArn),
						PermissionsBoundary: &iamtypes.AttachedPermissionsBoundary{
							PermissionsBoundaryArn: aws.String(boundaryArn),
						},
					},
				}, nil
			},
			getPolicyFn: func(_ context.Context, params *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
				if aws.ToString(params.PolicyArn) != boundaryArn {
					return nil, fmt.Errorf("unexpected policy arn: %s", aws.ToString(params.PolicyArn))
				}
				return &iam.GetPolicyOutput{
					Policy: &iamtypes.Policy{
						Arn:              aws.String(boundaryArn),
						DefaultVersionId: aws.String("v1"),
					},
				}, nil
			},
			getPolicyVersionFn: func(_ context.Context, params *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
				if aws.ToString(params.PolicyArn) != boundaryArn || aws.ToString(params.VersionId) != "v1" {
					return nil, fmt.Errorf("unexpected policy version request: arn=%s version=%s", aws.ToString(params.PolicyArn), aws.ToString(params.VersionId))
				}
				return &iam.GetPolicyVersionOutput{
					PolicyVersion: &iamtypes.PolicyVersion{
						VersionId:        aws.String("v1"),
						IsDefaultVersion: true,
						Document:         encodePolicyDoc(boundaryDocJSON),
					},
				}, nil
			},
			// getRoleFn deliberately left unset: this is the fix under
			// test — a :user/ ARN must NOT fall through to GetRole (or to
			// no boundary resolution at all).
		}

		doc, present, err := GetPrincipalPermissionsBoundaryDoc(context.Background(), fake, userArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !present {
			t.Fatal("expected present=true")
		}
		if !reflect.DeepEqual(doc, wantDoc) {
			t.Fatalf("unexpected doc: %#v", doc)
		}
		if getUserCalls != 1 {
			t.Fatalf("expected exactly 1 GetUser call, got %d", getUserCalls)
		}
	})

	t.Run("role with NO boundary returns present=false and makes no GetPolicy call", func(t *testing.T) {
		const roleArn = "arn:aws:iam::123456789012:role/no-boundary-role"
		fake := &fakeIAMAPI{
			getRoleFn: func(_ context.Context, _ *iam.GetRoleInput) (*iam.GetRoleOutput, error) {
				return &iam.GetRoleOutput{Role: &iamtypes.Role{RoleName: aws.String("no-boundary-role")}}, nil
			},
			// getPolicyFn/getPolicyVersionFn deliberately unset: no
			// boundary means no ARN to resolve, so neither should be
			// called.
		}

		doc, present, err := GetPrincipalPermissionsBoundaryDoc(context.Background(), fake, roleArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if present {
			t.Fatalf("expected present=false, got doc=%q", doc)
		}
		if doc != "" {
			t.Fatalf("expected empty doc, got %q", doc)
		}
	})

	t.Run("user with NO boundary returns present=false and makes no GetPolicy call", func(t *testing.T) {
		const userArn = "arn:aws:iam::123456789012:user/no-boundary-user"
		fake := &fakeIAMAPI{
			getUserFn: func(_ context.Context, _ *iam.GetUserInput) (*iam.GetUserOutput, error) {
				return &iam.GetUserOutput{User: &iamtypes.User{UserName: aws.String("no-boundary-user")}}, nil
			},
		}

		doc, present, err := GetPrincipalPermissionsBoundaryDoc(context.Background(), fake, userArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if present {
			t.Fatalf("expected present=false, got doc=%q", doc)
		}
		if doc != "" {
			t.Fatalf("expected empty doc, got %q", doc)
		}
	})

	t.Run("unsupported/garbage ARN is a lenient skip, not an error", func(t *testing.T) {
		fake := &fakeIAMAPI{} // no methods should be called at all

		doc, present, err := GetPrincipalPermissionsBoundaryDoc(context.Background(), fake, "not-an-arn")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if present {
			t.Fatalf("expected present=false, got doc=%q", doc)
		}
		if doc != "" {
			t.Fatalf("expected empty doc, got %q", doc)
		}
	})

	t.Run("GetPolicyVersion error propagates as a wrapped error", func(t *testing.T) {
		const roleArn = "arn:aws:iam::123456789012:role/broken-role"
		fake := &fakeIAMAPI{
			getRoleFn: func(_ context.Context, _ *iam.GetRoleInput) (*iam.GetRoleOutput, error) {
				return &iam.GetRoleOutput{
					Role: &iamtypes.Role{
						RoleName: aws.String("broken-role"),
						PermissionsBoundary: &iamtypes.AttachedPermissionsBoundary{
							PermissionsBoundaryArn: aws.String(boundaryArn),
						},
					},
				}, nil
			},
			getPolicyFn: func(_ context.Context, _ *iam.GetPolicyInput) (*iam.GetPolicyOutput, error) {
				return &iam.GetPolicyOutput{
					Policy: &iamtypes.Policy{Arn: aws.String(boundaryArn), DefaultVersionId: aws.String("v1")},
				}, nil
			},
			getPolicyVersionFn: func(_ context.Context, _ *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error) {
				return nil, fmt.Errorf("boom: simulated AWS failure")
			},
		}

		_, _, err := GetPrincipalPermissionsBoundaryDoc(context.Background(), fake, roleArn)
		if err == nil {
			t.Fatal("expected error, got nil")
		}
	})
}
