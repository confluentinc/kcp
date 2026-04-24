`kcp create-asset target-infra` itself only reads local configuration. The generated Terraform provisions Confluent Cloud resources and (when `--needs-private-link` is set) AWS networking — VPC endpoint, security group, and optionally a Route53 private hosted zone with alias records. The executor of `terraform apply` / `terraform destroy` needs the base policy below plus the addition matching the chosen `--cluster-type`. `Resource: "*"` mirrors the captured `iamlive` output — operators are free to tighten scope in production.

#### Base — always required

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:AuthorizeSecurityGroupEgress",
        "ec2:AuthorizeSecurityGroupIngress",
        "ec2:CreateSecurityGroup",
        "ec2:CreateSubnet",
        "ec2:CreateVpcEndpoint",
        "ec2:DeleteSecurityGroup",
        "ec2:DeleteSubnet",
        "ec2:DeleteVpcEndpoints",
        "ec2:DescribeNetworkInterfaces",
        "ec2:DescribePrefixLists",
        "ec2:DescribeSecurityGroups",
        "ec2:DescribeSubnets",
        "ec2:DescribeVpcEndpoints",
        "ec2:DescribeVpcs",
        "ec2:RevokeSecurityGroupEgress",
        "route53:AssociateVPCWithHostedZone",
        "route53:ChangeTagsForResource",
        "route53:CreateHostedZone",
        "route53:DeleteHostedZone",
        "route53:GetChange",
        "route53:GetHostedZone",
        "route53:ListResourceRecordSets",
        "route53:ListTagsForResource",
        "sts:GetCallerIdentity"
      ],
      "Resource": "*"
    }
  ]
}
```

#### Additional for `--cluster-type enterprise`

Enterprise PrivateLink Attachment places subnets across Confluent-selected availability zones, which requires an extra AZ describe.

```json
{
  "Version": "2012-10-17",
  "Statement": [
    {
      "Effect": "Allow",
      "Action": [
        "ec2:DescribeAvailabilityZones"
      ],
      "Resource": "*"
    }
  ]
}
```

#### Additional for `--cluster-type dedicated`

Dedicated clusters with PrivateLink reuse the caller's existing subnets and VPC endpoints.

_No additional permissions beyond the base._
