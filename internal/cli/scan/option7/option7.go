package option7

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	credentialsYaml string
	globalTheme     = huh.ThemeCatppuccin()

	// Nord-inspired color styles for each column
	clusterNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("#88C0D0")) // Nord frost blue
	regionStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#A3BE8C")) // Nord green
	arnStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("#D8DEE9")) // Nord snow storm (subtle gray)
	separatorStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#4C566A")) // Nord polar night (dark gray)
)

type clusterTableItem struct {
	key         string
	displayName string
	region      string
	clusterArn  string
	clusterName string
}

func NewScanOption7Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "option7",
		Short:         "Option 7 - Single table view cluster selection with huh",
		Long:          "Single multi-picker table view showing all clusters sorted by region using huh",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption7,
		RunE:          runScanOption7,
	}

	// Required flags
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file")

	cmd.Flags().AddFlagSet(requiredFlags)
	cmd.MarkFlagsOneRequired("credentials-yaml")

	return cmd
}

func preRunScanOption7(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runScanOption7(cmd *cobra.Command, args []string) error {
	// Load and validate credentials
	data, err := os.ReadFile(credentialsYaml)
	if err != nil {
		return fmt.Errorf("failed to read credentials file: %w", err)
	}

	var credsYaml types.CredsYaml
	if err := yaml.Unmarshal(data, &credsYaml); err != nil {
		return fmt.Errorf("failed to unmarshal YAML: %w", err)
	}

	if err := validateCredentials(credsYaml); err != nil {
		return err
	}

	// Show cluster selection and handle results
	selectedClusters, err := showClusterTableSelection(credsYaml)
	if err != nil {
		return fmt.Errorf("cluster selection error: %w", err)
	}

	// Process results
	if len(selectedClusters) == 0 {
		slog.Info("No clusters selected")
		return nil
	}

	// Log selected clusters
	slog.Info("Starting cluster scan", "count", len(selectedClusters))
	for _, item := range selectedClusters {
		slog.Info("Will scan cluster", "cluster", item.clusterArn, "region", item.region, "name", item.clusterName)
	}

	return nil
}

func showClusterTableSelection(credsYaml types.CredsYaml) ([]clusterTableItem, error) {
	var previouslySelectedKeys []string

	for {
		var allClusters []clusterTableItem

		for regionName, region := range credsYaml.Regions {
			for clusterArn, cluster := range region.Clusters {
				key := fmt.Sprintf("%s|%s", regionName, clusterArn)

				clusterNameFormatted := clusterNameStyle.Render(fmt.Sprintf("%-30s", truncateString(cluster.ClusterName, 30)))
				regionFormatted := regionStyle.Render(fmt.Sprintf("%-15s", regionName))
				arnFormatted := arnStyle.Render(clusterArn)
				separator := separatorStyle.Render(" | ")

				displayName := clusterNameFormatted + separator + regionFormatted + separator + arnFormatted

				allClusters = append(allClusters, clusterTableItem{
					key:         key,
					displayName: displayName,
					region:      regionName,
					clusterArn:  clusterArn,
					clusterName: cluster.ClusterName,
				})
			}
		}

		sort.Slice(allClusters, func(i, j int) bool {
			if allClusters[i].region != allClusters[j].region {
				return allClusters[i].region < allClusters[j].region
			}
			return allClusters[i].clusterName < allClusters[j].clusterName
		})

		var clusterOptions []huh.Option[string]
		for _, cluster := range allClusters {
			clusterOptions = append(clusterOptions, huh.NewOption(cluster.displayName, cluster.key))
		}

		var selectedKeys []string = previouslySelectedKeys

		// Create static title and dynamic description
		var titleText string
		if len(allClusters) == 0 {
			titleText = "🌍 MSK Cluster Selection"
		} else {
			totalCount := len(allClusters)
			regionCount := len(credsYaml.Regions)
			titleText = fmt.Sprintf("🌍 MSK Cluster Selection • (%d clusters across %d regions)", totalCount, regionCount)
		}

		// Create initial description that will be updated dynamically
		// descriptionFunc := func() string {
		// 	if len(allClusters) == 0 {
		// 		return "⚠️  No clusters found in the credentials file."
		// 	}
		// 	selectedCount := len(selectedKeys)
		// 	totalCount := len(allClusters)
		// 	return fmt.Sprintf("Selected: [%d/%d]", selectedCount, totalCount)
		// }

		initialDescription := fmt.Sprintf("Selected: [0/%d]", len(allClusters))
		form := huh.NewForm(
			huh.NewGroup(
				huh.NewMultiSelect[string]().
					Title(titleText).
					Description(initialDescription).
					DescriptionFunc(func() string {
						if len(allClusters) == 0 {
							return "⚠️  No clusters found in the credentials file."
						}
						selectedCount := len(selectedKeys)
						totalCount := len(allClusters)
						return fmt.Sprintf("Selected: [%d/%d]", selectedCount, totalCount)
					}, &selectedKeys).
					Options(clusterOptions...).
					Value(&selectedKeys).
					Height(15),
			),
		).WithTheme(globalTheme)

		if err := form.Run(); err != nil {
			return nil, fmt.Errorf("form error: %w", err)
		}

		var selectedClusters []clusterTableItem
		keyToCluster := make(map[string]clusterTableItem)
		for _, cluster := range allClusters {
			keyToCluster[cluster.key] = cluster
		}

		for _, key := range selectedKeys {
			if cluster, exists := keyToCluster[key]; exists {
				selectedClusters = append(selectedClusters, cluster)
			}
		}

		// Always show summary (even if no clusters selected)
		confirmed, err := showSelectionSummary(selectedClusters)
		if err != nil {
			return nil, fmt.Errorf("summary error: %w", err)
		}
		if !confirmed {
			previouslySelectedKeys = selectedKeys
			continue
		}

		return selectedClusters, nil
	}
}

func showSelectionSummary(selectedClusters []clusterTableItem) (bool, error) {
	regionGroups := make(map[string][]clusterTableItem)
	for _, cluster := range selectedClusters {
		regionGroups[cluster.region] = append(regionGroups[cluster.region], cluster)
	}

	var regions []string
	for region := range regionGroups {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	// Build summary text and calculate required height
	summaryText := buildSummaryText(selectedClusters, regionGroups, regions)
	summaryHeight := calculateSummaryHeight(summaryText)

	var shouldProceed bool
	confirmForm := huh.NewForm(
		huh.NewGroup(
			huh.NewNote().
				Title("🎯 Cluster Selection Complete").
				Description(summaryText).
				Height(summaryHeight), // Dynamic height based on content
			huh.NewConfirm().
				Title("Proceed with scanning?").
				Description("Select 'Yes' to start scanning the selected clusters, or 'No' to modify your selection").
				Affirmative("Yes, start scanning").
				Negative("No, modify selection").
				Value(&shouldProceed),
		),
	).WithTheme(globalTheme)

	if err := confirmForm.Run(); err != nil {
		return false, fmt.Errorf("confirmation form error: %w", err)
	}

	return shouldProceed, nil
}

func buildSummaryText(selectedClusters []clusterTableItem, regionGroups map[string][]clusterTableItem, regions []string) string {
	var summaryLines []string

	summaryLines = append(summaryLines, fmt.Sprintf("📋 Selection Summary: %d clusters selected", len(selectedClusters)))
	summaryLines = append(summaryLines, "")

	if len(selectedClusters) == 0 {
		summaryLines = append(summaryLines, "You have included no clusters in the scan.")
		summaryLines = append(summaryLines, "")
	} else {
		for _, region := range regions {
			clusters := regionGroups[region]
			summaryLines = append(summaryLines, fmt.Sprintf("🌍 %s (%d clusters):", region, len(clusters)))
			for _, cluster := range clusters {
				summaryLines = append(summaryLines, fmt.Sprintf("  ✓ %s", cluster.clusterName))
			}
			summaryLines = append(summaryLines, "") // Empty line between regions
		}
		summaryLines = append(summaryLines, "Ready to proceed with scanning these clusters?")
	}

	return strings.Join(summaryLines, "\n")
}

func calculateSummaryHeight(summaryText string) int {
	// Count the number of lines in the summary text
	lines := strings.Split(summaryText, "\n")
	lineCount := len(lines)

	// Add some padding for the UI chrome (title, borders, etc.)
	// Minimum height of 5, maximum reasonable height of 25 to avoid taking over the screen
	minHeight := 5
	maxHeight := 25
	padding := 3

	calculatedHeight := lineCount + padding

	if calculatedHeight < minHeight {
		return minHeight
	}
	if calculatedHeight > maxHeight {
		return maxHeight
	}

	return calculatedHeight
}

func truncateString(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}

func validateCredentials(credsYaml types.CredsYaml) error {
	for regionName, region := range credsYaml.Regions {
		for clusterArn, cluster := range region.Clusters {
			enabled := 0
			if cluster.AuthMethod.Unauthenticated != nil && cluster.AuthMethod.Unauthenticated.Use {
				enabled++
			}
			if cluster.AuthMethod.IAM != nil && cluster.AuthMethod.IAM.Use {
				enabled++
			}
			if cluster.AuthMethod.TLS != nil && cluster.AuthMethod.TLS.Use {
				enabled++
			}
			if cluster.AuthMethod.SASLScram != nil && cluster.AuthMethod.SASLScram.Use {
				enabled++
			}

			if enabled != 1 {
				return fmt.Errorf("exactly one auth method must be enabled for cluster %s in region %s, found %d", clusterArn, regionName, enabled)
			}
		}
	}
	return nil
}
