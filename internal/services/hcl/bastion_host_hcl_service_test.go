package hcl

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
)

func TestBastionHost(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name      string
		createIGW bool
		sgIds     []string
	}{
		{name: "create_igw_no_sg", createIGW: true, sgIds: nil},
		{name: "create_igw_with_sg", createIGW: true, sgIds: []string{"sg-aaa", "sg-bbb"}},
		{name: "existing_igw_no_sg", createIGW: false, sgIds: nil},
		{name: "existing_igw_with_sg", createIGW: false, sgIds: []string{"sg-ccc"}},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			service := &BastionHostHCLService{DeploymentID: "testdeploy"}
			request := types.BastionHostRequest{
				Region:           "us-east-1",
				VPCId:            "vpc-0123456789abcdef0",
				PublicSubnetCidr: "10.0.30.0/24",
				CreateIGW:        tc.createIGW,
				SecurityGroupIds: tc.sgIds,
			}

			files, err := service.GenerateBastionHostFiles(request)
			if err != nil {
				t.Fatal(err)
			}

			fileMap := terraformFilesToMap(files)
			fileMap["bastion-host-user-data.tpl"] = service.GenerateBastionHostUserDataTemplate()

			validateTerraformProject(t, fileMap)
		})
	}
}
