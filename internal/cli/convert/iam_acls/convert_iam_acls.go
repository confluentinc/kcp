package iam_acls

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/client"
	iamservice "github.com/confluentinc/kcp/internal/services/iam"
	"github.com/confluentinc/kcp/internal/types"

	"github.com/spf13/cobra"
)

//go:embed assets
var assetsFS embed.FS

type ACLMapping struct {
	Operation       string
	ResourceType    string
	Description     string
	RequiresPattern bool
}

var aclMap = map[string]ACLMapping{
	// Cluster-level permissions
	"kafka-cluster:Connect": {
		Operation:       "CLUSTER_ACTION",
		ResourceType:    "Cluster",
		Description:     "Allows connecting to the cluster.",
		RequiresPattern: false,
	},
	"kafka-cluster:DescribeCluster": {
		Operation:    "DESCRIBE",
		ResourceType: "Cluster",
		Description:  "Allows retrieving information about the cluster, including brokers and nodes.",

		RequiresPattern: false,
	},
	"kafka-cluster:AlterCluster": {
		Operation:    "ALTER",
		ResourceType: "Cluster",
		Description:  "Allows altering the cluster, such as updating broker counts.",

		RequiresPattern: false,
	},
	"kafka-cluster:DescribeClusterDynamicConfiguration": {
		Operation:    "DESCRIBE_CONFIGS",
		ResourceType: "Cluster",
		Description:  "Allows viewing the dynamic configuration of the cluster.",

		RequiresPattern: false,
	},
	"kafka-cluster:AlterClusterDynamicConfiguration": {
		Operation:    "ALTER_CONFIGS",
		ResourceType: "Cluster",
		Description:  "Allows modifying the dynamic configuration of the cluster.",

		RequiresPattern: false,
	},

	// Topic-level permissions (prefixed with 'kafka-cluster:' in IAM)
	"kafka-cluster:WriteData": {
		Operation:    "WRITE",
		ResourceType: "Topic",
		Description:  "Allows a producer to send messages to a topic.",

		RequiresPattern: true,
	},
	"kafka-cluster:ReadData": {
		Operation:    "READ",
		ResourceType: "Topic",
		Description:  "Allows a consumer to fetch messages from a topic.",

		RequiresPattern: true,
	},
	"kafka-cluster:CreateTopic": {
		Operation:    "CREATE",
		ResourceType: "Topic",
		Description:  "Allows creating a new topic.",

		RequiresPattern: true,
	},
	"kafka-cluster:DeleteTopic": {
		Operation:    "DELETE",
		ResourceType: "Topic",
		Description:  "Allows deleting an existing topic.",

		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopic": {
		Operation:    "DESCRIBE",
		ResourceType: "Topic",
		Description:  "Allows retrieving metadata and configuration for a topic.",

		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopic": {
		Operation:    "ALTER",
		ResourceType: "Topic",
		Description:  "Allows altering topic configurations, like partition counts.",

		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTopicConfig": {
		Operation:    "DESCRIBE_CONFIGS",
		ResourceType: "Topic",
		Description:  "Allows viewing topic-specific configurations.",

		RequiresPattern: true,
	},
	"kafka-cluster:AlterTopicConfig": {
		Operation:    "ALTER_CONFIGS",
		ResourceType: "Topic",
		Description:  "Allows modifying topic-specific configurations.",

		RequiresPattern: true,
	},

	// Group and Transactional ID permissions
	"kafka-cluster:DescribeGroup": {
		Operation:    "DESCRIBE",
		ResourceType: "Group",
		Description:  "Allows describing a consumer group, including its members and offsets.",

		RequiresPattern: true,
	},
	"kafka-cluster:AlterGroup": {
		Operation:    "READ", // Note: AlterGroup maps to READ for consumer group operations like offset commits.
		ResourceType: "Group",
		Description:  "Allows a consumer to commit offsets.",

		RequiresPattern: true,
	},
	"kafka-cluster:DeleteGroup": {
		Operation:    "DELETE",
		ResourceType: "Group",
		Description:  "Allows deleting a consumer group.",

		RequiresPattern: true,
	},
	"kafka-cluster:DescribeTransactionalId": {
		Operation:    "DESCRIBE",
		ResourceType: "TransactionalId",
		Description:  "Allows describing a transactional ID.",

		RequiresPattern: true,
	},
	"kafka-cluster:WriteTransactionalId": {
		Operation:    "WRITE",
		ResourceType: "TransactionalId",
		Description:  "Allows producers to use transactional capabilities.",

		RequiresPattern: true,
	},
}

