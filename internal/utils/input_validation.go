package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// AWSZone represents an AWS availability zone with its CIDR block
type AWSZone struct {
	CIDR string
	Zone string
}

// ValidateAWSZones validates and parses the AWSZones string into a slice of AWSZone structs
// Expected format: "us-east-1a:10.0.0.0/24,us-east-1b:10.0.1.0/24"
func ValidateAWSZones(awsZonesStr string) ([]AWSZone, error) {
	awsZones := []AWSZone{}

	// Validate that AWSZones is not empty
	if awsZonesStr == "" {
		return nil, fmt.Errorf("AWS_ZONES environment variable is required but not set")
	}

	for zone := range strings.SplitSeq(awsZonesStr, ",") {
		// Validate that the zone string contains a colon separator
		if !strings.Contains(zone, ":") {
			return nil, fmt.Errorf("invalid AWS zone format: %s. Expected format: 'zone:cidr' (e.g., 'us-east-1a:10.0.0.0/24')", zone)
		}

		parts := strings.Split(zone, ":")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid AWS zone format: %s. Expected exactly one colon separator", zone)
		}

		awsZones = append(awsZones, AWSZone{
			CIDR: parts[1],
			Zone: parts[0],
		})
	}

	return awsZones, nil
}

func ParseTerraformState(targetEnvFolder string, requiredFields []string) (*types.TerraformState, error) {
	outputJsonFile, err := os.ReadFile(filepath.Join(targetEnvFolder, "terraform.tfstate"))
	if err != nil {
		return nil, err
	}

	var terraformState types.TerraformState
	if err := json.Unmarshal(outputJsonFile, &terraformState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal terraform state: %w", err)
	}

	output := terraformState.Outputs
	if output == (types.TerraformOutputOld{}) {
		return nil, fmt.Errorf("terraform outputs are missing")
	}

	if len(requiredFields) == 0 {
		return &terraformState, nil
	}

	var missingFields []string
	for _, fieldName := range requiredFields {
		getter, exists := terraformOutputGetters[fieldName]
		if !exists {
			return nil, fmt.Errorf("unknown field name for validation: %s", fieldName)
		}

		value := getter(output)
		if value == nil || value == "" {
			missingFields = append(missingFields, fieldName)
		}
	}

	if len(missingFields) > 0 {
		return nil, fmt.Errorf("terraform outputs are missing or incomplete for the following fields: %v", missingFields)
	}

	return &terraformState, nil
}

type TerraformOutputGetter func(types.TerraformOutputOld) any

// terraformOutputGetters maps field names to functions that extract values from TerraformOutput
var terraformOutputGetters = map[string]TerraformOutputGetter{
	"confluent_cloud_cluster_api_key": func(output types.TerraformOutputOld) any {
		return output.ConfluentCloudClusterApiKey.Value
	},
	"confluent_cloud_cluster_api_key_secret": func(output types.TerraformOutputOld) any {
		return output.ConfluentCloudClusterApiKeySecret.Value
	},
	"confluent_cloud_cluster_id": func(output types.TerraformOutputOld) any {
		return output.ConfluentCloudClusterId.Value
	},
	"confluent_cloud_cluster_rest_endpoint": func(output types.TerraformOutputOld) any {
		return output.ConfluentCloudClusterRestEndpoint.Value
	},
	"confluent_cloud_cluster_bootstrap_endpoint": func(output types.TerraformOutputOld) any {
		return output.ConfluentCloudClusterBootstrapEndpoint.Value
	},
	"confluent_platform_controller_bootstrap_server": func(output types.TerraformOutputOld) any {
		return output.ConfluentPlatformControllerBootstrapServer.Value
	},
}

// sets flag values from corresponding environment variables if flags weren't explicitly provided
func BindEnvToFlags(cmd *cobra.Command) error {
	v := viper.New()

	cmd.Flags().VisitAll(func(f *pflag.Flag) {
		flagName := f.Name

		// Convert flag name to environment variable name
		// e.g., "vpc-id" -> "VPC_ID"
		envVarName := strings.ToUpper(strings.ReplaceAll(f.Name, "-", "_"))

		v.BindEnv(flagName, envVarName)

		// If the flag wasn't explicitly set via command line
		// AND
		// there's a value available from environment,
		// THEN
		// set the flag value from the environment
		if !f.Changed && v.IsSet(flagName) {
			val := v.Get(flagName)
			cmd.Flags().Set(f.Name, fmt.Sprintf("%v", val))
		}
	})

	return nil
}
