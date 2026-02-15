package list

import (
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/fatih/color"
)

type MigrationListerOpts struct {
	MigrationStateFile string
	MigrationState     types.MigrationState
}

type MigrationLister struct {
	migrationStateFile string
	migrationState     types.MigrationState
}

func NewMigrationLister(opts MigrationListerOpts) *MigrationLister {
	return &MigrationLister{
		migrationStateFile: opts.MigrationStateFile,
		migrationState:     opts.MigrationState,
	}
}

func (ml *MigrationLister) Run() error {
	migrations := ml.migrationState.Migrations

	if len(migrations) == 0 {
		fmt.Printf("\n%s No migrations found in %s\n\n", color.YellowString("ℹ"), ml.migrationStateFile)
		fmt.Printf("Run %s to create a new migration.\n\n", color.CyanString("kcp migration init"))
		return nil
	}

	// Get file info for last updated timestamp
	fileInfo, err := os.Stat(ml.migrationStateFile)
	var lastUpdated string
	if err == nil {
		lastUpdated = fileInfo.ModTime().Format("2006-01-02 15:04:05")
	} else {
		lastUpdated = "unknown"
	}

	// Print header
	fmt.Printf("\n%s %s\n", color.CyanString("Migration State:"), color.WhiteString(ml.migrationStateFile))
	fmt.Printf("%s %s\n\n", color.CyanString("Last Updated:"), color.WhiteString(lastUpdated))
	fmt.Printf("%s\n\n", color.CyanString("Migrations (%d):", len(migrations)))

	// Sort migrations by creation time (newest first)
	// We'll use the migration ID timestamp if available, otherwise just reverse order
	sortedMigrations := make([]types.Migration, len(migrations))
	copy(sortedMigrations, migrations)
	sort.SliceStable(sortedMigrations, func(i, j int) bool {
		// Reverse order (newest first)
		return i > j
	})

	// Display each migration
	for idx, migration := range sortedMigrations {
		ml.displayMigration(idx+1, migration)
	}

	return nil
}

func (ml *MigrationLister) displayMigration(index int, migration types.Migration) {
	// Index and Migration ID
	fmt.Printf("%s %s %s\n",
		color.HiBlackString("[%d]", index),
		color.HiBlackString("Migration ID:"),
		color.New(color.Bold, color.FgWhite).Sprint(migration.MigrationId))

	// Status with color coding
	statusColor := ml.getStatusColor(migration.CurrentState)
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Status:"),
		statusColor.Sprint(migration.CurrentState))

	// Gateway
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Gateway:"),
		color.WhiteString("%s/%s", migration.GatewayNamespace, migration.GatewayCrdName))

	// Source
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Source:"),
		color.WhiteString(migration.SourceName))

	// Destination
	if migration.DestinationName != "" {
		fmt.Printf("    %s %s\n",
			color.HiBlackString("Destination:"),
			color.WhiteString(migration.DestinationName))
	}

	// Source Route
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Source Route:"),
		color.WhiteString(migration.SourceRouteName))

	// Cluster Link
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Cluster Link:"),
		color.WhiteString(migration.ClusterLinkName))

	// Topics - display all topic names with word wrapping
	ml.displayTopics(migration.Topics)

	fmt.Println() // Blank line between migrations
}

func (ml *MigrationLister) displayTopics(topics []string) {
	if len(topics) == 0 {
		fmt.Printf("    %s %s\n",
			color.HiBlackString("Topics:"),
			color.HiBlackString("(none)"))
		return
	}

	// Display topic count and names
	topicsLabel := fmt.Sprintf("Topics (%d):", len(topics))
	fmt.Printf("    %s ", color.HiBlackString(topicsLabel))

	// Join topics with commas
	topicsStr := strings.Join(topics, ", ")

	// Word wrap at 80 characters, accounting for the indent
	const maxLineLength = 80
	const indent = "                   " // Align with the label
	remainingLength := maxLineLength - len("    "+topicsLabel) - 1

	// If it fits on one line, just print it
	if len(topicsStr) <= remainingLength {
		fmt.Printf("%s\n", color.WhiteString(topicsStr))
		return
	}

	// Otherwise, wrap intelligently at comma boundaries
	words := strings.Split(topicsStr, ", ")
	currentLine := ""
	firstLine := true

	for i, word := range words {
		testLine := currentLine
		if testLine != "" {
			testLine += ", "
		}
		testLine += word

		maxLen := remainingLength
		if !firstLine {
			maxLen = maxLineLength - len(indent)
		}

		if len(testLine) > maxLen && currentLine != "" {
			// Print current line and start new one
			if firstLine {
				fmt.Printf("%s\n", color.WhiteString(currentLine+","))
				firstLine = false
			} else {
				fmt.Printf("%s%s\n", indent, color.WhiteString(currentLine+","))
			}
			currentLine = word
		} else {
			currentLine = testLine
		}

		// Last word
		if i == len(words)-1 {
			if firstLine {
				fmt.Printf("%s\n", color.WhiteString(currentLine))
			} else {
				fmt.Printf("%s%s\n", indent, color.WhiteString(currentLine))
			}
		}
	}
}

func (ml *MigrationLister) getStatusColor(state string) *color.Color {
	switch state {
	case "uninitialized":
		return color.New(color.FgYellow)
	case "initialized":
		return color.New(color.FgCyan)
	case "lags_ok":
		return color.New(color.FgCyan)
	case "fenced":
		return color.New(color.FgYellow)
	case "promoting":
		return color.New(color.FgYellow)
	case "promoted":
		return color.New(color.FgGreen)
	case "switched":
		return color.New(color.FgGreen, color.Bold)
	default:
		return color.New(color.FgWhite)
	}
}
