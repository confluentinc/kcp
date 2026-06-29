package list

import (
	"fmt"
	"os"
	"slices"
	"strings"

	"github.com/confluentinc/kcp/internal/services/cutover"
	"github.com/fatih/color"
)

type CutoverListerOpts struct {
	CutoverStateFile string
	CutoverState     cutover.CutoverState
}

type CutoverLister struct {
	cutoverStateFile string
	cutoverState     cutover.CutoverState
}

func NewCutoverLister(opts CutoverListerOpts) *CutoverLister {
	return &CutoverLister{
		cutoverStateFile: opts.CutoverStateFile,
		cutoverState:     opts.CutoverState,
	}
}

func (ml *CutoverLister) Run() error {
	cutovers := ml.cutoverState.Cutovers

	if len(cutovers) == 0 {
		fmt.Printf("\n%s No cutovers found in %s\n\n", color.YellowString("ℹ"), ml.cutoverStateFile)
		fmt.Printf("Run %s to create a new cutover.\n\n", color.CyanString("kcp cutover init"))
		return nil
	}

	// Get file info for last updated timestamp
	fileInfo, err := os.Stat(ml.cutoverStateFile)
	var lastUpdated string
	if err == nil {
		lastUpdated = fileInfo.ModTime().Format("2006-01-02 15:04:05")
	} else {
		lastUpdated = "unknown"
	}

	// Print header
	fmt.Printf("\n%s %s\n", color.CyanString("Cutover State:"), color.WhiteString(ml.cutoverStateFile))
	fmt.Printf("%s %s\n\n", color.CyanString("Last Updated:"), color.WhiteString(lastUpdated))
	fmt.Printf("%s\n\n", color.CyanString("Cutovers (%d):", len(cutovers)))

	// Sort cutovers by creation time (newest first)
	// We'll use the cutover ID timestamp if available, otherwise just reverse order
	sortedCutovers := make([]cutover.CutoverConfig, len(cutovers))
	copy(sortedCutovers, cutovers)
	slices.Reverse(sortedCutovers)

	// Display each cutover
	for idx, cfg := range sortedCutovers {
		ml.displayCutover(idx+1, cfg)
	}

	return nil
}

func (ml *CutoverLister) displayCutover(index int, cfg cutover.CutoverConfig) {
	// Index and Cutover ID
	fmt.Printf("%s %s %s\n",
		color.HiBlackString("[%d]", index),
		color.HiBlackString("Cutover ID:"),
		color.New(color.Bold, color.FgWhite).Sprint(cfg.CutoverId))

	// Status with color coding
	statusColor := ml.getStatusColor(cfg.CurrentState)
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Status:"),
		statusColor.Sprint(cfg.CurrentState))

	// Gateway
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Gateway:"),
		color.WhiteString("%s/%s", cfg.K8sNamespace, cfg.InitialCrName))

	// Cluster Link
	fmt.Printf("    %s %s\n",
		color.HiBlackString("Cluster Link:"),
		color.WhiteString(cfg.ClusterLinkName))

	// Topics - display all topic names with word wrapping
	ml.displayTopics(cfg.Topics)

	fmt.Println() // Blank line between cutovers
}

func (ml *CutoverLister) displayTopics(topics []string) {
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

func (ml *CutoverLister) getStatusColor(state string) *color.Color {
	switch state {
	case "uninitialized":
		return color.New(color.FgYellow)
	case "initialized":
		return color.New(color.FgCyan)
	case "lags_ok":
		return color.New(color.FgCyan)
	case "fenced":
		return color.New(color.FgYellow)
	case "promoted":
		return color.New(color.FgGreen)
	case "switched":
		return color.New(color.FgGreen, color.Bold)
	default:
		return color.New(color.FgWhite)
	}
}
