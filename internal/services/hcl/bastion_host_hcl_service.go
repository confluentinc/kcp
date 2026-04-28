package hcl

import (
	_ "embed"
	"time"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/other"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

//go:embed bastion_host/bastion-host-user-data.tpl
var bastionHostUserDataTpl string

type BastionHostHCLService struct {
	// DeploymentID overrides the random deployment identifier in AWS provider tags.
	// When empty, a random 8-character string is generated.
	DeploymentID string

	// Now overrides the current time used to stamp the bastion host instance
	// Name tag. When nil, time.Now is used.
	Now func() time.Time
}

func NewBastionHostHCLService() *BastionHostHCLService {
	return &BastionHostHCLService{}
}

func (s *BastionHostHCLService) GenerateBastionHostFiles(request types.BastionHostRequest) (types.TerraformFiles, error) {
	return types.TerraformFiles{
		MainTf:           s.generateMainTf(request),
		ProvidersTf:      s.generateProvidersTf(),
		VariablesTf:      s.generateVariablesTf(),
		OutputsTf:        s.generateOutputsTf(),
		InputsAutoTfvars: s.generateInputsAutoTfvars(request),
	}, nil
}

func (s *BastionHostHCLService) GenerateBastionHostUserDataTemplate() string {
	return bastionHostUserDataTpl
}

func (s *BastionHostHCLService) generateMainTf(request types.BastionHostRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Random string for unique key-pair naming
	rootBody.AppendBlock(other.GenerateRandomStringResource("suffix", 4, false, false, false))
	rootBody.AppendNewline()

	// data "http" "ec2_instance_connect" — fetches AWS published IP ranges so the SG
	// can allow only EC2 Instance Connect traffic for the cluster's region.
	httpBlock := rootBody.AppendNewBlock("data", []string{"http", "ec2_instance_connect"})
	httpBlock.Body().SetAttributeValue("url", cty.StringVal("https://ip-ranges.amazonaws.com/ip-ranges.json"))
	rootBody.AppendNewline()

	// locals { ec2_instance_connect_ip = [ for e in jsondecode(...) : e.ip_prefix if ... ] }
	localsBlock := rootBody.AppendNewBlock("locals", nil)
	localsBlock.Body().SetAttributeRaw("ec2_instance_connect_ip", rawExpr(
		`[
    for e in jsondecode(data.http.ec2_instance_connect.response_body)["prefixes"] : e.ip_prefix if e.region == "${var.aws_region}" && e.service == "EC2_INSTANCE_CONNECT"
  ]`,
	))
	rootBody.AppendNewline()

	// TLS private key + matching local files
	rootBody.AppendBlock(other.GenerateTLSPrivateKeyResource("ssh_key", "RSA", 4096))
	rootBody.AppendNewline()

	rootBody.AppendBlock(other.GenerateLocalFileResource(
		"private_key", "tls_private_key.ssh_key.private_key_pem", "./.ssh/migration_rsa", "400",
	))
	rootBody.AppendNewline()
	rootBody.AppendBlock(other.GenerateLocalFileResource(
		"public_key", "tls_private_key.ssh_key.public_key_openssh", "./.ssh/migration_rsa.pub", "400",
	))
	rootBody.AppendNewline()

	// aws_key_pair.deployer
	keyPairBlock := rootBody.AppendNewBlock("resource", []string{"aws_key_pair", "deployer"})
	keyPairBlock.Body().SetAttributeRaw(
		"key_name",
		utils.TokensForStringTemplate("migration-ssh-key-${random_string.suffix.result}"),
	)
	SetResourceRef(keyPairBlock.Body(), "public_key", "tls_private_key.ssh_key.public_key_openssh")
	rootBody.AppendNewline()

	// data "aws_ami" "amzn_linux_ami"
	rootBody.AppendBlock(aws.GenerateAmiDataResource(
		"amzn_linux_ami",
		"137112412989",
		true,
		map[string]string{
			"name":                "al2023-ami-2023.*-kernel-6.1-x86_64",
			"state":               "available",
			"architecture":        "x86_64",
			"virtualization-type": "hvm",
		},
	))
	rootBody.AppendNewline()

	// IGW: emit either a fresh resource (default) or a data source lookup of
	// the IGW already attached to the VPC.
	if request.HasExistingInternetGateway {
		rootBody.AppendBlock(aws.GenerateInternetGatewayDataSource("internet_gateway", "vpc_id"))
	} else {
		igwBlock := aws.GenerateInternetGatewayResource("internet_gateway", "vpc_id")
		igwBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
			"Name": utils.TokensForStringTemplate("migration-jumpserver-igw"),
		}))
		rootBody.AppendBlock(igwBlock)
	}
	rootBody.AppendNewline()

	// resource "aws_route_table" "public_rt" → gateway_id resolved at gen time
	rtBlock := rootBody.AppendNewBlock("resource", []string{"aws_route_table", "public_rt"})
	SetVarRef(rtBlock.Body(), "vpc_id", "vpc_id")
	routeBlock := rtBlock.Body().AppendNewBlock("route", nil)
	routeBlock.Body().SetAttributeValue("cidr_block", cty.StringVal("0.0.0.0/0"))
	routeBlock.Body().SetAttributeRaw("gateway_id", utils.TokensForResourceReference(
		aws.GetInternetGatewayReference(request.HasExistingInternetGateway, "internet_gateway"),
	))
	rtBlock.Body().AppendNewline()
	rtBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
		"Name": utils.TokensForStringTemplate("migration-bastion-host-public-rt"),
	}))
	rootBody.AppendNewline()

	// data "aws_availability_zones" "available" with opt-in-status filter
	azBlock := rootBody.AppendNewBlock("data", []string{"aws_availability_zones", "available"})
	azBlock.Body().SetAttributeValue("state", cty.StringVal("available"))
	azBlock.Body().AppendNewline()
	azFilter := azBlock.Body().AppendNewBlock("filter", nil)
	azFilter.Body().SetAttributeValue("name", cty.StringVal("opt-in-status"))
	azFilter.Body().SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("opt-in-not-required")}))
	rootBody.AppendNewline()

	// resource "aws_subnet" "public_subnet"
	subnetBlock := rootBody.AppendNewBlock("resource", []string{"aws_subnet", "public_subnet"})
	SetVarRef(subnetBlock.Body(), "vpc_id", "vpc_id")
	SetVarRef(subnetBlock.Body(), "cidr_block", "public_subnet_cidr")
	SetResourceRef(subnetBlock.Body(), "availability_zone", "data.aws_availability_zones.available.names[0]")
	subnetBlock.Body().SetAttributeValue("map_public_ip_on_launch", cty.BoolVal(true))
	subnetBlock.Body().AppendNewline()
	subnetBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
		"Name": utils.TokensForStringTemplate("migration-bastion-host-public-subnet"),
	}))
	rootBody.AppendNewline()

	// route table association
	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource(
		"public_rt_association",
		"aws_subnet.public_subnet",
		"aws_route_table.public_rt.id",
	))
	rootBody.AppendNewline()

	// resource "aws_security_group" "bastion_host_security_group"
	sgBlock := rootBody.AppendNewBlock("resource", []string{"aws_security_group", "bastion_host_security_group"})
	sgBlock.Body().SetAttributeRaw("count", rawExpr("length(var.aws_security_group_ids) == 0 ? 1 : 0"))
	SetVarRef(sgBlock.Body(), "vpc_id", "vpc_id")
	sgBlock.Body().AppendNewline()

	ingress := sgBlock.Body().AppendNewBlock("ingress", nil)
	ingress.Body().SetAttributeValue("from_port", cty.NumberIntVal(0))
	ingress.Body().SetAttributeValue("to_port", cty.NumberIntVal(22))
	ingress.Body().SetAttributeValue("protocol", cty.StringVal("TCP"))
	ingress.Body().SetAttributeRaw("cidr_blocks", listOfTokens(
		utils.TokensForStringTemplate("${local.ec2_instance_connect_ip[0]}"),
	))
	sgBlock.Body().AppendNewline()

	egress := sgBlock.Body().AppendNewBlock("egress", nil)
	egress.Body().SetAttributeValue("from_port", cty.NumberIntVal(0))
	egress.Body().SetAttributeValue("to_port", cty.NumberIntVal(0))
	egress.Body().SetAttributeValue("protocol", cty.StringVal("-1"))
	egress.Body().SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))
	sgBlock.Body().AppendNewline()

	sgBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
		"Name": utils.TokensForStringTemplate("migration-bastion-host-security-group"),
	}))
	rootBody.AppendNewline()

	// resource "aws_instance" "migration_bastion_host"
	instanceBlock := rootBody.AppendNewBlock("resource", []string{"aws_instance", "migration_bastion_host"})
	SetResourceRef(instanceBlock.Body(), "ami", "data.aws_ami.amzn_linux_ami.id")
	instanceBlock.Body().SetAttributeValue("instance_type", cty.StringVal("t2.medium"))
	SetResourceRef(instanceBlock.Body(), "subnet_id", "aws_subnet.public_subnet.id")
	instanceBlock.Body().SetAttributeRaw("vpc_security_group_ids", rawExpr(
		"length(var.aws_security_group_ids) == 0 ? [aws_security_group.bastion_host_security_group[0].id] : var.aws_security_group_ids",
	))
	SetResourceRef(instanceBlock.Body(), "key_name", "aws_key_pair.deployer.key_name")
	instanceBlock.Body().SetAttributeValue("associate_public_ip_address", cty.BoolVal(true))
	instanceBlock.Body().AppendNewline()

	instanceBlock.Body().SetAttributeRaw("user_data", utils.TokensForFunctionCall(
		"templatefile",
		utils.TokensForStringTemplate("${path.module}/bastion-host-user-data.tpl"),
		utils.TokensForMap(map[string]hclwrite.Tokens{}),
	))
	instanceBlock.Body().AppendNewline()

	instanceBlock.Body().SetAttributeValue("user_data_replace_on_change", cty.BoolVal(true))
	instanceBlock.Body().AppendNewline()

	instanceBlock.Body().SetAttributeRaw("tags", utils.TokensForMap(map[string]hclwrite.Tokens{
		"Name": utils.TokensForStringTemplate("kcp-bastion-host-" + s.deploymentDateStamp()),
	}))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

