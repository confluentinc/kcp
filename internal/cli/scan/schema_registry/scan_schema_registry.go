package schema_registry

import (
	"fmt"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/services/schema_registry"
	sr "github.com/confluentinc/kcp/internal/generators/scan/schema_registry"
)

var (
	schemaRegistryURL string
	useUnauthenticated bool
	useBasicAuth bool
	username string
	password string
	groups = make(map[*pflag.FlagSet]string)
)

func NewScanSchemaRegistryCmd() *cobra.Command {
	schemaRegistryCmd := &cobra.Command{
		Use:           "schema-registry",
		Short:         "Scan Schema Registry and export all schemas",
		Long:          "Scan Schema Registry to discover all schemas, subjects, and versions, then export to JSON",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		RunE:          runScanSchemaRegistry,
	}

	// Required flags.
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&schemaRegistryURL, "url", "", "Schema Registry URL")
	schemaRegistryCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	// Authentication flags.
	authFlags := pflag.NewFlagSet("auth", pflag.ExitOnError)
	authFlags.SortFlags = false
	authFlags.BoolVar(&useUnauthenticated, "use-unauthenticated", false, "Use Unauthenticated Authentication")
	authFlags.BoolVar(&useBasicAuth, "use-basic-auth", false, "Use Basic Authentication")
	authFlags.StringVar(&username, "username", "", "Username for Basic Authentication")
	authFlags.StringVar(&password, "password", "", "Password for Basic Authentication")
	schemaRegistryCmd.Flags().AddFlagSet(authFlags)
	groups[authFlags] = "Authentication Flags"

	schemaRegistryCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags, authFlags}
		groupNames := []string{"Required Flags", "Authentication Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	schemaRegistryCmd.MarkFlagRequired("url")
	schemaRegistryCmd.MarkFlagsMutuallyExclusive("use-unauthenticated", "use-basic-auth")
	schemaRegistryCmd.MarkFlagsOneRequired("use-unauthenticated", "use-basic-auth")
	
	return schemaRegistryCmd
}
// }

func runScanSchemaRegistry(cmd *cobra.Command, args []string) error {
	var config client.SchemaRegistryConfig
	
	switch {
	case useUnauthenticated:
		config = client.SchemaRegistryConfig{URL: schemaRegistryURL}
	case useBasicAuth:
		config = client.SchemaRegistryConfig{
			URL:      schemaRegistryURL,
			Username: username,
			Password: password,
		}
	default: 
		return fmt.Errorf("❌ Authentication type not supported")
	}

	fmt.Printf("Using URL: %s, Username: %s, Password: %s\n", config.URL, config.Username, config.Password)

	schemaRegistryClient, err := client.NewSchemaRegistryClient(config)
	if err != nil {
		return fmt.Errorf("❌ Failed to create schema registry client: %v", err)
	}

	service := schema_registry.NewSchemaRegistryService(schemaRegistryClient)

	scanner := sr.NewSchemaRegistryScanner(service, schemaRegistryURL)
	
	return scanner.Run()
}
