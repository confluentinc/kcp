package confluent

import (
	"bytes"
	"fmt"
	"math/rand"
	"text/template"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/hashicorp/hcl/v2/hclsyntax"
	"github.com/hashicorp/hcl/v2/hclwrite"
)

type ClusterLinkTemplateData struct {
	TargetClusterRestEndpoint string
	TargetClusterId           string
	LinkName                  string
	BasicAuthCredentials      string
	SourceClusterId           string
	SourceBootstrapServers    string
	SaslUsername              string
	SaslPassword              string
}

func generateRandomSuffix() string {
	const letters = "abcdefghijklmnopqrstuvwxyz"
	suffix := make([]byte, 4)
	for i := range suffix {
		suffix[i] = letters[rand.Intn(len(letters))]
	}

	return string(suffix)
}

func GenerateClusterLinkLocals() *hclwrite.Block {
	localsBlock := hclwrite.NewBlock("locals", nil)

	basicAuthTokens := utils.TokensForFunctionCall(
		"base64encode",
		"${var.confluent_cloud_cluster_api_key}:${var.confluent_cloud_cluster_api_secret}",
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
func GenerateClusterLinkResource(request types.MigrationWizardRequest) *hclwrite.Block {
	resourceBlock := hclwrite.NewBlock("resource", []string{"null_resource", "confluent_cluster_link"})

	triggersMap := map[string]hclwrite.Tokens{
		"source_cluster_id":      utils.TokensForStringTemplate(request.MskClusterId),
		"destination_cluster_id": utils.TokensForStringTemplate(request.TargetClusterId),
		"bootstrap_servers":      utils.TokensForStringTemplate(request.MskSaslScramBootstrapServers),
	}
	resourceBlock.Body().SetAttributeRaw("triggers", utils.TokensForMap(triggersMap))

	resourceBlock.Body().AppendNewline()

	provisionerBlock := resourceBlock.Body().AppendNewBlock("provisioner", []string{"local-exec"})

	// Template data with Terraform variable references
	templateData := ClusterLinkTemplateData{
		TargetClusterRestEndpoint: request.TargetRestEndpoint,
		TargetClusterId:           request.TargetClusterId,
		LinkName:                  request.ClusterLinkName,
		SourceClusterId:           request.MskClusterId,
		SourceBootstrapServers:    request.MskSaslScramBootstrapServers,
	}

	curlCommand := generateClusterLinkCurlCommand(templateData)

	provisionerBlock.Body().SetAttributeRaw("command", hclwrite.Tokens{
		&hclwrite.Token{Type: hclsyntax.TokenOHeredoc, Bytes: []byte("<<-EOT")},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		&hclwrite.Token{Type: hclsyntax.TokenStringLit, Bytes: []byte(curlCommand)},
		&hclwrite.Token{Type: hclsyntax.TokenNewline, Bytes: []byte("\n")},
		&hclwrite.Token{Type: hclsyntax.TokenCHeredoc, Bytes: []byte("EOT")},
	})

	return resourceBlock
}

// generateClusterLinkCurlCommand generates a curl command from template data
func generateClusterLinkCurlCommand(data ClusterLinkTemplateData) string {
	tmplStr := `curl --request POST \
  --url '{{.TargetClusterRestEndpoint}}/kafka/v3/clusters/{{.TargetClusterId}}/links/?link_name={{.LinkName}}' \
  --header 'Authorization: Basic ${local.basic_auth_credentials}' \
  --header "Content-Type: application/json" \
  --data '{
    "source_cluster_id": "{{.SourceClusterId}}",
    "configs": [
      {
        "name": "bootstrap.servers",
        "value": "{{.SourceBootstrapServers}}"
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
        "value": "org.apache.kafka.common.security.scram.ScramLoginModule required username=\"${var.msk_sasl_scram_username}\" password=\"${var.msk_sasl_scram_password}\";"
      }
    ]
  }'`

	tmpl := template.Must(template.New("cluster_link").Parse(tmplStr))

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return fmt.Sprintf("error generating cluster link command: %v", err)
	}

	return buf.String()
}
