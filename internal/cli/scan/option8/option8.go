package option8

import (
	"fmt"
	"log/slog"
	"os"
	"sort"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	credentialsYaml string

	// Nord theme color palette
	titleStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("#88C0D0")).Bold(true) // Nord Frost - Ice Blue
	regionStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#5E81AC")).Bold(true) // Nord Frost - Deep Blue
	clusterStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("#A3BE8C"))            // Nord Aurora - Green
	selectedStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("#EBCB8B")).Bold(true) // Nord Aurora - Yellow
	cursorStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#BF616A")).Bold(true) // Nord Aurora - Red
	helpStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("#616E88"))            // Nord Polar Night - Light Gray
	subtleStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("#81A1C1"))            // Nord Frost - More readable blue
	expandedIcon   = "▼"
	collapsedIcon  = "▶"
	selectedIcon   = "●" // Filled circle for selected
	unselectedIcon = "○" // Empty circle for unselected
)

// TreeItem represents an item in the tree (either region or cluster)
type TreeItem struct {
	ID          string
	DisplayName string
	ItemType    string // "region" or "cluster"
	Region      string
	ClusterArn  string
	ClusterName string
	Expanded    bool // only relevant for regions
	Selected    bool // only relevant for clusters
}

// Model represents the Bubble Tea model
type Model struct {
	items          []TreeItem
	cursor         int
	selectedCount  int
	confirmed      bool
	quitting       bool
	width          int
	height         int
	showingSummary bool
	summaryScroll  int // For scrolling in summary view
}

func NewScanOption8Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "option8",
		Short:         "Option 8 - Collapsible region-based cluster selection",
		Long:          "Hierarchical cluster selection with collapsible regions and multi-select clusters using Bubble Tea",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption8,
		RunE:          runScanOption8,
	}

	// Required flags
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file")

	cmd.Flags().AddFlagSet(requiredFlags)
	cmd.MarkFlagsOneRequired("credentials-yaml")

	return cmd
}

func preRunScanOption8(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runScanOption8(cmd *cobra.Command, args []string) error {
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

	// Create and run the Bubble Tea program
	model := initializeModel(credsYaml)
	program := tea.NewProgram(model, tea.WithAltScreen())

	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("error running program: %w", err)
	}

	// Process results
	m := finalModel.(Model)
	if m.quitting && !m.confirmed {
		slog.Info("Selection cancelled")
		return nil
	}

	selectedClusters := getSelectedClusters(m)
	if len(selectedClusters) == 0 {
		slog.Info("No clusters selected")
		return nil
	}

	// Log selected clusters
	slog.Info("Starting cluster scan", "count", len(selectedClusters))
	for _, item := range selectedClusters {
		slog.Info("Will scan cluster", "cluster", item.ClusterArn, "region", item.Region, "name", item.ClusterName)
	}

	return nil
}

func initializeModel(credsYaml types.CredsYaml) Model {
	var items []TreeItem

	// Sort regions by name
	var regionNames []string
	for regionName := range credsYaml.Regions {
		regionNames = append(regionNames, regionName)
	}
	sort.Strings(regionNames)

	// Build tree items
	for _, regionName := range regionNames {
		region := credsYaml.Regions[regionName]

		// Add region item (DisplayName will be updated dynamically in renderTreeItem)
		regionItem := TreeItem{
			ID:          fmt.Sprintf("region_%s", regionName),
			DisplayName: regionName, // Store just the region name, we'll format it dynamically
			ItemType:    "region",
			Region:      regionName,
			Expanded:    false, // Start collapsed
		}
		items = append(items, regionItem)

		// Add cluster items (initially hidden since region is collapsed)
		var clusterNames []string
		for clusterArn := range region.Clusters {
			clusterNames = append(clusterNames, clusterArn)
		}
		sort.Strings(clusterNames)

		for _, clusterArn := range clusterNames {
			cluster := region.Clusters[clusterArn]

			// Create a nicely formatted display name with Nord styling
			clusterNamePart := truncateString(cluster.ClusterName, 30)
			// Don't truncate ARN - show it in full

			clusterItem := TreeItem{
				ID:          fmt.Sprintf("cluster_%s_%s", regionName, clusterArn),
				DisplayName: fmt.Sprintf("%s • %s", clusterNamePart, clusterArn),
				ItemType:    "cluster",
				Region:      regionName,
				ClusterArn:  clusterArn,
				ClusterName: cluster.ClusterName,
			}
			items = append(items, clusterItem)
		}
	}

	return Model{
		items: items,
	}
}