// deploymentDateStamp returns the YYYYMMDD UTC stamp used in the bastion host
// instance Name tag, frozen at HCL generation time so re-applies don't recreate
// the instance.
func (s *BastionHostHCLService) deploymentDateStamp() string {
	now := time.Now
	if s.Now != nil {
		now = s.Now
	}
	return now().UTC().Format("20060102")
}

func (s *BastionHostHCLService) generateProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	requiredProviders := terraformBlock.Body().AppendNewBlock("required_providers", nil).Body()

	// Match the legacy assets/providers.tf provider list (no `random`, no `http` —
	// Terraform auto-installs the latter; both auto-installs are unchanged behaviour).
	requiredProviders.SetAttributeRaw("time", utils.TokensForMap(map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/time"),
		"version": utils.TokensForStringTemplate("0.13.1-alpha1"),
	}))
	requiredProviders.SetAttributeRaw("external", utils.TokensForMap(map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/external"),
		"version": utils.TokensForStringTemplate("2.3.4"),
	}))
	requiredProviders.SetAttributeRaw("tls", utils.TokensForMap(map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/tls"),
		"version": utils.TokensForStringTemplate("4.0.6"),
	}))
	requiredProviders.SetAttributeRaw("local", utils.TokensForMap(map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/local"),
		"version": utils.TokensForStringTemplate("2.4.0"),
	}))
	requiredProviders.SetAttributeRaw(aws.GenerateRequiredProviderTokens())

	rootBody.AppendNewline()
	rootBody.AppendBlock(aws.GenerateProviderBlockWithVarAndDeploymentID(s.DeploymentID))
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (s *BastionHostHCLService) generateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	type variableSpec struct {
		name        string
		typeRef     string
		description string
		defaultVal  *cty.Value
	}

	specs := []variableSpec{
		{name: "vpc_id", typeRef: "string", description: "The ID of the VPC"},
		{name: "public_subnet_cidr", typeRef: "string", description: "CIDR block for the public subnet"},
		{name: "aws_region", typeRef: "string", description: "The AWS region"},
		{name: "aws_security_group_ids", typeRef: "list(string)", description: "List of string of AWS Security Group Ids"},
	}

	for _, spec := range specs {
		varBlock := rootBody.AppendNewBlock("variable", []string{spec.name})
		body := varBlock.Body()
		body.SetAttributeValue("description", cty.StringVal(spec.description))
		body.SetAttributeRaw("type", utils.TokensForResourceReference(spec.typeRef))
		if spec.defaultVal != nil {
			body.SetAttributeValue("default", *spec.defaultVal)
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (s *BastionHostHCLService) generateOutputsTf() string {
	return GenerateOutputsTf([]types.TerraformOutput{
		{Name: "bastion_host_public_ip", Value: "aws_instance.migration_bastion_host.public_ip"},
	})
}

func (s *BastionHostHCLService) generateInputsAutoTfvars(request types.BastionHostRequest) string {
	return GenerateInputsAutoTfvars(map[string]any{
		"aws_region":             request.Region,
		"public_subnet_cidr":     request.PublicSubnetCidr,
		"vpc_id":                 request.VPCId,
		"aws_security_group_ids": request.SecurityGroupIds,
	})
}

// rawExpr emits a single TokenIdent containing the literal expression bytes.
// Use for Terraform expressions hclwrite cannot model directly (ternaries,
// list comprehensions, length()/jsondecode() calls, etc.).
func rawExpr(expr string) hclwrite.Tokens {
	return hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(expr)},
	}
}

// listOfTokens wraps the given token sequence in [ ... ] brackets.
func listOfTokens(inner hclwrite.Tokens) hclwrite.Tokens {
	tokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
	}
	tokens = append(tokens, inner...)
	tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")})
	return tokens
}
