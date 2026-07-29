package iam

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/url"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

type RolePolicies struct {
	RoleName         string           `json:"role_name"`
	RoleArn          string           `json:"role_arn"`
	InlinePolicies   []InlinePolicy   `json:"inline_policies"`
	AttachedPolicies []AttachedPolicy `json:"attached_policies"`
}

type PrincipalPolicies struct {
	PrincipalName    string           `json:"principal_name"`
	PrincipalArn     string           `json:"principal_arn"`
	PrincipalType    string           `json:"principal_type"` // "role" or "user"
	InlinePolicies   []InlinePolicy   `json:"inline_policies"`
	AttachedPolicies []AttachedPolicy `json:"attached_policies"`
}

type InlinePolicy struct {
	PolicyName     string         `json:"policy_name"`
	PolicyDocument map[string]any `json:"policy_document"`
}

type AttachedPolicy struct {
	PolicyName     string         `json:"policy_name"`
	PolicyArn      string         `json:"policy_arn"`
	PolicyDocument map[string]any `json:"policy_document"`
	Description    string         `json:"description,omitempty"`
}

func GetRolePolicies(ctx context.Context, iamClient *iam.Client, roleArn string) (*RolePolicies, error) {
	roleName, err := extractRoleNameFromArn(roleArn)
	if err != nil {
		return nil, fmt.Errorf("failed to extract role name from ARN: %v", err)
	}

	result := &RolePolicies{
		RoleName:         roleName,
		RoleArn:          roleArn,
		InlinePolicies:   []InlinePolicy{},
		AttachedPolicies: []AttachedPolicy{},
	}

	inlinePolicies, err := getInlinePolicies(ctx, iamClient, roleName)
	if err != nil {
		return nil, fmt.Errorf("failed to get inline policies: %v", err)
	}
	result.InlinePolicies = inlinePolicies

	attachedPolicies, err := getAttachedPolicies(ctx, iamClient, roleName)
	if err != nil {
		return nil, fmt.Errorf("failed to get attached policies: %v", err)
	}
	result.AttachedPolicies = attachedPolicies

	return result, nil
}

func GetPrincipalPolicies(ctx context.Context, iamClient *iam.Client, principalArn string) (*PrincipalPolicies, error) {
	principalName, principalType, err := extractPrincipalFromArn(principalArn)
	if err != nil {
		return nil, fmt.Errorf("failed to extract principal from ARN: %v", err)
	}

	result := &PrincipalPolicies{
		PrincipalName:    principalName,
		PrincipalArn:     principalArn,
		PrincipalType:    principalType,
		InlinePolicies:   []InlinePolicy{},
		AttachedPolicies: []AttachedPolicy{},
	}

	var inlinePolicies []InlinePolicy
	var attachedPolicies []AttachedPolicy

	switch principalType {
	case "role":
		inlinePolicies, err = getInlinePolicies(ctx, iamClient, principalName)
		if err != nil {
			return nil, fmt.Errorf("failed to get inline policies: %v", err)
		}

		attachedPolicies, err = getAttachedPolicies(ctx, iamClient, principalName)
		if err != nil {
			return nil, fmt.Errorf("failed to get attached policies: %v", err)
		}
	case "user":
		inlinePolicies, err = getUserInlinePolicies(ctx, iamClient, principalName)
		if err != nil {
			return nil, fmt.Errorf("failed to get user inline policies: %v", err)
		}

		attachedPolicies, err = getUserAttachedPolicies(ctx, iamClient, principalName)
		if err != nil {
			return nil, fmt.Errorf("failed to get user attached policies: %v", err)
		}
	}

	result.InlinePolicies = inlinePolicies
	result.AttachedPolicies = attachedPolicies

	return result, nil
}

