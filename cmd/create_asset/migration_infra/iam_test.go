package migration_infra

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/confluentinc/kcp/internal/services/iampolicy"
)

// capturedType2Enterprise is the raw iamlive capture for
// --type 2 (Enterprise, SASL/SCRAM). Apply-phase only — iamlive wasn't run
// against a `terraform destroy`, so destroy actions that Type 3 has are
// missing here even though operators need them for a symmetric role.
var capturedType2Enterprise = []string{
	"sts:GetCallerIdentity",
	"ec2:DescribeVpcs",
	"ec2:DescribeVpcAttribute",
	"ec2:DescribeRouteTables",
	"elasticloadbalancing:DescribeTargetGroups",
	"ec2:CreateSecurityGroup",
	"elasticloadbalancing:CreateTargetGroup",
	"ec2:DescribeSecurityGroups",
	"elasticloadbalancing:ModifyTargetGroupAttributes",
	"ec2:RevokeSecurityGroupEgress",
	"elasticloadbalancing:DescribeTargetGroupAttributes",
	"elasticloadbalancing:RegisterTargets",
	"ec2:AuthorizeSecurityGroupEgress",
	"elasticloadbalancing:DescribeLoadBalancers",
	"elasticloadbalancing:CreateLoadBalancer",
	"ec2:AuthorizeSecurityGroupIngress",
	"elasticloadbalancing:ModifyLoadBalancerAttributes",
	"elasticloadbalancing:DescribeLoadBalancerAttributes",
	"ec2:CreateVpcEndpointServiceConfiguration",
	"elasticloadbalancing:DescribeListeners",
	"elasticloadbalancing:DescribeListenerAttributes",
	"ec2:DescribeVpcEndpointServiceConfigurations",
	"ec2:ModifyVpcEndpointServicePermissions",
	"ec2:DescribeVpcEndpointServicePermissions",
	"ec2:DescribeImages",
	"ec2:GetInstanceUefiData",
	"ec2:RunInstances",
	"ec2:DescribeInstances",
	"ec2:DescribeInstanceTypes",
	"ec2:DescribeTags",
	"ec2:DescribeInstanceAttribute",
	"ec2:DescribeVolumes",
	"ec2:DescribeInstanceCreditSpecifications",
	"ec2:DescribeSecurityGroupRules",
	"elasticloadbalancing:DescribeTargetHealth",
}

// capturedType3Enterprise — raw iamlive for --type 3 (Enterprise,
// Unauthenticated Plaintext). Covers both apply and destroy.
var capturedType3Enterprise = []string{
	"sts:GetCallerIdentity",
	"ec2:DescribeVpcs",
	"ec2:DescribeVpcAttribute",
	"ec2:DescribeRouteTables",
	"ec2:CreateSecurityGroup",
	"elasticloadbalancing:DescribeTargetGroups",
	"ec2:DescribeSecurityGroups",
	"elasticloadbalancing:CreateTargetGroup",
	"ec2:RevokeSecurityGroupEgress",
	"elasticloadbalancing:ModifyTargetGroupAttributes",
	"elasticloadbalancing:DescribeTargetGroupAttributes",
	"elasticloadbalancing:RegisterTargets",
	"ec2:AuthorizeSecurityGroupEgress",
	"elasticloadbalancing:DescribeLoadBalancers",
	"ec2:AuthorizeSecurityGroupIngress",
	"elasticloadbalancing:CreateLoadBalancer",
	"elasticloadbalancing:ModifyLoadBalancerAttributes",
	"elasticloadbalancing:DescribeLoadBalancerAttributes",
	"ec2:CreateVpcEndpointServiceConfiguration",
	"elasticloadbalancing:DescribeListeners",
	"elasticloadbalancing:DescribeListenerAttributes",
	"ec2:DescribeVpcEndpointServiceConfigurations",
	"ec2:ModifyVpcEndpointServicePermissions",
	"ec2:DescribeVpcEndpointServicePermissions",
	"ec2:DescribeSecurityGroupRules",
	"elasticloadbalancing:DescribeTargetHealth",
	"ec2:DescribeImages",
	"ec2:GetInstanceUefiData",
	"ec2:RunInstances",
	"ec2:DescribeInstances",
	"ec2:DescribeInstanceTypes",
	"ec2:DescribeTags",
	"ec2:DescribeInstanceAttribute",
	"ec2:DescribeVolumes",
	"ec2:DescribeInstanceCreditSpecifications",
	"ec2:TerminateInstances",
	"elasticloadbalancing:DeregisterTargets",
	"elasticloadbalancing:DeleteListener",
	"ec2:DescribeVpcEndpointConnections",
	"elasticloadbalancing:DeleteTargetGroup",
	"ec2:RevokeSecurityGroupIngress",
	"ec2:RejectVpcEndpointConnections",
	"ec2:DeleteVpcEndpointServiceConfigurations",
	"elasticloadbalancing:DeleteLoadBalancer",
	"ec2:DescribeNetworkInterfaces",
	"ec2:DeleteSecurityGroup",
}

