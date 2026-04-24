package migration_infra

import "github.com/confluentinc/kcp/internal/services/iampolicy"

// IAM permissions required to `terraform apply` / `terraform destroy` the
// output of `kcp create-asset migration-infra`. Captured via iamlive.
//
// Fragments below decompose the per-type captures into reusable named
// groups. Each variant's Additions is the union of the fragments it
// actually exercises. The raw captured sets are reproduced in iam_test.go
// (capturedType*) and the tests assert that our composed Additions match
// each capture exactly — so if you update a fragment, you'll see the
// failure point to the type whose capture you broke.

// ---------------------------------------------------------------------------
// Fragments — semantic building blocks shared across migration-infra types.
// ---------------------------------------------------------------------------

var (
	// identityFragment — AWS provider identity check (every variant).
	identityFragment = []string{
		"sts:GetCallerIdentity",
	}

	// ec2HostCoreFragment — launching and describing an EC2 host (the
	// outbound cluster link host for types 2/3 or jump-cluster brokers
	// for types 4/5).
	ec2HostCoreFragment = []string{
		"ec2:DescribeImages",
		"ec2:DescribeInstanceAttribute",
		"ec2:DescribeInstanceCreditSpecifications",
		"ec2:DescribeInstanceTypes",
		"ec2:DescribeInstances",
		"ec2:DescribeTags",
		"ec2:DescribeVolumes",
		"ec2:GetInstanceUefiData",
		"ec2:RunInstances",
	}

	// sgCoreFragment — security group create + ingress/egress rule
	// management present in every migration-infra capture.
	sgCoreFragment = []string{
		"ec2:AuthorizeSecurityGroupEgress",
		"ec2:AuthorizeSecurityGroupIngress",
		"ec2:CreateSecurityGroup",
		"ec2:DescribeSecurityGroups",
		"ec2:RevokeSecurityGroupEgress",
	}

	// vpcCoreFragment — VPC describe (universal).
	vpcCoreFragment = []string{
		"ec2:DescribeVpcs",
	}

	// ec2HostDestroyFragment — destroy-phase actions for an EC2 host.
	// Absent from the Type 2 capture (apply-only run); included in
	// Types 3, 4, 5. See iam_test.go for notes.
	ec2HostDestroyFragment = []string{
		"ec2:TerminateInstances",
	}

	// sgDestroyFragment — security group destroy-phase + ENI describe
	// (Route53/NLB attachments leave dangling ENIs that must be
	// described before the SG can be deleted).
	sgDestroyFragment = []string{
		"ec2:DeleteSecurityGroup",
		"ec2:DescribeNetworkInterfaces",
		"ec2:DescribeSecurityGroupRules",
		"ec2:RevokeSecurityGroupIngress",
	}

	// vpcEndpointServiceFragment — Types 2 & 3 expose the outbound
	// cluster link host as a VPC Endpoint Service.
	vpcEndpointServiceFragment = []string{
		"ec2:CreateVpcEndpointServiceConfiguration",
		"ec2:DescribeVpcEndpointServiceConfigurations",
		"ec2:DescribeVpcEndpointServicePermissions",
		"ec2:ModifyVpcEndpointServicePermissions",
	}

	// vpcEndpointServiceDestroyFragment — cleanup side of vpc endpoint
	// service (Type 3 capture; Type 2 capture is apply-only).
	vpcEndpointServiceDestroyFragment = []string{
		"ec2:DeleteVpcEndpointServiceConfigurations",
		"ec2:DescribeVpcEndpointConnections",
		"ec2:RejectVpcEndpointConnections",
	}

	// nlbFragment — Network Load Balancer + target group + listener
	// (Types 2 & 3; the NLB front-ends the outbound cluster link host).
	nlbFragment = []string{
		"ec2:DescribeRouteTables",
		"ec2:DescribeVpcAttribute",
		"elasticloadbalancing:CreateLoadBalancer",
		"elasticloadbalancing:CreateTargetGroup",
		"elasticloadbalancing:DescribeListenerAttributes",
		"elasticloadbalancing:DescribeListeners",
		"elasticloadbalancing:DescribeLoadBalancerAttributes",
		"elasticloadbalancing:DescribeLoadBalancers",
		"elasticloadbalancing:DescribeTargetGroupAttributes",
		"elasticloadbalancing:DescribeTargetGroups",
		"elasticloadbalancing:DescribeTargetHealth",
		"elasticloadbalancing:ModifyLoadBalancerAttributes",
		"elasticloadbalancing:ModifyTargetGroupAttributes",
		"elasticloadbalancing:RegisterTargets",
	}

	// nlbDestroyFragment — destroy-phase for the NLB stack (Type 3).
	nlbDestroyFragment = []string{
		"elasticloadbalancing:DeleteListener",
		"elasticloadbalancing:DeleteLoadBalancer",
		"elasticloadbalancing:DeleteTargetGroup",
		"elasticloadbalancing:DeregisterTargets",
	}

	// jumpClusterSubnetFragment — subnet + AZ describes for the
	// jump-cluster VPC layout (Types 4 & 5).
	jumpClusterSubnetFragment = []string{
		"ec2:CreateSubnet",
		"ec2:DeleteSubnet",
		"ec2:DescribeAvailabilityZones",
		"ec2:DescribeSubnets",
	}

	// jumpClusterRouteTableFragment — route table + route CRUD for the
	// jump-cluster VPC layout (Types 4 & 5).
	jumpClusterRouteTableFragment = []string{
		"ec2:AssociateRouteTable",
		"ec2:CreateRoute",
		"ec2:CreateRouteTable",
		"ec2:DeleteRouteTable",
		"ec2:DescribeRouteTables",
		"ec2:DisassociateRouteTable",
	}

	// jumpClusterNatFragment — NAT gateway + Elastic IP for outbound
	// traffic from the jump cluster (Types 4 & 5).
	jumpClusterNatFragment = []string{
		"ec2:AllocateAddress",
		"ec2:CreateNatGateway",
		"ec2:DeleteNatGateway",
		"ec2:DescribeAddresses",
		"ec2:DescribeAddressesAttribute",
		"ec2:DescribeNatGateways",
		"ec2:DisassociateAddress",
		"ec2:ReleaseAddress",
	}

	// jumpClusterKeyPairFragment — SSH key pair for jump-cluster hosts
	// (Types 4 & 5).
	jumpClusterKeyPairFragment = []string{
		"ec2:DeleteKeyPair",
		"ec2:DescribeKeyPairs",
		"ec2:ImportKeyPair",
	}

	// jumpClusterInternetGatewayFragment — IGW describe required when
	// reusing an existing gateway (Types 4 & 5).
	jumpClusterInternetGatewayFragment = []string{
		"ec2:DescribeInternetGateways",
	}

	// privateLinkConsumerFragment — when the target is a Dedicated
	// cluster, the jump cluster consumes a Confluent-provided VPC
	// Endpoint Service via PrivateLink and needs to describe endpoints
	// and managed prefix lists.
	privateLinkConsumerFragment = []string{
		"ec2:DescribePrefixLists",
		"ec2:DescribeVpcEndpoints",
	}
)