func (m Model) Init() tea.Cmd {
	return nil
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		if m.showingSummary {
			// Handle summary view navigation
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit
			case "y", "enter":
				m.confirmed = true
				return m, tea.Quit
			case "n", "b", "esc":
				m.showingSummary = false
				m.summaryScroll = 0 // Reset scroll when going back
			case "up", "k":
				if m.summaryScroll > 0 {
					m.summaryScroll--
				}
			case "down", "j":
				m.summaryScroll++
				// Clamping will be done in View function
			case "home":
				m.summaryScroll = 0
			case "end":
				m.summaryScroll = 999 // Large number, will be clamped in View
			}
		} else {
			// Handle main tree view navigation
			switch msg.String() {
			case "ctrl+c", "q":
				m.quitting = true
				return m, tea.Quit

			case "up", "k":
				m.moveCursorUp()

			case "down", "j":
				m.moveCursorDown()

			case "right", "l":
				m.expandCurrentRegion()

			case "left", "h":
				m.collapseCurrentRegion()

			case "enter", " ":
				m.toggleItem()

			case "c":
				if m.selectedCount > 0 {
					m.showingSummary = true
				}

			case "a":
				m.selectAllInRegion()

			case "ctrl+a":
				m.selectAllClusters()

			case "r":
				m.resetSelections()
			}
		}

	}

	return m, nil
}

func (m *Model) moveCursorUp() {
	visibleItems := m.getVisibleItems()
	currentPos := m.findVisibleItemIndex(visibleItems)
	if currentPos > 0 {
		m.cursor = m.findItemIndex(visibleItems[currentPos-1].ID)
	}
}

func (m *Model) moveCursorDown() {
	visibleItems := m.getVisibleItems()
	currentPos := m.findVisibleItemIndex(visibleItems)
	if currentPos >= 0 && currentPos < len(visibleItems)-1 {
		m.cursor = m.findItemIndex(visibleItems[currentPos+1].ID)
	}
}

func (m *Model) toggleItem() {
	if m.cursor >= len(m.items) {
		return
	}

	item := &m.items[m.cursor]
	switch item.ItemType {
	case "region":
		// Toggle region expansion
		item.Expanded = !item.Expanded
	case "cluster":
		// Toggle cluster selection
		item.Selected = !item.Selected
		if item.Selected {
			m.selectedCount++
		} else {
			m.selectedCount--
		}
	}
}

func (m *Model) expandCurrentRegion() {
	if regionName := m.getCurrentRegion(); regionName != "" {
		m.setRegionExpanded(regionName, true)
	}
}

func (m *Model) collapseCurrentRegion() {
	if regionName := m.getCurrentRegion(); regionName != "" {
		currentItem := m.items[m.cursor]
		m.setRegionExpanded(regionName, false)

		// If cursor was on a cluster in the collapsed region, move it to the region
		if currentItem.ItemType == "cluster" {
			m.cursor = m.findRegionIndex(regionName)
		}
	}
}

func (m Model) getCurrentRegion() string {
	if m.cursor >= len(m.items) {
		return ""
	}
	return m.items[m.cursor].Region
}

func (m *Model) setRegionExpanded(regionName string, expanded bool) {
	for i := range m.items {
		if m.items[i].ItemType == "region" && m.items[i].Region == regionName {
			m.items[i].Expanded = expanded
			break
		}
	}
}

func (m Model) findRegionIndex(regionName string) int {
	for i, item := range m.items {
		if item.ItemType == "region" && item.Region == regionName {
			return i
		}
	}
	return m.cursor // fallback
}

func (m *Model) selectAllInRegion() {
	regionName := m.getCurrentRegion()
	if regionName == "" {
		return
	}

	// Expand region if cursor is on it and it's collapsed
	currentItem := m.items[m.cursor]
	if currentItem.ItemType == "region" && !currentItem.Expanded {
		m.items[m.cursor].Expanded = true
	}

	// Check if all clusters in region are selected
	allSelected, clusterCount := m.getRegionSelectionState(regionName)
	if clusterCount == 0 {
		return
	}

	// Toggle all clusters in the region
	selectAll := !allSelected
	for i := range m.items {
		if m.items[i].ItemType == "cluster" && m.items[i].Region == regionName {
			if m.items[i].Selected != selectAll {
				m.items[i].Selected = selectAll
				if selectAll {
					m.selectedCount++
				} else {
					m.selectedCount--
				}
			}
		}
	}
}

func (m Model) getRegionSelectionState(regionName string) (allSelected bool, count int) {
	selected := 0
	total := 0
	for _, item := range m.items {
		if item.ItemType == "cluster" && item.Region == regionName {
			total++
			if item.Selected {
				selected++
			}
		}
	}
	return selected == total && total > 0, total
}