var (
	roleArn string
	outputDir string
)

func NewConvertIamAclsCmd() *cobra.Command {
	aclsCmd := &cobra.Command{
		Use:   "iam-acls",
		Short: "Convert IAM ACLs to Confluent Cloud IAM ACLs.",
		Long: "Convert IAM ACLs to Confluent Cloud IAM ACLs as individual Terraform resources.",
		
		RunE: func(cmd *cobra.Command, args []string) error {
			roleArn, _ = cmd.Flags().GetString("role-arn")

			return runConvertIamAcls(roleArn)
		},
	}

	aclsCmd.Flags().StringP("role-arn", "r", "", "IAM Role ARN to convert ACLs from (required)")
	aclsCmd.Flags().StringVar(&outputDir, "output-dir", "", "The directory to write the ACL files to")

	aclsCmd.MarkFlagRequired("role-arn")
	aclsCmd.MarkFlagRequired("output-dir")

	return aclsCmd
}

func runConvertIamAcls(roleArn string) error {
	ctx := context.Background()

	fmt.Printf("🚀 Converting IAM ACLs for role: %s\n", roleArn)

	iamClient, err := client.NewIAMClient()
	if err != nil {
		return fmt.Errorf("failed to create IAM client: %v", err)
	}

	fmt.Printf("📋 Retrieving IAM role policies for role: %s\n", roleArn)
	policies, err := iamservice.GetRolePolicies(ctx, iamClient, roleArn)
	if err != nil {
		return fmt.Errorf("failed to get role policies: %v", err)
	}

	extractedACLs, err := extractKafkaPermissions(policies)
	if err != nil {
		return fmt.Errorf("failed to extract Kafka permissions: %v", err)
	}

	if len(extractedACLs) == 0 {
		fmt.Println("No `kafka-cluster` permissions found in the specified role policies.")
		return nil
	}

	fmt.Println(extractedACLs)

	return nil
}

func extractKafkaPermissions(policies *iamservice.RolePolicies) ([]types.Acls, error) {
	var extractedACLs []types.Acls

	for _, policy := range policies.AttachedPolicies {
		fmt.Printf("📝 Processing policy: %s\n", policy.PolicyName)

		for _, statement := range policy.PolicyDocument["Statement"].([]any) {
			statementMap := statement.(map[string]any)

			effect := "ALLOW"
			if effectVal, ok := statementMap["Effect"]; ok {
				effect = strings.ToUpper(effectVal.(string))
			}

			// Handle JSON object of actions.
			var actions []string
			switch actionData := statementMap["Action"].(type) {
			case string:
				actions = append(actions, actionData)
			case []any:
				for _, action := range actionData {
					actions = append(actions, action.(string))
				}
			}

			for _, action := range actions {
				if strings.HasPrefix(action, "kafka-cluster:") {
					mapping, found := TranslateIAMToKafkaACL(action)
					if found {
						acl := createACLFromMapping(mapping, effect)
						extractedACLs = append(extractedACLs, acl)
					} else {
						continue
					}
				}
			}
		}
	}

	return extractedACLs, nil
}

func TranslateIAMToKafkaACL(iamPermission string) (ACLMapping, bool) {
	normalizedPermission := strings.TrimSpace(iamPermission)
	acl, found := aclMap[normalizedPermission]
	return acl, found
}

func createACLFromMapping(mapping ACLMapping, effect string) types.Acls {
	resourceName := "*"
	patternType := "LITERAL"

	switch mapping.ResourceType {
	case "Cluster":
		resourceName = "kafka-cluster"
	}

	return types.Acls{
		ResourceType:        mapping.ResourceType,
		ResourceName:        resourceName,
		ResourcePatternType: patternType,
		Principal:           getPrincipalFromRoleName(),
		Host:                "*",
		Operation:           mapping.Operation,
		PermissionType:      effect,
	}
}

func getPrincipalFromRoleName() string {
	principal := strings.Split(roleArn, "/")[1]

	return fmt.Sprintf("User:%s", principal)
}
