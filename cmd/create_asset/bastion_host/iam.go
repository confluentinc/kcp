package bastion_host

import "github.com/confluentinc/kcp/internal/services/iampolicy"

// bastionHostIAMIntro is the prose preamble rendered above the policy
// JSON block in the generated command reference.
const bastionHostIAMIntro = "`kcp create-asset bastion-host` itself only reads local configuration. " +
	"The generated Terraform provisions EC2, subnet, security group, route table and (optionally) internet gateway resources; " +
	"the executor of `terraform apply` needs a policy equivalent to:"

// bastionHostIAMAnnotation assembles the IAM permissions markdown from the
// statement fragments below. Statement order here is the order it renders
// in the docs — keep the Read-only describes first and scoped-ARN grants
// after.
func bastionHostIAMAnnotation() string {
	return iampolicy.RenderStatements(bastionHostIAMIntro, []iampolicy.Statement{
		{
			Sid: "EC2ReadOnlyAccess",
			Actions: []string{
				"ec2:DescribeImages",
				"ec2:DescribeAvailabilityZones",
				"ec2:DescribeKeyPairs",
				"ec2:DescribeInternetGateways",
				"ec2:DescribeSubnets",
				"ec2:DescribeSecurityGroups",
				"ec2:DescribeNetworkInterfaces",
				"ec2:DescribeRouteTables",
				"ec2:DescribeInstances",
				"ec2:DescribeInstanceTypes",
				"ec2:DescribeTags",
				"ec2:DescribeVolumes",
				"ec2:DescribeInstanceCreditSpecifications",
			},
		},
		{
			Sid: "MigrationKeyPairManagement",
			Actions: []string{
				"ec2:ImportKeyPair",
				"ec2:DescribeKeyPairs",
				"ec2:DeleteKeyPair",
				"ec2:RunInstances",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/migration-ssh-key"},
		},
		{
			Sid: "InternetGatewayManagement",
			Actions: []string{
				"ec2:CreateInternetGateway",
				"ec2:CreateTags",
				"ec2:AttachInternetGateway",
				"ec2:DeleteInternetGateway",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:internet-gateway/*"},
		},
		{
			Sid: "VPCResourceCreation",
			Actions: []string{
				"ec2:CreateSubnet",
				"ec2:CreateSecurityGroup",
				"ec2:AttachInternetGateway",
				"ec2:CreateRouteTable",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:vpc/*"},
		},
		{
			Sid: "SubnetManagement",
			Actions: []string{
				"ec2:CreateSubnet",
				"ec2:CreateTags",
				"ec2:DeleteSubnet",
				"ec2:ModifySubnetAttribute",
				"ec2:AssociateRouteTable",
				"ec2:RunInstances",
				"ec2:DisassociateRouteTable",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:subnet/*"},
		},
		{
			Sid: "SecurityGroupManagement",
			Actions: []string{
				"ec2:CreateSecurityGroup",
				"ec2:CreateTags",
				"ec2:DeleteSecurityGroup",
				"ec2:RevokeSecurityGroupEgress",
				"ec2:AuthorizeSecurityGroupIngress",
				"ec2:AuthorizeSecurityGroupEgress",
				"ec2:RunInstances",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:security-group/*"},
		},
		{
			Sid: "RouteTableManagement",
			Actions: []string{
				"ec2:CreateRouteTable",
				"ec2:CreateTags",
				"ec2:DeleteRouteTable",
				"ec2:CreateRoute",
				"ec2:AssociateRouteTable",
				"ec2:DisassociateRouteTable",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:route-table/*"},
		},
		{
			Sid: "InstanceLifecycleManagement",
			Actions: []string{
				"ec2:RunInstances",
				"ec2:CreateTags",
				"ec2:DescribeInstanceAttribute",
				"ec2:TerminateInstances",
			},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:instance/*"},
		},
		{
			Sid:       "InstanceLaunchNetworkInterface",
			Actions:   []string{"ec2:RunInstances"},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:network-interface/*"},
		},
		{
			Sid:       "InstanceLaunchVolume",
			Actions:   []string{"ec2:RunInstances"},
			Resources: []string{"arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:volume/*"},
		},
		{
			Sid:       "InstanceLaunchAMI",
			Actions:   []string{"ec2:RunInstances"},
			Resources: []string{"arn:aws:ec2:<AWS REGION>::image/*"},
		},
	})
}