func (m *Model) selectAllClusters() {
	// Check if all clusters are already selected
	totalClusters := 0
	selectedClusters := 0

	for _, item := range m.items {
		if item.ItemType == "cluster" {
			totalClusters++
			if item.Selected {
				selectedClusters++
			}
		}
	}

	// If all clusters are selected, deselect all. Otherwise, select all.
	selectAll := selectedClusters < totalClusters

	for i := range m.items {
		if m.items[i].ItemType == "cluster" {
			m.items[i].Selected = selectAll
		}
	}

	if selectAll {
		m.selectedCount = totalClusters
	} else {
		m.selectedCount = 0
	}
}

func (m *Model) resetSelections() {
	for i := range m.items {
		if m.items[i].ItemType == "cluster" && m.items[i].Selected {
			m.items[i].Selected = false
		}
	}
	m.selectedCount = 0
}

func (m Model) getVisibleItems() []TreeItem {
	var visible []TreeItem
	for _, item := range m.items {
		if m.isItemVisible(item) {
			visible = append(visible, item)
		}
	}
	return visible
}

func (m Model) findVisibleItemIndex(visibleItems []TreeItem) int {
	if m.cursor >= len(m.items) {
		return -1
	}
	currentID := m.items[m.cursor].ID
	for i, item := range visibleItems {
		if item.ID == currentID {
			return i
		}
	}
	return -1
}

func (m Model) findItemIndex(id string) int {
	for i, item := range m.items {
		if item.ID == id {
			return i
		}
	}
	return m.cursor // fallback to current position
}

func (m Model) isItemVisible(item TreeItem) bool {
	if item.ItemType == "region" {
		return true // Regions are always visible
	}

	// Clusters are visible only if their region is expanded
	for _, regionItem := range m.items {
		if regionItem.ItemType == "region" && regionItem.Region == item.Region {
			return regionItem.Expanded
		}
	}

	return false
}

func (m Model) View() string {
	if m.quitting {
		return ""
	}

	if m.showingSummary {
		return m.renderSummaryView()
	}

	return m.renderTreeView()
}

func (m Model) renderTreeView() string {
	var b strings.Builder

	// Title
	title := titleStyle.Render("🗂️  MSK Cluster Selection")
	b.WriteString(title + "\n\n")

	// Instructions
	help := helpStyle.Render("↑/↓: navigate • ←/→: expand/collapse region • space/enter: toggle • a: select all in region • ctrl+a: select all clusters • r: reset • c: confirm • q: quit")
	b.WriteString(help + "\n\n")

	// Status
	totalClusters := 0
	for _, item := range m.items {
		if item.ItemType == "cluster" {
			totalClusters++
		}
	}
	status := fmt.Sprintf("Selected: %d/%d clusters", m.selectedCount, totalClusters)
	b.WriteString(selectedStyle.Render(status) + "\n\n")

	// Tree view
	visibleItems := m.getVisibleItems()
	for _, item := range visibleItems {
		line := m.renderTreeItem(item)
		b.WriteString(line + "\n")
	}

	return b.String()
}

