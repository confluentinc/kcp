package confluent

import (
	"fmt"

	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

func GenerateClusterLinkLocals(ccClusterKeyVarName, ccClusterSecretVarName string) *hclwrite.Block {
	localsBlock := hclwrite.NewBlock("locals", nil)

	basicAuthTokens := utils.TokensForFunctionCall(
		"base64encode",
		utils.TokensForStringTemplate(fmt.Sprintf("${var.%s}:${var.%s}", ccClusterKeyVarName, ccClusterSecretVarName)),
	)
	localsBlock.Body().SetAttributeRaw("basic_auth_credentials", basicAuthTokens)

	return localsBlock
}

/*
Blocked from using the actual Terraform cluster link resource because it only supports the 'PLAIN' SASL mechanism. MSK's
SASL/SCRAM only supports the 'SCRAM-SHA-512' mechanism.

Error: error creating Cluster Link: 401 Unauthorized: Unable to validate cluster link due to error: Client SASL mechanism
'PLAIN' not enabled in the server, enabled mechanisms are [SCRAM-SHA-512]
*/
func GenerateClusterLinkResource(tfResourceName, mskClusterIdVarName, targetClusterIdVarName, targetClusterRestEndpointVarName, clusterLinkNameVarName, mskSaslScramBootstrapServersVarName, mskSaslScramUsernameVarName, mskSaslScramPasswordVarName string) *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"null_resource", tfResourceName})

	triggersMap := map[string]hclwrite.Tokens{
		"source_cluster_id":            utils.TokensForVarReference(mskClusterIdVarName),
		"destination_cluster_id":       utils.TokensForVarReference(targetClusterIdVarName),
		"bootstrap_servers":            utils.TokensForVarReference(mskSaslScramBootstrapServersVarName),
		"basic_auth_credentials":       utils.TokensForResourceReference("local.basic_auth_credentials"),
		"target_cluster_rest_endpoint": utils.TokensForVarReference(targetClusterRestEndpointVarName),
		"link_name":                    utils.TokensForVarReference(clusterLinkNameVarName),
		"msk_sasl_scram_username":      utils.TokensForVarReference(mskSaslScramUsernameVarName),
		"msk_sasl_scram_password":      utils.TokensForVarReference(mskSaslScramPasswordVarName),
	}
	resourceBlock.Body().SetAttributeRaw("triggers", utils.TokensForMap(triggersMap))

	resourceBlock.Body().AppendNewline()

	provisionerBlock := resourceBlock.Body().AppendNewBlock("provisioner", []string{"local-exec"})

	// Generate curl command using triggers map
	curlCommand := generateCreateClusterLinkCurlCommand(triggersMap)

	provisionerBlock.Body().SetAttributeRaw("command", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOHeredoc, Bytes: []byte("<<-EOT")},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		&hclwrite.Token{Type: hclsyntax.TokenStringLit, Bytes: []byte(curlCommand)},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		&hclwrite.Token{Type: hclsyntax.TokenCHeredoc, Bytes: []byte("EOT")},
	})

	resourceBlock.Body().AppendNewline()

	// Destroy provisioner
	destroyProvisionerBlock := resourceBlock.Body().AppendNewBlock("provisioner", []string{"local-exec"})
	destroyProvisionerBlock.Body().SetAttributeRaw("when", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenIdent, Bytes: []byte("destroy")},
	})

	destroyCurlCommand := generateDeleteClusterLinkCurlCommandForDestroy()

	destroyProvisionerBlock.Body().SetAttributeRaw("command", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOHeredoc, Bytes: []byte("<<-EOT")},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		&hclwrite.Token{Type: hclsyntax.TokenStringLit, Bytes: []byte(destroyCurlCommand)},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		&hclwrite.Token{Type: hclsyntax.TokenCHeredoc, Bytes: []byte("EOT")},
	})

	return resourceBlock
}

// generateCreateClusterLinkCurlCommand generates a curl command using trigger references
func generateCreateClusterLinkCurlCommand(triggersMap map[string]hclwrite.Tokens) string {
	return `curl --request POST \
  --url '${self.triggers.target_cluster_rest_endpoint}/kafka/v3/clusters/${self.triggers.destination_cluster_id}/links/?link_name=${self.triggers.link_name}' \
  --header 'Authorization: Basic ${self.triggers.basic_auth_credentials}' \
  --header "Content-Type: application/json" \
  --data '{
    "source_cluster_id": "${self.triggers.source_cluster_id}",
    "configs": [
      {
        "name": "bootstrap.servers",
        "value": "${self.triggers.bootstrap_servers}"
      },
      {
        "name": "link.mode",
        "value": "DESTINATION"
      },
      {
        "name": "security.protocol",
        "value": "SASL_SSL"
      },
      {
        "name": "sasl.mechanism",
        "value": "SCRAM-SHA-512"
      },
      {
        "name": "sasl.jaas.config",
        "value": "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"${self.triggers.msk_sasl_scram_username}\" password=\"${self.triggers.msk_sasl_scram_password}\";"
      }
    ]
  }'`
}

func generateDeleteClusterLinkCurlCommandForDestroy() string {
	return `curl --request DELETE \
  --url '${self.triggers.target_cluster_rest_endpoint}/kafka/v3/clusters/${self.triggers.destination_cluster_id}/links/${self.triggers.link_name}' \
  --header 'Authorization: Basic ${self.triggers.basic_auth_credentials}'`
}
