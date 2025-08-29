package brokers

import (
	"fmt"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	discoverDir     string
	credentialsYaml string
)

func NewScanBrokersCmd() *cobra.Command {
	brokersCmd := &cobra.Command{
		Use:   "brokers",
		Short: "Scan brokers using the Kafka Admin API",
		Long:  "Scan brokers for information using the Kafka Admin API such as topics, ACLs and cluster ID",
		SilenceErrors: true,
		PreRunE:       preRunScanBrokers,
		RunE:          runScanBrokers,
	}

	groups := map[*pflag.FlagSet]string{}

	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&discoverDir, "discover-dir", "", "The path to the directory where the MSK cluster discovery reports have been written to.")
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file used for authenticating to the MSK cluster(s).")
	brokersCmd.Flags().AddFlagSet(requiredFlags)
	groups[requiredFlags] = "Required Flags"

	brokersCmd.SetUsageFunc(func(c *cobra.Command) error {
		fmt.Printf("%s\n\n", c.Short)

		flagOrder := []*pflag.FlagSet{requiredFlags}
		groupNames := []string{"Required Flags"}

		for i, fs := range flagOrder {
			usage := fs.FlagUsages()
			if usage != "" {
				fmt.Printf("%s:\n%s\n", groupNames[i], usage)
			}
		}

		fmt.Println("All flags can be provided via environment variables (uppercase, with underscores).")

		return nil
	})

	return brokersCmd
}

func preRunScanBrokers(cmd *cobra.Command, args []string) error {


	return nil
}

func runScanBrokers(cmd *cobra.Command, args []string) error {

	return nil
}