func (m Model) renderSummaryView() string {
	// Build the full content first
	var contentLines []string

	// Title
	contentLines = append(contentLines, titleStyle.Render("🎯 Selection Summary"))
	contentLines = append(contentLines, "")

	// Get selected clusters grouped by region
	selectedClusters := getSelectedClusters(m)
	regionGroups := make(map[string][]TreeItem)
	for _, cluster := range selectedClusters {
		regionGroups[cluster.Region] = append(regionGroups[cluster.Region], cluster)
	}

	// Sort regions
	var regions []string
	for region := range regionGroups {
		regions = append(regions, region)
	}
	sort.Strings(regions)

	if len(selectedClusters) == 0 {
		// No clusters selected
		contentLines = append(contentLines, helpStyle.Render("No clusters selected for scanning."))
		contentLines = append(contentLines, "")
		contentLines = append(contentLines, subtleStyle.Render("You can go back to select clusters or quit the application."))
		contentLines = append(contentLines, "")
	} else {
		// Show summary
		summaryHeader := selectedStyle.Render(fmt.Sprintf("📋 Ready to scan %d clusters across %d regions", len(selectedClusters), len(regions)))
		contentLines = append(contentLines, summaryHeader)
		contentLines = append(contentLines, "")

		// Show clusters grouped by region
		for i, region := range regions {
			clusters := regionGroups[region]

			// Region header
			regionHeader := regionStyle.Render(fmt.Sprintf("🌍 %s (%d clusters)", region, len(clusters)))
			contentLines = append(contentLines, regionHeader)

			// List clusters in this region
			for _, cluster := range clusters {
				clusterLine := fmt.Sprintf("  %s %s %s %s",
					selectedStyle.Render("●"),
					clusterStyle.Render(cluster.ClusterName),
					subtleStyle.Render("•"),
					subtleStyle.Render(cluster.ClusterArn))
				contentLines = append(contentLines, clusterLine)
			}

			// Add spacing between regions (except for the last one)
			if i < len(regions)-1 {
				contentLines = append(contentLines, "")
			}
		}

		contentLines = append(contentLines, "")
	}

	// Instructions (always at the bottom)
	contentLines = append(contentLines, helpStyle.Render("Controls:"))
	if len(selectedClusters) > 0 {
		contentLines = append(contentLines, helpStyle.Render("  y/enter: Proceed with scanning"))
	}
	contentLines = append(contentLines, helpStyle.Render("  n/b/esc: Go back to modify selection"))
	contentLines = append(contentLines, helpStyle.Render("  ↑/↓: Scroll • q: Quit"))

	// Calculate scrolling
	availableHeight := m.height - 2 // Leave some margin
	if availableHeight < 10 {
		availableHeight = 10 // Minimum height
	}

	totalLines := len(contentLines)
	maxScroll := totalLines - availableHeight
	if maxScroll < 0 {
		maxScroll = 0
	}

	// Clamp scroll position (create local copy since we can't modify model in View)
	scrollPos := m.summaryScroll
	if scrollPos > maxScroll {
		scrollPos = maxScroll
	}
	if scrollPos < 0 {
		scrollPos = 0
	}

	// Get the visible lines
	startLine := scrollPos
	endLine := startLine + availableHeight
	if endLine > totalLines {
		endLine = totalLines
	}

	visibleLines := contentLines[startLine:endLine]

	// Add scroll indicators if needed
	var result strings.Builder
	for i, line := range visibleLines {
		result.WriteString(line)
		if i < len(visibleLines)-1 {
			result.WriteString("\n")
		}
	}

	// Add scroll indicator at bottom if there's more content
	if endLine < totalLines {
		result.WriteString("\n" + subtleStyle.Render(fmt.Sprintf("... (%d more lines) ...", totalLines-endLine)))
	}

	return result.String()
}

func (m Model) renderTreeItem(item TreeItem) string {
	var prefix string
	var content string
	var style lipgloss.Style

	// Determine if this item is under cursor
	isCursor := false
	if m.cursor < len(m.items) && m.items[m.cursor].ID == item.ID {
		isCursor = true
	}

	if item.ItemType == "region" {
		// Region item with Nord styling and dynamic cluster count
		icon := collapsedIcon
		if item.Expanded {
			icon = expandedIcon
		}
		prefix = icon + " "

		// Calculate selection count for this region
		_, totalCount := m.getRegionSelectionState(item.Region)
		selectedCount := 0
		for _, otherItem := range m.items {
			if otherItem.ItemType == "cluster" && otherItem.Region == item.Region && otherItem.Selected {
				selectedCount++
			}
		}

		// Format the display name with selection count in [X/Y] format
		content = fmt.Sprintf("%s [%d/%d]", item.DisplayName, selectedCount, totalCount)
		style = regionStyle
	} else {
		// Cluster item with enhanced Nord styling
		icon := unselectedIcon
		if item.Selected {
			icon = selectedIcon
		}

		// Split the display name to style cluster name and ARN differently
		parts := strings.Split(item.DisplayName, " • ")
		if len(parts) == 2 {
			clusterNameStyled := clusterStyle.Render(parts[0])
			arnStyled := subtleStyle.Render(parts[1])
			content = clusterNameStyled + subtleStyle.Render(" • ") + arnStyled
		} else {
			content = item.DisplayName
		}

		// Style the icon based on selection state
		if item.Selected {
			styledIcon := selectedStyle.Render(icon)
			prefix = "  " + styledIcon + " "
		} else {
			prefix = "  " + icon + " "
		}

		// Always use the styled content (colors already applied above)
		style = lipgloss.NewStyle() // No additional styling since we already styled the content
	}

	line := prefix + content

	if isCursor {
		// Highlight cursor line with Nord red
		cursorIndicator := cursorStyle.Render("❯ ")
		if item.ItemType == "region" || item.Selected {
			line = cursorIndicator + style.Render(prefix+content)
		} else {
			line = cursorIndicator + prefix + content
		}
	} else {
		if item.ItemType == "region" || item.Selected {
			line = "  " + style.Render(prefix+content)
		} else {
			line = "  " + prefix + content
		}
	}

	return line
}

func getSelectedClusters(m Model) []TreeItem {
	var selected []TreeItem
	for _, item := range m.items {
		if item.ItemType == "cluster" && item.Selected {
			selected = append(selected, item)
		}
	}
	return selected
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