// GetAllRolePolicies enumerates every IAM role in the account via
// GetAccountAuthorizationDetails (filtered to roles), paginating on
// IsTruncated/Marker, and returns one PrincipalPolicies per role with its
// inline policies decoded and its attached managed policies resolved to
// their default-version document.
//
// This is a thin AWS wrapper: it does no exclusion/scoping (that lives in
// the migrate layer). A role whose attached managed policy cannot be
// resolved (missing from the account's Policies list, or no default
// version present) is logged and skipped rather than failing the sweep,
// since a single unresolvable policy should not block enumeration of every
// other role.
func GetAllRolePolicies(ctx context.Context, iamClient *iam.Client) ([]PrincipalPolicies, error) {
	var roleDetails []iamtypes.RoleDetail
	var managedPolicies []iamtypes.ManagedPolicyDetail

	// maxPages bounds the loop against a misbehaving server that keeps
	// returning IsTruncated=true with a nil/unchanged Marker, rather than
	// spinning forever. Mirrors the same guard in
	// internal/migrate/serviceaccounts/ccclient.go (NumericToResourceID).
	const maxPages = 10000
	var marker *string
	for page := 0; ; page++ {
		if page >= maxPages {
			return nil, fmt.Errorf("GetAccountAuthorizationDetails pagination exceeded %d pages", maxPages)
		}
		output, err := iamClient.GetAccountAuthorizationDetails(ctx, &iam.GetAccountAuthorizationDetailsInput{
			Filter: []iamtypes.EntityType{iamtypes.EntityTypeRole},
			Marker: marker,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get account authorization details: %v", err)
		}

		roleDetails = append(roleDetails, output.RoleDetailList...)
		managedPolicies = append(managedPolicies, output.Policies...)

		if !output.IsTruncated {
			break
		}
		marker = output.Marker
	}

	principalPolicies := make([]PrincipalPolicies, 0, len(roleDetails))
	for _, roleDetail := range roleDetails {
		principalPolicies = append(principalPolicies, buildRolePrincipalPolicies(roleDetail, managedPolicies))
	}

	return principalPolicies, nil
}

// buildRolePrincipalPolicies converts a single RoleDetail (plus the account-wide
// managed policy list it must be joined against) into a PrincipalPolicies value.
func buildRolePrincipalPolicies(roleDetail iamtypes.RoleDetail, managedPolicies []iamtypes.ManagedPolicyDetail) PrincipalPolicies {
	roleArn := aws.ToString(roleDetail.Arn)

	inlinePolicies := make([]InlinePolicy, 0, len(roleDetail.RolePolicyList))
	for _, pd := range roleDetail.RolePolicyList {
		policyName := aws.ToString(pd.PolicyName)
		policyDocument, err := parsePolicyDocument(aws.ToString(pd.PolicyDocument))
		if err != nil {
			// Deliberately skip-and-log rather than hard-fail (unlike explicit-mode
			// getInlinePolicies below): this runs across an entire account sweep, so
			// one unparsable policy on one role must not abort enumeration of every
			// other role.
			slog.Warn("⚠️ skipping unparsable inline role policy", "role_arn", roleArn, "policy_name", policyName, "error", err)
			continue
		}
		inlinePolicies = append(inlinePolicies, InlinePolicy{
			PolicyName:     policyName,
			PolicyDocument: policyDocument,
		})
	}

	attachedPolicies := make([]AttachedPolicy, 0, len(roleDetail.AttachedManagedPolicies))
	for _, summary := range roleDetail.AttachedManagedPolicies {
		policyArn := aws.ToString(summary.PolicyArn)
		policyDocument, err := resolveManagedPolicyDoc(managedPolicies, policyArn)
		if err != nil {
			slog.Warn("⚠️ skipping unresolvable attached managed policy", "role_arn", roleArn, "policy_arn", policyArn, "error", err)
			continue
		}
		attachedPolicies = append(attachedPolicies, AttachedPolicy{
			PolicyName:     aws.ToString(summary.PolicyName),
			PolicyArn:      policyArn,
			PolicyDocument: policyDocument,
		})
	}

	return PrincipalPolicies{
		PrincipalName:    aws.ToString(roleDetail.RoleName),
		PrincipalArn:     roleArn,
		PrincipalType:    "role",
		InlinePolicies:   inlinePolicies,
		AttachedPolicies: attachedPolicies,
	}
}

// resolveManagedPolicyDoc finds the ManagedPolicyDetail matching arn in policies,
// selects its default version (IsDefaultVersion, falling back to a VersionId match
// against DefaultVersionId), and decodes that version's document. It is a pure
// helper (no AWS calls) so the join + default-version-selection logic can be unit
// tested without a fake IAM client.
func resolveManagedPolicyDoc(policies []iamtypes.ManagedPolicyDetail, arn string) (map[string]any, error) {
	for _, policy := range policies {
		if aws.ToString(policy.Arn) != arn {
			continue
		}

		defaultVersionID := aws.ToString(policy.DefaultVersionId)
		for _, version := range policy.PolicyVersionList {
			if version.IsDefaultVersion || (defaultVersionID != "" && aws.ToString(version.VersionId) == defaultVersionID) {
				return parsePolicyDocument(aws.ToString(version.Document))
			}
		}

		return nil, fmt.Errorf("managed policy %s has no default version", arn)
	}

	return nil, fmt.Errorf("managed policy %s not found in account authorization details", arn)
}

// ARN format: arn:aws:iam::123456789012:role/RoleName
func extractRoleNameFromArn(roleArn string) (string, error) {
	parts := strings.Split(roleArn, "/")
	if len(parts) < 2 {
		return "", fmt.Errorf("invalid role ARN format: %s", roleArn)
	}
	return parts[len(parts)-1], nil
}

// Extract principal name and type from ARN
// Role ARN format: arn:aws:iam::123456789012:role/RoleName
// User ARN format: arn:aws:iam::123456789012:user/UserName
func extractPrincipalFromArn(principalArn string) (string, string, error) {
	parts := strings.Split(principalArn, "/")
	if len(parts) < 2 {
		return "", "", fmt.Errorf("invalid principal ARN format: %s", principalArn)
	}

	// Check if it's a role or user ARN
	if strings.Contains(principalArn, ":role/") {
		return parts[len(parts)-1], "role", nil
	} else if strings.Contains(principalArn, ":user/") {
		return parts[len(parts)-1], "user", nil
	}

	return "", "", fmt.Errorf("unsupported principal type in ARN: %s (must be role or user)", principalArn)
}

func getInlinePolicies(ctx context.Context, iamClient *iam.Client, roleName string) ([]InlinePolicy, error) {
	var inlinePolicies []InlinePolicy

	listInput := &iam.ListRolePoliciesInput{
		RoleName: aws.String(roleName),
	}

	listOutput, err := iamClient.ListRolePolicies(ctx, listInput)
	if err != nil {
		return nil, fmt.Errorf("failed to list inline policies: %v", err)
	}

	for _, policyName := range listOutput.PolicyNames {
		getInput := &iam.GetRolePolicyInput{
			RoleName:   aws.String(roleName),
			PolicyName: aws.String(policyName),
		}

		getOutput, err := iamClient.GetRolePolicy(ctx, getInput)
		if err != nil {
			return nil, fmt.Errorf("failed to get inline policy %s: %v", policyName, err)
		}

		policyDocument, err := parsePolicyDocument(*getOutput.PolicyDocument)
		if err != nil {
			return nil, fmt.Errorf("failed to parse policy document for %s: %v", policyName, err)
		}

		inlinePolicies = append(inlinePolicies, InlinePolicy{
			PolicyName:     policyName,
			PolicyDocument: policyDocument,
		})
	}

	return inlinePolicies, nil
}

func getAttachedPolicies(ctx context.Context, iamClient *iam.Client, roleName string) ([]AttachedPolicy, error) {
	listInput := &iam.ListAttachedRolePoliciesInput{
		RoleName: aws.String(roleName),
	}

	listOutput, err := iamClient.ListAttachedRolePolicies(ctx, listInput)
	if err != nil {
		return nil, fmt.Errorf("failed to list attached policies: %v", err)
	}
	return buildAttachedPoliciesDetails(ctx, iamClient, listOutput.AttachedPolicies)
}

func getUserInlinePolicies(ctx context.Context, iamClient *iam.Client, userName string) ([]InlinePolicy, error) {
	var inlinePolicies []InlinePolicy

	listInput := &iam.ListUserPoliciesInput{
		UserName: aws.String(userName),
	}

	listOutput, err := iamClient.ListUserPolicies(ctx, listInput)
	if err != nil {
		return nil, fmt.Errorf("failed to list user inline policies: %v", err)
	}

	for _, policyName := range listOutput.PolicyNames {
		getInput := &iam.GetUserPolicyInput{
			UserName:   aws.String(userName),
			PolicyName: aws.String(policyName),
		}

		getOutput, err := iamClient.GetUserPolicy(ctx, getInput)
		if err != nil {
			return nil, fmt.Errorf("failed to get user inline policy %s: %v", policyName, err)
		}

		policyDocument, err := parsePolicyDocument(*getOutput.PolicyDocument)
		if err != nil {
			return nil, fmt.Errorf("failed to parse user policy document for %s: %v", policyName, err)
		}

		inlinePolicies = append(inlinePolicies, InlinePolicy{
			PolicyName:     policyName,
			PolicyDocument: policyDocument,
		})
	}

	return inlinePolicies, nil
}

func getUserAttachedPolicies(ctx context.Context, iamClient *iam.Client, userName string) ([]AttachedPolicy, error) {
	listInput := &iam.ListAttachedUserPoliciesInput{
		UserName: aws.String(userName),
	}

	listOutput, err := iamClient.ListAttachedUserPolicies(ctx, listInput)
	if err != nil {
		return nil, fmt.Errorf("failed to list user attached policies: %v", err)
	}
	return buildAttachedPoliciesDetails(ctx, iamClient, listOutput.AttachedPolicies)
}

// buildAttachedPoliciesDetails converts a list of attached policy summaries into fully
// populated AttachedPolicy values by fetching policy metadata and default version documents.
func buildAttachedPoliciesDetails(
	ctx context.Context,
	iamClient *iam.Client,
	summaries []iamtypes.AttachedPolicy,
) ([]AttachedPolicy, error) {
	var detailedPolicies []AttachedPolicy

	for _, summary := range summaries {
		getPolicyOutput, err := iamClient.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: summary.PolicyArn})
		if err != nil {
			return nil, fmt.Errorf("failed to get policy %s: %v", aws.ToString(summary.PolicyArn), err)
		}

		getPolicyVersionOutput, err := iamClient.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
			PolicyArn: summary.PolicyArn,
			VersionId: getPolicyOutput.Policy.DefaultVersionId,
		})
		if err != nil {
			return nil, fmt.Errorf("failed to get policy version for %s: %v", aws.ToString(summary.PolicyArn), err)
		}

		policyDocument, err := parsePolicyDocument(aws.ToString(getPolicyVersionOutput.PolicyVersion.Document))
		if err != nil {
			return nil, fmt.Errorf("failed to parse policy document for %s: %v", aws.ToString(summary.PolicyArn), err)
		}

		description := ""
		if getPolicyOutput.Policy.Description != nil {
			description = aws.ToString(getPolicyOutput.Policy.Description)
		}

		detailedPolicies = append(detailedPolicies, AttachedPolicy{
			PolicyName:     aws.ToString(summary.PolicyName),
			PolicyArn:      aws.ToString(summary.PolicyArn),
			PolicyDocument: policyDocument,
			Description:    description,
		})
	}

	return detailedPolicies, nil
}

