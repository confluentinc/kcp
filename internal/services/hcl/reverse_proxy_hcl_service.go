package hcl

import (
	_ "embed"
	"strings"

	"github.com/confluentinc/kcp/internal/services/hcl/aws"
	"github.com/confluentinc/kcp/internal/services/hcl/other"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
	"github.com/zclconf/go-cty/cty"
)

//go:embed reverse_proxy/generate_dns_entries.sh
var generateDnsEntriesScript string

//go:embed reverse_proxy/reverse-proxy-user-data.tpl
var reverseProxyUserDataTpl string

type ReverseProxyHCLService struct {
}

func NewReverseProxyHCLService() *ReverseProxyHCLService {
	return &ReverseProxyHCLService{}
}

func (s *ReverseProxyHCLService) GenerateReverseProxyFiles(request types.ReverseProxyRequest) (types.TerraformFiles, error) {
	return types.TerraformFiles{
		MainTf:           s.generateMainTf(request),
		ProvidersTf:      s.generateProvidersTf(),
		VariablesTf:      s.generateVariablesTf(),
		InputsAutoTfvars: s.generateInputsAutoTfvars(request),
	}, nil
}

func (s *ReverseProxyHCLService) GenerateReverseProxyUserDataTemplate() string {
	return reverseProxyUserDataTpl
}

func (s *ReverseProxyHCLService) GenerateReverseProxyShellScript() string {
	return generateDnsEntriesScript
}