// capturedType4Enterprise — raw iamlive for --type 4 (Enterprise, SASL/SCRAM).
var capturedType4Enterprise = []string{
	"sts:GetCallerIdentity",
	"ec2:DescribeInternetGateways",
	"ec2:DescribeAvailabilityZones",
	"ec2:DescribeImages",
	"ec2:GetInstanceUefiData",
	"ec2:CreateRouteTable",
	"ec2:CreateSubnet",
	"ec2:AllocateAddress",
	"ec2:CreateSecurityGroup",
	"ec2:DescribeRouteTables",
	"ec2:DescribeSubnets",
	"ec2:DescribeSecurityGroups",
	"ec2:CreateRoute",
	"ec2:ImportKeyPair",
	"ec2:DescribeKeyPairs",
	"ec2:RevokeSecurityGroupEgress",
	"ec2:DescribeAddresses",
	"ec2:AssociateRouteTable",
	"ec2:DescribeAddressesAttribute",
	"ec2:CreateNatGateway",
	"ec2:AuthorizeSecurityGroupIngress",
	"ec2:DescribeNatGateways",
	"ec2:AuthorizeSecurityGroupEgress",
	"ec2:RunInstances",
	"ec2:DescribeInstances",
	"ec2:DescribeInstanceTypes",
	"ec2:DescribeTags",
	"ec2:DescribeVpcs",
	"ec2:DescribeInstanceAttribute",
	"ec2:DescribeVolumes",
	"ec2:DescribeInstanceCreditSpecifications",
	"ec2:DisassociateRouteTable",
	"ec2:TerminateInstances",
	"ec2:DeleteRouteTable",
	"ec2:DeleteNatGateway",
	"ec2:DeleteKeyPair",
	"ec2:DescribeNetworkInterfaces",
	"ec2:DeleteSubnet",
	"ec2:DeleteSecurityGroup",
	"ec2:DisassociateAddress",
	"ec2:ReleaseAddress",
}

// capturedType4Dedicated — raw iamlive for --type 4 (Dedicated, SASL/SCRAM).
var capturedType4Dedicated = []string{
	"sts:GetCallerIdentity",
	"ec2:DescribeImages",
	"ec2:DescribeVpcEndpoints",
	"ec2:DescribeAvailabilityZones",
	"ec2:DescribeInternetGateways",
	"ec2:DescribePrefixLists",
	"ec2:GetInstanceUefiData",
	"ec2:CreateSubnet",
	"ec2:CreateRouteTable",
	"ec2:AllocateAddress",
	"ec2:CreateSecurityGroup",
	"ec2:DescribeSubnets",
	"ec2:DescribeRouteTables",
	"ec2:DescribeSecurityGroups",
	"ec2:CreateRoute",
	"ec2:ImportKeyPair",
	"ec2:DescribeAddresses",
	"ec2:RevokeSecurityGroupEgress",
	"ec2:DescribeKeyPairs",
	"ec2:DescribeAddressesAttribute",
	"ec2:AssociateRouteTable",
	"ec2:CreateNatGateway",
	"ec2:DescribeNatGateways",
	"ec2:AuthorizeSecurityGroupIngress",
	"ec2:AuthorizeSecurityGroupEgress",
	"ec2:RunInstances",
	"ec2:DescribeInstances",
	"ec2:DescribeInstanceTypes",
	"ec2:DescribeTags",
	"ec2:DescribeVpcs",
	"ec2:DescribeInstanceAttribute",
	"ec2:DescribeVolumes",
	"ec2:DescribeInstanceCreditSpecifications",
	"ec2:DescribeSecurityGroupRules",
	"ec2:DisassociateRouteTable",
	"ec2:TerminateInstances",
	"ec2:RevokeSecurityGroupIngress",
	"ec2:DeleteRouteTable",
	"ec2:DeleteNatGateway",
	"ec2:DescribeNetworkInterfaces",
	"ec2:DeleteKeyPair",
	"ec2:DeleteSecurityGroup",
	"ec2:DeleteSubnet",
	"ec2:DisassociateAddress",
	"ec2:ReleaseAddress",
}