func parsePolicyDocument(encodedDocument string) (map[string]interface{}, error) {
	decodedDocument, err := decodePolicyDocument(encodedDocument)
	if err != nil {
		return nil, err
	}

	var policyDocument map[string]interface{}
	if err := json.Unmarshal([]byte(decodedDocument), &policyDocument); err != nil {
		return nil, fmt.Errorf("failed to parse policy document JSON: %v", err)
	}

	return policyDocument, nil
}

// decodePolicyDocument URL-decodes a policy document as returned by IAM
// (GetPolicyVersion/GetRolePolicy/etc always URL-encode the JSON body). It is
// split out from parsePolicyDocument so callers that need the raw decoded
// JSON string — rather than an unmarshaled map — can reuse the same decode
// step (e.g. GetRolePermissionsBoundaryDoc, whose caller needs a JSON string
// for SimulatePrincipalPolicyInput.PermissionsBoundaryPolicyInputList).
func decodePolicyDocument(encodedDocument string) (string, error) {
	decodedDocument, err := url.QueryUnescape(encodedDocument)
	if err != nil {
		return "", fmt.Errorf("failed to URL decode policy document: %v", err)
	}
	return decodedDocument, nil
}

// GetRolePermissionsBoundaryDoc fetches the JSON document of the IAM
// permissions boundary attached to roleName, if any. It returns
// present=false (doc="", err=nil) when the role has no permissions boundary
// attached — this is the common case, not an error.
//
// The returned doc is the URL-DECODED JSON string of the boundary policy's
// default version, suitable for passing directly as one element of
// SimulatePrincipalPolicyInput.PermissionsBoundaryPolicyInputList (which
// wants a JSON string, not a parsed map — unlike parsePolicyDocument's
// callers).
//
// This is a thin AWS wrapper (GetRole → GetPolicy → GetPolicyVersion), like
// GetAllRolePolicies above: not unit-tested here, validated against a live
// boundary-attached role instead.
func GetRolePermissionsBoundaryDoc(ctx context.Context, iamClient *iam.Client, roleName string) (string, bool, error) {
	roleOutput, err := iamClient.GetRole(ctx, &iam.GetRoleInput{RoleName: aws.String(roleName)})
	if err != nil {
		return "", false, fmt.Errorf("failed to get role %s: %v", roleName, err)
	}

	boundary := roleOutput.Role.PermissionsBoundary
	if boundary == nil || aws.ToString(boundary.PermissionsBoundaryArn) == "" {
		return "", false, nil
	}
	boundaryArn := aws.ToString(boundary.PermissionsBoundaryArn)

	getPolicyOutput, err := iamClient.GetPolicy(ctx, &iam.GetPolicyInput{PolicyArn: aws.String(boundaryArn)})
	if err != nil {
		return "", false, fmt.Errorf("failed to get permissions boundary policy %s for role %s: %v", boundaryArn, roleName, err)
	}

	getPolicyVersionOutput, err := iamClient.GetPolicyVersion(ctx, &iam.GetPolicyVersionInput{
		PolicyArn: aws.String(boundaryArn),
		VersionId: getPolicyOutput.Policy.DefaultVersionId,
	})
	if err != nil {
		return "", false, fmt.Errorf("failed to get permissions boundary policy version for %s (role %s): %v", boundaryArn, roleName, err)
	}

	decodedDocument, err := decodePolicyDocument(aws.ToString(getPolicyVersionOutput.PolicyVersion.Document))
	if err != nil {
		return "", false, fmt.Errorf("failed to decode permissions boundary policy document for %s (role %s): %v", boundaryArn, roleName, err)
	}

	return decodedDocument, true, nil
}

