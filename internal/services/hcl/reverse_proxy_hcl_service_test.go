package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestReverseProxy(t *testing.T) {
	service := &ReverseProxyHCLService{DeploymentID: "testdeploy"}
	request := types.ReverseProxyRequest{
		Region:                                 "us-east-1",
		VPCId:                                  "vpc-0123456789abcdef0",
		PublicSubnetCidr:                       "10.0.30.0/24",
		ConfluentCloudClusterBootstrapEndpoint: "pkc-abc123.us-east-1.aws.confluent.cloud:9092",
	}

	files, err := service.GenerateReverseProxyFiles(request)
	if err != nil {
		t.Fatal(err)
	}

	fileMap := terraformFilesToMap(files)

	// Also include the embedded templates
	fileMap["reverse-proxy-user-data.tpl"] = service.GenerateReverseProxyUserDataTemplate()
	fileMap["generate_dns_entries.sh"] = service.GenerateReverseProxyShellScript()

	assertMatchesGoldenFiles(t, "TestReverseProxy", fileMap)
}