func (s *ReverseProxyHCLService) generateMainTf(request types.ReverseProxyRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	// Locals block
	localsBlock := rootBody.AppendNewBlock("locals", nil)
	localsBody := localsBlock.Body()
	localsBody.AppendUnstructuredTokens(utils.TokensForComment("# Extract the hostname from the bootstrap endpoint\n"))
	// Use function call tokens for regex expression: regex("(.*):", var.confluent_cloud_cluster_bootstrap_endpoint)[0]
	regexPattern := cty.StringVal("(.*):")
	regexTokens := utils.TokensForFunctionCall(
		"regex",
		hclwrite.TokensForValue(regexPattern),
		utils.TokensForVarReference("confluent_cloud_cluster_bootstrap_endpoint"),
	)
	// Append [0] to get first element
	indexTokens := hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOBrack, Bytes: []byte("[")},
		&hclwrite.Token{Type: hclsyntax.TokenNumberLit, Bytes: []byte("0")},
		&hclwrite.Token{Type: hclsyntax.TokenCBrack, Bytes: []byte("]")},
	}
	localsBody.SetAttributeRaw("cluster_hostname", append(regexTokens, indexTokens...))
	rootBody.AppendNewline()

	// Random string
	rootBody.AppendBlock(other.GenerateRandomStringResource("suffix", 4, false, false, false))
	rootBody.AppendNewline()

	// TLS private key
	rootBody.AppendBlock(other.GenerateTLSPrivateKeyResource("ssh_key", "RSA", 4096))
	rootBody.AppendNewline()

	// Local files for SSH keys
	rootBody.AppendBlock(other.GenerateLocalFileResource("private_key", "tls_private_key.ssh_key.private_key_pem", "./.ssh/reverse_proxy_rsa", "400"))
	rootBody.AppendNewline()
	rootBody.AppendBlock(other.GenerateLocalFileResource("public_key", "tls_private_key.ssh_key.public_key_openssh", "./.ssh/reverse_proxy_rsa.pub", "400"))
	rootBody.AppendNewline()

	// AWS key pair
	// Key name uses interpolation: "reverse-proxy-ssh-key-${random_string.suffix.result}"
	keyPairNameStr := "reverse-proxy-ssh-key-${random_string.suffix.result}"
	keyPairNameTokens := utils.TokensForStringTemplate(keyPairNameStr)
	keyPairBlock := rootBody.AppendNewBlock("resource", []string{"aws_key_pair", "deployer"})
	keyPairBody := keyPairBlock.Body()
	keyPairBody.SetAttributeRaw("key_name", keyPairNameTokens)
	keyPairBody.SetAttributeRaw("public_key", utils.TokensForResourceReference("tls_private_key.ssh_key.public_key_openssh"))
	rootBody.AppendNewline()

	// Availability zones data source
	rootBody.AppendBlock(aws.GenerateAvailabilityZonesDataSource("available"))
	rootBody.AppendNewline()

	// Internet gateway data source
	igwBlock := rootBody.AppendNewBlock("data", []string{"aws_internet_gateway", "existing_internet_gateway"})
	igwBody := igwBlock.Body()
	filterBlock := igwBody.AppendNewBlock("filter", nil)
	filterBody := filterBlock.Body()
	filterBody.SetAttributeValue("name", cty.StringVal("attachment.vpc-id"))
	filterBody.SetAttributeValue("values", cty.ListVal([]cty.Value{cty.StringVal("var.vpc_id")}))
	rootBody.AppendNewline()

	// Route table
	rtBlock := rootBody.AppendNewBlock("resource", []string{"aws_route_table", "public_rt"})
	rtBody := rtBlock.Body()
	rtBody.SetAttributeRaw("vpc_id", utils.TokensForVarReference("vpc_id"))
	routeBlock := rtBody.AppendNewBlock("route", nil)
	routeBody := routeBlock.Body()
	routeBody.SetAttributeValue("cidr_block", cty.StringVal("0.0.0.0/0"))
	routeBody.SetAttributeRaw("gateway_id", utils.TokensForResourceReference("data.aws_internet_gateway.existing_internet_gateway.id"))
	rootBody.AppendNewline()

	// Subnet
	subnetBlock := rootBody.AppendNewBlock("resource", []string{"aws_subnet", "public_subnet"})
	subnetBody := subnetBlock.Body()
	subnetBody.SetAttributeRaw("vpc_id", utils.TokensForVarReference("vpc_id"))
	subnetBody.SetAttributeRaw("cidr_block", utils.TokensForVarReference("public_subnet_cidr"))
	subnetBody.SetAttributeRaw("availability_zone", utils.TokensForResourceReference("data.aws_availability_zones.available.names[0]"))
	subnetBody.SetAttributeValue("map_public_ip_on_launch", cty.BoolVal(true))
	rootBody.AppendNewline()

	// Route table association
	rootBody.AppendBlock(aws.GenerateRouteTableAssociationResource("public_rt_association", "aws_subnet.public_subnet.id", "aws_route_table.public_rt.id"))
	rootBody.AppendNewline()

	// Security group
	securityGroupBlock := rootBody.AppendNewBlock("resource", []string{"aws_security_group", "public"})
	securityGroupBody := securityGroupBlock.Body()
	securityGroupBody.SetAttributeRaw("vpc_id", utils.TokensForVarReference("vpc_id"))
	securityGroupBody.AppendNewline()

	// Ingress rules
	for _, port := range []int{22, 443, 9092} {
		ingressBlock := securityGroupBody.AppendNewBlock("ingress", nil)
		ingressBody := ingressBlock.Body()
		ingressBody.SetAttributeValue("from_port", cty.NumberIntVal(int64(port)))
		ingressBody.SetAttributeValue("to_port", cty.NumberIntVal(int64(port)))
		ingressBody.SetAttributeValue("protocol", cty.StringVal("tcp"))
		ingressBody.SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))
		securityGroupBody.AppendNewline()
	}

	// Egress rule
	egressBlock := securityGroupBody.AppendNewBlock("egress", nil)
	egressBody := egressBlock.Body()
	egressBody.SetAttributeValue("from_port", cty.NumberIntVal(0))
	egressBody.SetAttributeValue("to_port", cty.NumberIntVal(0))
	egressBody.SetAttributeValue("protocol", cty.StringVal("-1"))
	egressBody.SetAttributeValue("cidr_blocks", cty.ListVal([]cty.Value{cty.StringVal("0.0.0.0/0")}))
	securityGroupBody.AppendNewline()
	rootBody.AppendNewline()

	// AMI data source
	amiFilters := map[string]string{
		"name":                "ubuntu/images/hvm-ssd/ubuntu-*-amd64-server-*",
		"state":               "available",
		"architecture":        "x86_64",
		"virtualization-type": "hvm",
	}
	rootBody.AppendBlock(aws.GenerateAmiDataResource("ubuntu_ami", "099720109477", true, amiFilters))
	rootBody.AppendNewline()

	// EC2 instance with provisioner
	provisioner := &aws.ProvisionerConfig{
		Type:       "local-exec",
		When:       "create",
		OnFailure:  "continue",
		Command:    "bash ./generate_dns_entries.sh ${self.public_ip} ${local.cluster_hostname}",
		WorkingDir: "path.module",
	}

	// Create instance block
	instanceBlock := rootBody.AppendNewBlock("resource", []string{"aws_instance", "proxy"})
	instanceBody := instanceBlock.Body()
	instanceBody.SetAttributeRaw("ami", utils.TokensForResourceReference("data.aws_ami.ubuntu_ami.id"))
	instanceBody.SetAttributeValue("instance_type", cty.StringVal("t2.micro"))
	instanceBody.SetAttributeRaw("subnet_id", utils.TokensForResourceReference("aws_subnet.public_subnet.id"))
	instanceBody.SetAttributeRaw("vpc_security_group_ids", utils.TokensForStringList([]string{"aws_security_group.public.id"}))

	instanceBody.SetAttributeRaw("key_name", utils.TokensForResourceReference("aws_key_pair.deployer.key_name"))
	instanceBody.SetAttributeValue("associate_public_ip_address", cty.BoolVal(true))
	instanceBody.AppendNewline()

	// User data
	userDataTokens := utils.TokensForFunctionCall(
		"templatefile",
		utils.TokensForStringTemplate("${path.module}/reverse-proxy-user-data.tpl"),
		utils.TokensForMap(map[string]hclwrite.Tokens{}),
	)
	instanceBody.SetAttributeRaw("user_data", userDataTokens)
	instanceBody.AppendNewline()

	// Provisioner
	provisionerBlock := instanceBody.AppendNewBlock("provisioner", []string{provisioner.Type})
	provisionerBody := provisionerBlock.Body()
	if provisioner.When != "" {
		// Use raw tokens for keywords (when and on_failure should not be quoted)
		provisionerBody.SetAttributeRaw("when", hclwrite.Tokens{
			&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(provisioner.When)},
		})
	}
	if provisioner.OnFailure != "" {
		// Use raw tokens for keywords (when and on_failure should not be quoted)
		provisionerBody.SetAttributeRaw("on_failure", hclwrite.Tokens{
			&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte(provisioner.OnFailure)},
		})
	}
	if provisioner.Command != "" {
		// Use heredoc format for command
		commandLines := strings.Split(provisioner.Command, "\n")
		tokens := hclwrite.Tokens{
			&hclwrite.Token{Type: hclsyntax.TokenOHeredoc, Bytes: []byte("<<-")},
			&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("EOF")},
			&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		}
		for _, line := range commandLines {
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenQuotedLit, Bytes: []byte("      " + line)})
			tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")})
		}
		tokens = append(tokens, &hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("    EOF")})
		provisionerBody.SetAttributeRaw("command", tokens)
	}
	if provisioner.WorkingDir != "" {
		provisionerBody.SetAttributeRaw("working_dir", utils.TokensForResourceReference(provisioner.WorkingDir))
	}
	instanceBody.AppendNewline()
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (s *ReverseProxyHCLService) generateProvidersTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	terraformBlock := rootBody.AppendNewBlock("terraform", nil)
	terraformBody := terraformBlock.Body()

	requiredProvidersBlock := terraformBody.AppendNewBlock("required_providers", nil)
	requiredProvidersBody := requiredProvidersBlock.Body()

	// AWS provider
	requiredProvidersBody.SetAttributeRaw(aws.GenerateRequiredProviderTokens())

	// TLS provider
	tlsProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/tls"),
		"version": utils.TokensForStringTemplate("4.0.6"),
	}
	requiredProvidersBody.SetAttributeRaw("tls", utils.TokensForMap(tlsProvider))

	// Local provider
	localProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/local"),
		"version": utils.TokensForStringTemplate("2.4.0"),
	}
	requiredProvidersBody.SetAttributeRaw("local", utils.TokensForMap(localProvider))

	// Random provider
	randomProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/random"),
		"version": utils.TokensForStringTemplate("3.7.2"),
	}
	requiredProvidersBody.SetAttributeRaw("random", utils.TokensForMap(randomProvider))

	// Time provider
	timeProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/time"),
		"version": utils.TokensForStringTemplate("0.13.1-alpha1"),
	}
	requiredProvidersBody.SetAttributeRaw("time", utils.TokensForMap(timeProvider))

	// External provider
	externalProvider := map[string]hclwrite.Tokens{
		"source":  utils.TokensForStringTemplate("hashicorp/external"),
		"version": utils.TokensForStringTemplate("2.3.4"),
	}
	requiredProvidersBody.SetAttributeRaw("external", utils.TokensForMap(externalProvider))

	rootBody.AppendNewline()

	// AWS provider block
	rootBody.AppendBlock(aws.GenerateProviderBlockWithVar())
	rootBody.AppendNewline()

	return string(f.Bytes())
}

