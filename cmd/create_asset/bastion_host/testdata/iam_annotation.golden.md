`kcp create-asset bastion-host` itself only reads local configuration. The generated Terraform provisions EC2, subnet, security group, route table and (optionally) internet gateway resources; the executor of `terraform apply` needs a policy equivalent to:

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Sid": "EC2ReadOnlyAccess",
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeAvailabilityZones",
        "ec2:DescribeImages",
        "ec2:DescribeInstanceCreditSpecifications",
        "ec2:DescribeInstanceTypes",
        "ec2:DescribeInstances",
        "ec2:DescribeInternetGateways",
        "ec2:DescribeKeyPairs",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribeRouteTables",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSubnets",
        "ec2:DescribeTags",
        "ec2:DescribeVolumes"
      ],
      "Resource": "*"
    },
    {
      "Sid": "MigrationKeyPairManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:DeleteKeyPair",
        "ec2:DescribeKeyPairs",
        "ec2:ImportKeyPair",
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:key-pair/migration-ssh-key"
    },
    {
      "Sid": "InternetGatewayManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:AttachInternetGateway",
        "ec2:CreateInternetGateway",
        "ec2:CreateTags",
        "ec2:DeleteInternetGateway"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:internet-gateway/*"
    },
    {
      "Sid": "VPCResourceCreation",
      "Effect": "Allow",
      "Action": [
        "ec2:AttachInternetGateway",
        "ec2:CreateRouteTable",
        "ec2:CreateSecurityGroup",
        "ec2:CreateSubnet"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:vpc/*"
    },
    {
      "Sid": "SubnetManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:AssociateRouteTable",
        "ec2:CreateSubnet",
        "ec2:CreateTags",
        "ec2:DeleteSubnet",
        "ec2:DisassociateRouteTable",
        "ec2:ModifySubnetAttribute",
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:subnet/*"
    },
    {
      "Sid": "SecurityGroupManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:AuthorizeSecurityGroupEgress",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:CreateSecurityGroup",
        "ec2:CreateTags",
        "ec2:DeleteSecurityGroup",
        "ec2:RevokeSecurityGroupEgress",
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:security-group/*"
    },
    {
      "Sid": "RouteTableManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:AssociateRouteTable",
        "ec2:CreateRoute",
        "ec2:CreateRouteTable",
        "ec2:CreateTags",
        "ec2:DeleteRouteTable",
        "ec2:DisassociateRouteTable"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:route-table/*"
    },
    {
      "Sid": "InstanceLifecycleManagement",
      "Effect": "Allow",
      "Action": [
        "ec2:CreateTags",
        "ec2:DescribeInstanceAttribute",
        "ec2:RunInstances",
        "ec2:TerminateInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:instance/*"
    },
    {
      "Sid": "InstanceLaunchNetworkInterface",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:network-interface/*"
    },
    {
      "Sid": "InstanceLaunchVolume",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>:<AWS ACCOUNT ID>:volume/*"
    },
    {
      "Sid": "InstanceLaunchAMI",
      "Effect": "Allow",
      "Action": [
        "ec2:RunInstances"
      ],
      "Resource": "arn:aws:ec2:<AWS REGION>::image/*"
    }
  ]
}
```
