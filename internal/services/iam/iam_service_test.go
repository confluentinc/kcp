package iam

import (
	"reflect"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	iamtypes "github.com/aws/aws-sdk-go-v2/service/iam/types"
)

func TestExtractPrincipalFromArn(t *testing.T) {
	t.Run("role principal", func(t *testing.T) {
		name, typ, err := extractPrincipalFromArn("arn:aws:iam::123456789000:role/mskRoleTestARN")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if name != "mskRoleTestARN" || typ != "role" {
			t.Fatalf("unexpected result: %s %s", name, typ)
		}
	})

	t.Run("user principal", func(t *testing.T) {
		name, typ, err := extractPrincipalFromArn("arn:aws:iam::123456789000:user/mskUserTestARN")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if name != "mskUserTestARN" || typ != "user" {
			t.Fatalf("unexpected result: %s %s", name, typ)
		}
	})
}

func TestResolveManagedPolicyDoc(t *testing.T) {
	const policyArn = "arn:aws:iam::123456789000:policy/kafka-access"
	const otherArn = "arn:aws:iam::123456789000:policy/other-access"

	wantDoc := map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{"Effect": "Allow", "Action": "kafka:*", "Resource": "*"},
		},
	}
	docJSON := `{"Version":"2012-10-17","Statement":[{"Effect":"Allow","Action":"kafka:*","Resource":"*"}]}`

	t.Run("resolves via IsDefaultVersion flag", func(t *testing.T) {
		policies := []iamtypes.ManagedPolicyDetail{
			{
				Arn:              aws.String(policyArn),
				DefaultVersionId: aws.String("v2"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: false, Document: aws.String(`{"stale":true}`)},
					{VersionId: aws.String("v2"), IsDefaultVersion: true, Document: aws.String(docJSON)},
				},
			},
		}

		got, err := resolveManagedPolicyDoc(policies, policyArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, wantDoc) {
			t.Fatalf("unexpected document: %#v", got)
		}
	})

	t.Run("falls back to VersionId match against DefaultVersionId", func(t *testing.T) {
		// No PolicyVersion has IsDefaultVersion set; must fall back to matching
		// VersionId against the policy's DefaultVersionId.
		policies := []iamtypes.ManagedPolicyDetail{
			{
				Arn:              aws.String(policyArn),
				DefaultVersionId: aws.String("v3"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: false, Document: aws.String(`{"stale":true}`)},
					{VersionId: aws.String("v3"), IsDefaultVersion: false, Document: aws.String(docJSON)},
				},
			},
		}

		got, err := resolveManagedPolicyDoc(policies, policyArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, wantDoc) {
			t.Fatalf("unexpected document: %#v", got)
		}
	})

	t.Run("selects the requested policy out of several", func(t *testing.T) {
		policies := []iamtypes.ManagedPolicyDetail{
			{
				Arn:              aws.String(otherArn),
				DefaultVersionId: aws.String("v1"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: true, Document: aws.String(`{"wrong":true}`)},
				},
			},
			{
				Arn:              aws.String(policyArn),
				DefaultVersionId: aws.String("v1"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: true, Document: aws.String(docJSON)},
				},
			},
		}

		got, err := resolveManagedPolicyDoc(policies, policyArn)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, wantDoc) {
			t.Fatalf("unexpected document: %#v", got)
		}
	})

	t.Run("errors when policy ARN is not present", func(t *testing.T) {
		policies := []iamtypes.ManagedPolicyDetail{
			{
				Arn:              aws.String(otherArn),
				DefaultVersionId: aws.String("v1"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: true, Document: aws.String(docJSON)},
				},
			},
		}

		if _, err := resolveManagedPolicyDoc(policies, policyArn); err == nil {
			t.Fatal("expected error for missing policy ARN, got nil")
		}
	})

	t.Run("errors when no version is marked default and none matches DefaultVersionId", func(t *testing.T) {
		policies := []iamtypes.ManagedPolicyDetail{
			{
				Arn:              aws.String(policyArn),
				DefaultVersionId: aws.String("v9"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: false, Document: aws.String(docJSON)},
				},
			},
		}

		if _, err := resolveManagedPolicyDoc(policies, policyArn); err == nil {
			t.Fatal("expected error for missing default version, got nil")
		}
	})

	t.Run("errors when the default version document cannot be parsed", func(t *testing.T) {
		policies := []iamtypes.ManagedPolicyDetail{
			{
				Arn:              aws.String(policyArn),
				DefaultVersionId: aws.String("v1"),
				PolicyVersionList: []iamtypes.PolicyVersion{
					{VersionId: aws.String("v1"), IsDefaultVersion: true, Document: aws.String("not-json")},
				},
			},
		}

		if _, err := resolveManagedPolicyDoc(policies, policyArn); err == nil {
			t.Fatal("expected parse error, got nil")
		}
	})
}