// ---------------------------------------------------------------------------
// Composition — base policy and per-variant Additions.
// ---------------------------------------------------------------------------

// migrationInfraBase is the intersection of actions present in every
// migration-infra variant that provisions AWS resources (Types 2–5).
// Type 1 is Confluent-only and has no AWS policy.
var migrationInfraBase = iampolicy.Union(
	identityFragment,
	vpcCoreFragment,
	sgCoreFragment,
	ec2HostCoreFragment,
)

// type2EnterpriseAdditions — Enterprise Type 2 (External Outbound Cluster
// Link, SASL/SCRAM). The captured policy is apply-phase only; destroy
// permissions are the same as Type 3 and operators should grant them too.
// See iam_test.go for the captured superset.
var type2EnterpriseAdditions = iampolicy.Union(
	nlbFragment,
	vpcEndpointServiceFragment,
	[]string{"ec2:DescribeSecurityGroupRules"},
)

// type3EnterpriseAdditions — Enterprise Type 3 (External Outbound Cluster
// Link, Unauthenticated Plaintext). Same topology as Type 2 plus the
// destroy-phase permissions captured in a full apply/destroy run.
var type3EnterpriseAdditions = iampolicy.Union(
	nlbFragment,
	nlbDestroyFragment,
	vpcEndpointServiceFragment,
	vpcEndpointServiceDestroyFragment,
	sgDestroyFragment,
	ec2HostDestroyFragment,
)

// jumpClusterNetworkingAdditions — common jump-cluster infrastructure
// (Types 4 & 5, both target cluster types).
var jumpClusterNetworkingAdditions = iampolicy.Union(
	jumpClusterSubnetFragment,
	jumpClusterRouteTableFragment,
	jumpClusterNatFragment,
	jumpClusterKeyPairFragment,
	jumpClusterInternetGatewayFragment,
	sgDestroyFragment,
	ec2HostDestroyFragment,
)

