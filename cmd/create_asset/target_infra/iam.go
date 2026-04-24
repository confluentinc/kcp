package targetinfra

import "github.com/confluentinc/kcp/internal/services/iampolicy"

// IAM permissions required to `terraform apply` / `terraform destroy` the
// output of `kcp create-asset target-infra`. Captured via iamlive against
// the generated Terraform and decomposed into a base (common to both
// cluster types) plus a per-variant addition.
//
// To regenerate after a Terraform change, re-run iamlive against both the
// enterprise and dedicated paths, diff the resulting action sets, and
// refresh the fragments below. The iam_test golden file locks the rendered
// markdown so any drift shows up in review.

var (
	// identityFragment — every AWS call path needs sts:GetCallerIdentity
	// (provider identity check).
	identityFragment = []string{
		"sts:GetCallerIdentity",
	}

	// vpcNetworkingFragment — subnet + security-group CRUD and VPC
	// metadata describes common to both target-infra variants.
	vpcNetworkingFragment = []string{
		"ec2:AuthorizeSecurityGroupEgress",
		"ec2:AuthorizeSecurityGroupIngress",
		"ec2:CreateSecurityGroup",
		"ec2:CreateSubnet",
		"ec2:DeleteSecurityGroup",
		"ec2:DeleteSubnet",
		"ec2:DescribeNetworkInterfaces",
		"ec2:DescribePrefixLists",
		"ec2:DescribeSecurityGroups",
		"ec2:DescribeSubnets",
		"ec2:DescribeVpcs",
		"ec2:RevokeSecurityGroupEgress",
	}

	// vpcEndpointFragment — PrivateLink consumer VPC endpoint lifecycle.
	vpcEndpointFragment = []string{
		"ec2:CreateVpcEndpoint",
		"ec2:DeleteVpcEndpoints",
		"ec2:DescribeVpcEndpoints",
	}

	// route53HostedZoneFragment — Route53 private hosted zone used to
	// resolve the Confluent Cloud PrivateLink endpoint within the VPC.
	route53HostedZoneFragment = []string{
		"route53:AssociateVPCWithHostedZone",
		"route53:ChangeTagsForResource",
		"route53:CreateHostedZone",
		"route53:DeleteHostedZone",
		"route53:GetChange",
		"route53:GetHostedZone",
		"route53:ListResourceRecordSets",
		"route53:ListTagsForResource",
	}

	// targetInfraBase — actions required regardless of --cluster-type.
	targetInfraBase = iampolicy.Union(
		identityFragment,
		vpcNetworkingFragment,
		vpcEndpointFragment,
		route53HostedZoneFragment,
	)

	// targetInfraEnterpriseAdditions — Enterprise PrivateLink Attachment
	// needs AZ metadata to place subnets across the zones Confluent
	// Cloud exposes for the attachment.
	targetInfraEnterpriseAdditions = []string{
		"ec2:DescribeAvailabilityZones",
	}

	// targetInfraDedicatedAdditions — Dedicated private-networking reuses
	// the caller's existing subnets; no extra permissions beyond base.
	targetInfraDedicatedAdditions []string
)

const targetInfraIAMIntro = "`kcp create-asset target-infra` itself only reads local configuration. " +
	"The generated Terraform provisions Confluent Cloud resources and (when `--needs-private-link` is set) AWS networking — VPC endpoint, security group, and optionally a Route53 private hosted zone with alias records. " +
	"The executor of `terraform apply` / `terraform destroy` needs the base policy below plus the addition matching the chosen `--cluster-type`.\n\n" +
	"!!! warning \"Scope down for production\"\n\n" +
	"    The policies below use `\"Resource\": \"*\"`. Narrow each statement to specific ARNs or `aws:ResourceTag` conditions before granting this policy to a CI/CD or pipeline role."

func iamAnnotation() string {
	return iampolicy.Render(
		targetInfraIAMIntro,
		targetInfraBase,
		[]iampolicy.Variant{
			{
				FlagHint:  "--cluster-type enterprise",
				Summary:   "Enterprise PrivateLink Attachment places subnets across Confluent-selected availability zones, which requires an extra AZ describe.",
				Additions: targetInfraEnterpriseAdditions,
			},
			{
				FlagHint:  "--cluster-type dedicated",
				Summary:   "Dedicated clusters with PrivateLink reuse the caller's existing subnets and VPC endpoints.",
				Additions: targetInfraDedicatedAdditions,
			},
		},
	)
}