func PrintRolePolicies(policies *RolePolicies) {
	fmt.Printf("IAM Role Policies for: %s\n", policies.RoleName)
	fmt.Printf("Role ARN: %s\n\n", policies.RoleArn)

	// Print inline policies
	if len(policies.InlinePolicies) > 0 {
		fmt.Printf("Inline Policies (%d):\n", len(policies.InlinePolicies))
		for i, policy := range policies.InlinePolicies {
			fmt.Printf("  %d. Policy Name: %s\n", i+1, policy.PolicyName)
			policyJSON, _ := json.MarshalIndent(policy.PolicyDocument, "     ", "  ")
			fmt.Printf("     Policy Document:\n%s\n\n", string(policyJSON))
		}
	} else {
		fmt.Println("No inline policies found")
	}

	// Print attached policies
	if len(policies.AttachedPolicies) > 0 {
		fmt.Printf("Attached Policies (%d):\n", len(policies.AttachedPolicies))
		for i, policy := range policies.AttachedPolicies {
			fmt.Printf("  %d. Policy Name: %s\n", i+1, policy.PolicyName)
			fmt.Printf("     Policy ARN: %s\n", policy.PolicyArn)
			if policy.Description != "" {
				fmt.Printf("     Description: %s\n", policy.Description)
			}
			policyJSON, _ := json.MarshalIndent(policy.PolicyDocument, "     ", "  ")
			fmt.Printf("     Policy Document:\n%s\n\n", string(policyJSON))
		}
	} else {
		fmt.Println("No attached policies found")
	}
}