// capturedType5Enterprise — raw iamlive for --type 5 (Enterprise, IAM auth).
// Note: no iam:* actions in the capture — see comment in iam.go.
var capturedType5Enterprise = []string{
	"sts:GetCallerIdentity",
	"ec2:DescribeAvailabilityZones",
	"ec2:DescribeImages",
	"ec2:DescribeInternetGateways",
	"ec2:DescribeVpcEndpoints",
	"ec2:DescribePrefixLists",
	"ec2:GetInstanceUefiData",
	"ec2:CreateSubnet",
	"ec2:CreateRouteTable",
	"ec2:AllocateAddress",
	"ec2:CreateSecurityGroup",
	"ec2:DescribeRouteTables",
	"ec2:DescribeSubnets",
	"ec2:DescribeSecurityGroups",
	"ec2:CreateRoute",
	"ec2:ImportKeyPair",
	"ec2:DescribeAddresses",
	"ec2:DescribeKeyPairs",
	"ec2:RevokeSecurityGroupEgress",
	"ec2:AssociateRouteTable",
	"ec2:DescribeAddressesAttribute",
	"ec2:CreateNatGateway",
	"ec2:DescribeNatGateways",
	"ec2:AuthorizeSecurityGroupIngress",
	"ec2:AuthorizeSecurityGroupEgress",
	"ec2:RunInstances",
	"ec2:DescribeInstances",
	"ec2:DescribeInstanceTypes",
	"ec2:DescribeTags",
	"ec2:DescribeVpcs",
	"ec2:DescribeInstanceAttribute",
	"ec2:DescribeVolumes",
	"ec2:DescribeInstanceCreditSpecifications",
	"ec2:DescribeSecurityGroupRules",
	"ec2:DisassociateRouteTable",
	"ec2:TerminateInstances",
	"ec2:RevokeSecurityGroupIngress",
	"ec2:DeleteRouteTable",
	"ec2:DeleteNatGateway",
	"ec2:DescribeNetworkInterfaces",
	"ec2:DeleteKeyPair",
	"ec2:DeleteSecurityGroup",
	"ec2:DeleteSubnet",
	"ec2:DisassociateAddress",
	"ec2:ReleaseAddress",
}

// capturedType5Dedicated — iamlive capture is identical to Type 5
// Enterprise per the source notes.
var capturedType5Dedicated = capturedType5Enterprise

// TestMigrationInfraVariantsMatchCaptures guards the semantic fragment
// decomposition against the raw iamlive captures. If a fragment edit
// drops or adds an action by mistake, the variant that depended on it
// fails here with a diff against its captured truth set.
func TestMigrationInfraVariantsMatchCaptures(t *testing.T) {
	cases := []struct {
		name     string
		captured []string
		composed []string
	}{
		{"type-2-enterprise", capturedType2Enterprise, iampolicy.Union(migrationInfraBase, type2EnterpriseAdditions)},
		{"type-3-enterprise", capturedType3Enterprise, iampolicy.Union(migrationInfraBase, type3EnterpriseAdditions)},
		{"type-4-enterprise", capturedType4Enterprise, iampolicy.Union(migrationInfraBase, type4EnterpriseAdditions)},
		{"type-4-dedicated", capturedType4Dedicated, iampolicy.Union(migrationInfraBase, type4DedicatedAdditions)},
		{"type-5-enterprise", capturedType5Enterprise, iampolicy.Union(migrationInfraBase, type5EnterpriseAdditions)},
		{"type-5-dedicated", capturedType5Dedicated, iampolicy.Union(migrationInfraBase, type5DedicatedAdditions)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			want := iampolicy.Union(c.captured)
			if !reflect.DeepEqual(c.composed, want) {
				missing := iampolicy.Difference(want, c.composed)
				extra := iampolicy.Difference(c.composed, want)
				t.Fatalf("composed policy differs from captured capture.\nmissing (in capture, not composed): %v\nextra (in composed, not capture): %v", missing, extra)
			}
		})
	}
}

// TestMigrationInfraFragmentsDisjointFromBase guards against an action
// appearing in both the base and a variant's Additions — rendering
// duplicates once is confusing.
func TestMigrationInfraFragmentsDisjointFromBase(t *testing.T) {
	for name, additions := range map[string][]string{
		"type-2-enterprise": type2EnterpriseAdditions,
		"type-3-enterprise": type3EnterpriseAdditions,
		"type-4-enterprise": type4EnterpriseAdditions,
		"type-4-dedicated":  type4DedicatedAdditions,
		"type-5-enterprise": type5EnterpriseAdditions,
		"type-5-dedicated":  type5DedicatedAdditions,
	} {
		if overlap := iampolicy.Overlap(migrationInfraBase, additions); len(overlap) > 0 {
			t.Errorf("%s additions overlap base: %v", name, overlap)
		}
	}
}

// TestMigrationInfraIAMAnnotationGolden locks down the rendered markdown.
// Set UPDATE_GOLDEN=1 to refresh after an intentional change.
func TestMigrationInfraIAMAnnotationGolden(t *testing.T) {
	got := iamAnnotation()
	path := filepath.Join("testdata", "iam_annotation.golden.md")

	if envFlag("UPDATE_GOLDEN") {
		if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("updated golden file: %s", path)
		return
	}

	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden: %v (set UPDATE_GOLDEN=1 to create)", err)
	}
	if got != string(want) {
		t.Fatalf("iamAnnotation() output differs from golden %s.\n"+
			"Set UPDATE_GOLDEN=1 to accept the new output after review.\n"+
			"--- got ---\n%s\n--- want ---\n%s", path, got, string(want))
	}
}

func envFlag(name string) bool {
	v, ok := os.LookupEnv(name)
	return ok && v != "" && v != "0" && v != "false"
}