// type4EnterpriseAdditions — Jump Cluster (SASL/SCRAM) with Enterprise
// target. The capture omits ec2:DescribeSecurityGroupRules,
// ec2:RevokeSecurityGroupIngress, and the PrivateLink describes that
// Type 4 Dedicated needs; we follow the capture verbatim.
var type4EnterpriseAdditions = iampolicy.Difference(
	iampolicy.Union(
		jumpClusterSubnetFragment,
		jumpClusterRouteTableFragment,
		jumpClusterNatFragment,
		jumpClusterKeyPairFragment,
		jumpClusterInternetGatewayFragment,
		[]string{
			"ec2:DeleteSecurityGroup",
			"ec2:DescribeNetworkInterfaces",
			"ec2:TerminateInstances",
		},
	),
	migrationInfraBase,
)

// type4DedicatedAdditions — Jump Cluster (SASL/SCRAM) with Dedicated
// target via Confluent PrivateLink.
var type4DedicatedAdditions = iampolicy.Union(
	jumpClusterNetworkingAdditions,
	privateLinkConsumerFragment,
)

// type5EnterpriseAdditions — Jump Cluster (IAM auth) with Enterprise
// target (MSK only). Same AWS footprint as Type 4 Enterprise plus the
// PrivateLink describes; iamlive did not capture iam:* actions (role is
// either pre-existing or captured under a different identity) — see
// iam_test.go for the anomaly note.
var type5EnterpriseAdditions = iampolicy.Union(
	jumpClusterNetworkingAdditions,
	privateLinkConsumerFragment,
)

// type5DedicatedAdditions — Jump Cluster (IAM auth) with Dedicated
// target (MSK only). Identical capture to Type 5 Enterprise.
var type5DedicatedAdditions = iampolicy.Union(
	jumpClusterNetworkingAdditions,
	privateLinkConsumerFragment,
)

// ---------------------------------------------------------------------------
// Annotation assembly.
// ---------------------------------------------------------------------------

const migrationInfraIAMIntro = "`kcp create-asset migration-infra` itself only reads local configuration. " +
	"The AWS IAM policy required to `terraform apply` / `terraform destroy` the generated output depends on `--type` and (for Types 4 & 5) the target cluster type. " +
	"**Type 1** (public MSK + Confluent Cluster Link) provisions only Confluent Cloud resources and needs no AWS IAM permissions. " +
	"For Types 2–5, apply the base policy below plus the matching variant block.\n\n" +
	"!!! warning \"Scope down for production\"\n\n" +
	"    The policies below use `\"Resource\": \"*\"` and include destructive actions (e.g. `ec2:TerminateInstances`, `ec2:DeleteNatGateway`, `elasticloadbalancing:DeleteLoadBalancer`). Narrow each statement to specific ARNs or `aws:ResourceTag` conditions before granting this policy to a CI/CD or pipeline role."

func iamAnnotation() string {
	return iampolicy.Render(
		migrationInfraIAMIntro,
		migrationInfraBase,
		[]iampolicy.Variant{
			{
				FlagHint:  "--type 2 (Enterprise target, SASL/SCRAM)",
				Summary:   "External Outbound Cluster Link fronted by a Network Load Balancer + VPC Endpoint Service that Confluent consumes. Grant the destroy counterparts listed under Type 3 for a symmetric apply/destroy role.",
				Additions: type2EnterpriseAdditions,
			},
			{
				FlagHint:  "--type 3 (Enterprise target, Unauthenticated Plaintext)",
				Summary:   "Same topology as Type 2 (NLB + VPC Endpoint Service), captured across a full apply and destroy so includes the teardown permissions.",
				Additions: type3EnterpriseAdditions,
			},
			{
				FlagHint:  "--type 4 (Enterprise target, SASL/SCRAM)",
				Summary:   "Jump cluster (EC2 brokers + NAT gateway + route tables + key pair) fronted by Confluent's managed NLB to an Enterprise target.",
				Additions: type4EnterpriseAdditions,
			},
			{
				FlagHint:  "--type 4 (Dedicated target, SASL/SCRAM)",
				Summary:   "Same jump-cluster stack as the Enterprise variant, plus PrivateLink consumer permissions to describe the VPC endpoints exposed by the Dedicated target.",
				Additions: type4DedicatedAdditions,
			},
			{
				FlagHint:  "--type 5 (Enterprise target, IAM auth — MSK only)",
				Summary:   "Jump cluster with IAM authentication to MSK. NOTE: the captured policy does not include `iam:*` actions; grant the role the right to create/pass the jump-cluster instance profile separately if Terraform must provision it.",
				Additions: type5EnterpriseAdditions,
			},
			{
				FlagHint:  "--type 5 (Dedicated target, IAM auth — MSK only)",
				Summary:   "Jump cluster with IAM authentication to MSK, via Confluent PrivateLink to a Dedicated target. Same IAM-role caveat as the Enterprise Type 5 variant.",
				Additions: type5DedicatedAdditions,
			},
		},
	)
}
