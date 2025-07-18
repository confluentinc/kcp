package utils

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/confluentinc/kcp-internal/internal/types"
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

func ParseTerraformState(targetEnvFolder string) (*types.TerraformState, error) {
	outputJsonFile, err := os.ReadFile(filepath.Join(targetEnvFolder, "terraform.tfstate"))
	if err != nil {
		return nil, err
	}

	var terraformState types.TerraformState
	if err := json.Unmarshal(outputJsonFile, &terraformState); err != nil {
		return nil, fmt.Errorf("failed to unmarshal terraform state: %w", err)
	}

	output := terraformState.Outputs
	if output == (types.TerraformOutput{}) {
		return nil, fmt.Errorf("terraform outputs are missing")
	}

	// Check if any required fields are missing or empty
	if output.ConfluentCloudClusterApiKey.Value == nil || output.ConfluentCloudClusterApiKey.Value == "" ||
		output.ConfluentCloudClusterApiKeySecret.Value == nil || output.ConfluentCloudClusterApiKeySecret.Value == "" ||
		output.ConfluentCloudClusterId.Value == nil || output.ConfluentCloudClusterId.Value == "" ||
		output.ConfluentCloudClusterRestEndpoint.Value == nil || output.ConfluentCloudClusterRestEndpoint.Value == "" ||
		output.ConfluentPlatformControllerBootstrapServer.Value == nil || output.ConfluentPlatformControllerBootstrapServer.Value == "" {

		return nil, fmt.Errorf("terraform outputs are missing or incomplete")
	}

	return &terraformState, nil
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