func (s *ReverseProxyHCLService) generateVariablesTf() string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	variables := []types.TerraformVariable{
		{Name: "vpc_id", Description: "The ID of the VPC", Type: "string", Sensitive: false},
		{Name: "public_subnet_cidr", Description: "CIDR block for the public subnet", Type: "string", Sensitive: false},
		{Name: "confluent_cloud_cluster_bootstrap_endpoint", Description: "The bootstrap endpoint of the Confluent cluster", Type: "string", Sensitive: false},
		{Name: "aws_region", Description: "AWS Region", Type: "string", Sensitive: false},
	}

	for _, v := range variables {
		variableBlock := rootBody.AppendNewBlock("variable", []string{v.Name})
		variableBody := variableBlock.Body()
		variableBody.SetAttributeRaw("type", utils.TokensForResourceReference(v.Type))
		if v.Description != "" {
			variableBody.SetAttributeValue("description", cty.StringVal(v.Description))
		}
		if v.Sensitive {
			variableBody.SetAttributeValue("sensitive", cty.BoolVal(true))
		}
		// Add default for public_subnet_cidr and aws_region
		if v.Name == "public_subnet_cidr" {
			variableBody.SetAttributeValue("default", cty.StringVal("10.0.30.0/24"))
		}
		if v.Name == "aws_region" {
			variableBody.SetAttributeValue("default", cty.StringVal("us-east-1"))
		}
		rootBody.AppendNewline()
	}

	return string(f.Bytes())
}

func (s *ReverseProxyHCLService) generateInputsAutoTfvars(request types.ReverseProxyRequest) string {
	f := hclwrite.NewEmptyFile()
	rootBody := f.Body()

	rootBody.SetAttributeValue("aws_region", cty.StringVal(request.Region))
	rootBody.SetAttributeValue("public_subnet_cidr", cty.StringVal(request.PublicSubnetCidr))
	rootBody.SetAttributeValue("vpc_id", cty.StringVal(request.VPCId))
	rootBody.SetAttributeValue("confluent_cloud_cluster_bootstrap_endpoint", cty.StringVal(request.ConfluentCloudClusterBootstrapEndpoint))

	return string(f.Bytes())
}
