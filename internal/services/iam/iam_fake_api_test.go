package iam

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/service/iam"
)

// fakeIAMAPI is a hermetic test double for iamAPI. Every method is backed by
// an optional func field; a test wires up only the methods it expects to be
// called. Any method left nil returns a "not expected" error, so a test
// fails loudly (rather than silently passing) if the code under test takes
// an unanticipated path — e.g. calling the role-branch methods for a user
// principal, or vice versa.
type fakeIAMAPI struct {
	getAccountAuthorizationDetailsFn func(ctx context.Context, params *iam.GetAccountAuthorizationDetailsInput) (*iam.GetAccountAuthorizationDetailsOutput, error)
	listRolePoliciesFn               func(ctx context.Context, params *iam.ListRolePoliciesInput) (*iam.ListRolePoliciesOutput, error)
	getRolePolicyFn                  func(ctx context.Context, params *iam.GetRolePolicyInput) (*iam.GetRolePolicyOutput, error)
	listAttachedRolePoliciesFn       func(ctx context.Context, params *iam.ListAttachedRolePoliciesInput) (*iam.ListAttachedRolePoliciesOutput, error)
	listUserPoliciesFn               func(ctx context.Context, params *iam.ListUserPoliciesInput) (*iam.ListUserPoliciesOutput, error)
	getUserPolicyFn                  func(ctx context.Context, params *iam.GetUserPolicyInput) (*iam.GetUserPolicyOutput, error)
	listAttachedUserPoliciesFn       func(ctx context.Context, params *iam.ListAttachedUserPoliciesInput) (*iam.ListAttachedUserPoliciesOutput, error)
	getPolicyFn                      func(ctx context.Context, params *iam.GetPolicyInput) (*iam.GetPolicyOutput, error)
	getPolicyVersionFn               func(ctx context.Context, params *iam.GetPolicyVersionInput) (*iam.GetPolicyVersionOutput, error)
	getRoleFn                        func(ctx context.Context, params *iam.GetRoleInput) (*iam.GetRoleOutput, error)
	getUserFn                        func(ctx context.Context, params *iam.GetUserInput) (*iam.GetUserOutput, error)
	simulatePrincipalPolicyFn        func(ctx context.Context, params *iam.SimulatePrincipalPolicyInput) (*iam.SimulatePrincipalPolicyOutput, error)
}

var _ iamAPI = (*fakeIAMAPI)(nil)

func (f *fakeIAMAPI) GetAccountAuthorizationDetails(ctx context.Context, params *iam.GetAccountAuthorizationDetailsInput, _ ...func(*iam.Options)) (*iam.GetAccountAuthorizationDetailsOutput, error) {
	if f.getAccountAuthorizationDetailsFn == nil {
		return nil, fmt.Errorf("not expected: GetAccountAuthorizationDetails call")
	}
	return f.getAccountAuthorizationDetailsFn(ctx, params)
}

func (f *fakeIAMAPI) ListRolePolicies(ctx context.Context, params *iam.ListRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListRolePoliciesOutput, error) {
	if f.listRolePoliciesFn == nil {
		return nil, fmt.Errorf("not expected: ListRolePolicies call")
	}
	return f.listRolePoliciesFn(ctx, params)
}

func (f *fakeIAMAPI) GetRolePolicy(ctx context.Context, params *iam.GetRolePolicyInput, _ ...func(*iam.Options)) (*iam.GetRolePolicyOutput, error) {
	if f.getRolePolicyFn == nil {
		return nil, fmt.Errorf("not expected: GetRolePolicy call")
	}
	return f.getRolePolicyFn(ctx, params)
}

func (f *fakeIAMAPI) ListAttachedRolePolicies(ctx context.Context, params *iam.ListAttachedRolePoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedRolePoliciesOutput, error) {
	if f.listAttachedRolePoliciesFn == nil {
		return nil, fmt.Errorf("not expected: ListAttachedRolePolicies call")
	}
	return f.listAttachedRolePoliciesFn(ctx, params)
}

func (f *fakeIAMAPI) ListUserPolicies(ctx context.Context, params *iam.ListUserPoliciesInput, _ ...func(*iam.Options)) (*iam.ListUserPoliciesOutput, error) {
	if f.listUserPoliciesFn == nil {
		return nil, fmt.Errorf("not expected: ListUserPolicies call")
	}
	return f.listUserPoliciesFn(ctx, params)
}

func (f *fakeIAMAPI) GetUserPolicy(ctx context.Context, params *iam.GetUserPolicyInput, _ ...func(*iam.Options)) (*iam.GetUserPolicyOutput, error) {
	if f.getUserPolicyFn == nil {
		return nil, fmt.Errorf("not expected: GetUserPolicy call")
	}
	return f.getUserPolicyFn(ctx, params)
}

func (f *fakeIAMAPI) ListAttachedUserPolicies(ctx context.Context, params *iam.ListAttachedUserPoliciesInput, _ ...func(*iam.Options)) (*iam.ListAttachedUserPoliciesOutput, error) {
	if f.listAttachedUserPoliciesFn == nil {
		return nil, fmt.Errorf("not expected: ListAttachedUserPolicies call")
	}
	return f.listAttachedUserPoliciesFn(ctx, params)
}

func (f *fakeIAMAPI) GetPolicy(ctx context.Context, params *iam.GetPolicyInput, _ ...func(*iam.Options)) (*iam.GetPolicyOutput, error) {
	if f.getPolicyFn == nil {
		return nil, fmt.Errorf("not expected: GetPolicy call")
	}
	return f.getPolicyFn(ctx, params)
}

func (f *fakeIAMAPI) GetPolicyVersion(ctx context.Context, params *iam.GetPolicyVersionInput, _ ...func(*iam.Options)) (*iam.GetPolicyVersionOutput, error) {
	if f.getPolicyVersionFn == nil {
		return nil, fmt.Errorf("not expected: GetPolicyVersion call")
	}
	return f.getPolicyVersionFn(ctx, params)
}

func (f *fakeIAMAPI) GetRole(ctx context.Context, params *iam.GetRoleInput, _ ...func(*iam.Options)) (*iam.GetRoleOutput, error) {
	if f.getRoleFn == nil {
		return nil, fmt.Errorf("not expected: GetRole call")
	}
	return f.getRoleFn(ctx, params)
}

func (f *fakeIAMAPI) GetUser(ctx context.Context, params *iam.GetUserInput, _ ...func(*iam.Options)) (*iam.GetUserOutput, error) {
	if f.getUserFn == nil {
		return nil, fmt.Errorf("not expected: GetUser call")
	}
	return f.getUserFn(ctx, params)
}

func (f *fakeIAMAPI) SimulatePrincipalPolicy(ctx context.Context, params *iam.SimulatePrincipalPolicyInput, _ ...func(*iam.Options)) (*iam.SimulatePrincipalPolicyOutput, error) {
	if f.simulatePrincipalPolicyFn == nil {
		return nil, fmt.Errorf("not expected: SimulatePrincipalPolicy call")
	}
	return f.simulatePrincipalPolicyFn(ctx, params)
}
