// Package option6 implements MSK cluster selection using Bubble Tea with a clean state machine.
//
// ARCHITECTURE:
// - Simple state machine with clear transitions
// - Each screen is a separate view function
// - All state is explicit and easy to debug
// - Natural keyboard navigation throughout
//
// LIBRARIES USED:
// - bubbletea: Core TUI framework with full control
// - bubbles/list: For region and cluster selection lists
// - lipgloss: For beautiful, consistent styling
//
// STATE FLOW:
// main_menu → select_clusters → review → exit
//
//	↑            ↑              ↑       ↑
//	└────────────┴──────────────┴───────┘
package option6

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/confluentinc/kcp/internal/utils"
	"github.com/goccy/go-yaml"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// Application state - simple and explicit
type appState string

const (
	stateMainMenu      appState = "main_menu"
	stateSelectRegion  appState = "select_region"
	stateSelectCluster appState = "select_cluster"
	stateReview        appState = "review"
	stateExit          appState = "exit"
)

// Model holds all application state in one place - easy to understand and debug
type Model struct {
	// Core state
	state appState
	err   error

	// Data
	credsYaml        types.CredsYaml
	clusterMap       map[string]clusterInfo
	regionSelections map[string][]string

	// Current context
	selectedRegion string
	regions        []string

	// UI components
	mainMenuList list.Model
	regionList   list.Model
	clusterList  list.Model

	// Styling
	styles Styles
}

// clusterInfo holds metadata for efficient lookups
type clusterInfo struct {
	region      string
	clusterArn  string
	clusterName string
}

// Styles centralizes all visual styling
type Styles struct {
	Title      lipgloss.Style
	Subtitle   lipgloss.Style
	Selected   lipgloss.Style
	Unselected lipgloss.Style
	Help       lipgloss.Style
	Error      lipgloss.Style
	Success    lipgloss.Style
	Border     lipgloss.Style
}

// NewStyles creates consistent styling throughout the app
func NewStyles() Styles {
	return Styles{
		Title: lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("205")).
			Padding(1, 0),
		Subtitle: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(0, 0, 1, 0),
		Selected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("170")).
			Bold(true),
		Unselected: lipgloss.NewStyle().
			Foreground(lipgloss.Color("244")),
		Help: lipgloss.NewStyle().
			Foreground(lipgloss.Color("241")).
			Padding(1, 0, 0, 0),
		Error: lipgloss.NewStyle().
			Foreground(lipgloss.Color("196")).
			Bold(true),
		Success: lipgloss.NewStyle().
			Foreground(lipgloss.Color("46")).
			Bold(true),
		Border: lipgloss.NewStyle().
			BorderStyle(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("62")).
			Padding(1, 2),
	}
}

var (
	credentialsYaml string
)

func NewScanOption6Cmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:           "option6",
		Short:         "Option 6 - Bubble Tea cluster selection",
		Long:          "Clean Bubble Tea implementation of MSK cluster selection",
		SilenceErrors: true,
		Args:          cobra.NoArgs,
		PreRunE:       preRunScanOption6,
		RunE:          runScanOption6,
	}

	// Required flags
	requiredFlags := pflag.NewFlagSet("required", pflag.ExitOnError)
	requiredFlags.SortFlags = false
	requiredFlags.StringVar(&credentialsYaml, "credentials-yaml", "", "The credentials YAML file")

	cmd.Flags().AddFlagSet(requiredFlags)
	cmd.MarkFlagsOneRequired("credentials-yaml")

	return cmd
}

func preRunScanOption6(cmd *cobra.Command, args []string) error {
	return utils.BindEnvToFlags(cmd)
}

func runScanOption6(cmd *cobra.Command, args []string) error {
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

	// Initialize the model with clean state
	model := NewModel(credsYaml)

	// Run the Bubble Tea program
	program := tea.NewProgram(model, tea.WithAltScreen())
	finalModel, err := program.Run()
	if err != nil {
		return fmt.Errorf("program error: %w", err)
	}

	// Handle the final result
	if m, ok := finalModel.(Model); ok {
		if m.err != nil {
			return m.err
		}
		if m.state == stateExit {
			slog.Info("User completed cluster selection")
			return nil
		}
	}

	return nil
}

// NewModel creates a new model with initialized state
func NewModel(credsYaml types.CredsYaml) Model {
	// Extract regions
	regions := make([]string, 0, len(credsYaml.Regions))
	for regionName := range credsYaml.Regions {
		regions = append(regions, regionName)
	}

	// Initialize selections map
	regionSelections := make(map[string][]string)
	for regionName := range credsYaml.Regions {
		regionSelections[regionName] = []string{}
	}

	// Build cluster map for lookups
	clusterMap := make(map[string]clusterInfo)
	for regionName, region := range credsYaml.Regions {
		for clusterArn, cluster := range region.Clusters {
			key := fmt.Sprintf("%s|%s", regionName, clusterArn)
			clusterMap[key] = clusterInfo{
				region:      regionName,
				clusterArn:  clusterArn,
				clusterName: cluster.ClusterName,
			}
		}
	}

	model := Model{
		state:            stateMainMenu,
		credsYaml:        credsYaml,
		clusterMap:       clusterMap,
		regionSelections: regionSelections,
		regions:          regions,
		styles:           NewStyles(),
	}

	// Initialize main menu list
	model = model.initMainMenuList()

	return model
}

// Init initializes the model (required by Bubble Tea)
func (m Model) Init() tea.Cmd {
	return nil
}

// Update handles all state transitions and user input
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		// Global key bindings
		switch msg.String() {
		case "ctrl+c", "q":
			if m.state == stateMainMenu {
				return m, tea.Quit
			}
		case "esc":
			// Go back to previous state
			switch m.state {
			case stateSelectRegion:
				m.state = stateMainMenu
			case stateSelectCluster:
				m.state = stateSelectRegion
			case stateReview:
				m.state = stateMainMenu
			}
			return m, nil
		}

		// State-specific input handling
		switch m.state {
		case stateMainMenu:
			return m.updateMainMenu(msg)
		case stateSelectRegion:
			return m.updateRegionSelection(msg)
		case stateSelectCluster:
			return m.updateClusterSelection(msg)
		case stateReview:
			return m.updateReview(msg)
		}

	case tea.WindowSizeMsg:
		// Handle window resize if needed
		return m, nil
	}

	return m, nil
}

// View renders the current state
func (m Model) View() string {
	if m.err != nil {
		return m.styles.Error.Render(fmt.Sprintf("Error: %v", m.err))
	}

	switch m.state {
	case stateMainMenu:
		return m.viewMainMenu()
	case stateSelectRegion:
		return m.viewRegionSelection()
	case stateSelectCluster:
		return m.viewClusterSelection()
	case stateReview:
		return m.viewReview()
	case stateExit:
		return m.styles.Success.Render("✓ Cluster selection complete!")
	}

	return "Unknown state"
}

// updateMainMenu handles main menu input
func (m Model) updateMainMenu(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if item, ok := m.mainMenuList.SelectedItem().(menuItem); ok {
			switch item.action {
			case "select_clusters":
				m.state = stateSelectRegion
				m = m.initRegionList()
			case "review":
				m.state = stateReview
			}
		}
	default:
		var cmd tea.Cmd
		m.mainMenuList, cmd = m.mainMenuList.Update(msg)
		return m, cmd
	}
	return m, nil
}

// updateRegionSelection handles region selection input
func (m Model) updateRegionSelection(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "enter":
		if item, ok := m.regionList.SelectedItem().(regionItem); ok {
			m.selectedRegion = item.name
			m.state = stateSelectCluster
			m = m.initClusterList()
		}
	default:
		var cmd tea.Cmd
		m.regionList, cmd = m.regionList.Update(msg)
		return m, cmd
	}
	return m, nil
}

// updateClusterSelection handles cluster selection input
func (m Model) updateClusterSelection(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case " ":
		// Toggle cluster selection
		if item, ok := m.clusterList.SelectedItem().(clusterItem); ok {
			m.toggleClusterSelection(item.key)
			m = m.refreshClusterList()
		}
	case "enter":
		// Go back to region selection
		m.state = stateSelectRegion
	default:
		var cmd tea.Cmd
		m.clusterList, cmd = m.clusterList.Update(msg)
		return m, cmd
	}
	return m, nil
}

// updateReview handles review screen input
func (m Model) updateReview(msg tea.KeyMsg) (Model, tea.Cmd) {
	switch msg.String() {
	case "y", "enter":
		// Proceed with scanning
		allSelected := m.collectAllSelectedClusters()
		if len(allSelected) > 0 {
			slog.Info("Starting cluster scan", "count", len(allSelected))
			for _, key := range allSelected {
				cluster := m.clusterMap[key]
				slog.Info("Will scan cluster", "cluster", cluster.clusterArn, "region", cluster.region)
			}
		}
		m.state = stateExit
	case "n":
		m.state = stateMainMenu
	}
	return m, nil
}

// viewMainMenu renders the main menu
func (m Model) viewMainMenu() string {
	title := m.styles.Title.Render("🏠 MSK Cluster Selection")
	subtitle := m.styles.Subtitle.Render("Choose what you'd like to do:")

	help := m.styles.Help.Render("↑/↓ navigate • enter select • q to quit")

	content := fmt.Sprintf("%s\n%s\n%s\n\n%s",
		title,
		subtitle,
		m.mainMenuList.View(),
		help,
	)

	return m.styles.Border.Render(content)
}

// viewRegionSelection renders the region selection screen
func (m Model) viewRegionSelection() string {
	title := m.styles.Title.Render("🗺️ Select Region")
	subtitle := m.styles.Subtitle.Render("Choose a region to explore its clusters:")

	help := m.styles.Help.Render("↑/↓ navigate • enter select • esc back to menu")

	content := fmt.Sprintf("%s\n%s\n%s\n\n%s",
		title,
		subtitle,
		m.regionList.View(),
		help,
	)

	return m.styles.Border.Render(content)
}

// viewClusterSelection renders the cluster selection screen
func (m Model) viewClusterSelection() string {
	title := m.styles.Title.Render(fmt.Sprintf("☁️ Clusters in %s", m.selectedRegion))

	selectedCount := len(m.regionSelections[m.selectedRegion])
	subtitle := m.styles.Subtitle.Render(fmt.Sprintf("Selected: %d clusters", selectedCount))

	help := m.styles.Help.Render("↑/↓ navigate • space toggle • enter done • esc back to regions")

	content := fmt.Sprintf("%s\n%s\n%s\n\n%s",
		title,
		subtitle,
		m.clusterList.View(),
		help,
	)

	return m.styles.Border.Render(content)
}

// viewReview renders the review screen
func (m Model) viewReview() string {
	title := m.styles.Title.Render("📋 Review Your Selections")

	allSelected := m.collectAllSelectedClusters()
	if len(allSelected) == 0 {
		content := m.styles.Subtitle.Render("⚠️ No clusters selected yet.\n\nPress 'n' to go back and select some clusters.")
		help := m.styles.Help.Render("n back to menu • esc back")
		return m.styles.Border.Render(fmt.Sprintf("%s\n\n%s\n\n%s", title, content, help))
	}

	// Build summary
	var summary strings.Builder
	summary.WriteString(fmt.Sprintf("Selected %d clusters for scanning:\n\n", len(allSelected)))

	for regionName, clusters := range m.regionSelections {
		if len(clusters) > 0 {
			summary.WriteString(fmt.Sprintf("🌍 %s:\n", regionName))
			for _, clusterKey := range clusters {
				cluster := m.clusterMap[clusterKey]
				summary.WriteString(fmt.Sprintf("  ✓ %s\n", cluster.clusterName))
			}
			summary.WriteString("\n")
		}
	}

	content := m.styles.Subtitle.Render(summary.String())
	help := m.styles.Help.Render("y proceed with scanning • n back to menu • esc back")

	return m.styles.Border.Render(fmt.Sprintf("%s\n%s\n%s", title, content, help))
}

// Helper methods for state management

func (m Model) initMainMenuList() Model {
	items := []list.Item{
		menuItem{title: "🌍 Select Clusters", action: "select_clusters"},
		menuItem{title: "📋 Review Selections", action: "review"},
	}

	m.mainMenuList = list.New(items, menuDelegate{}, 50, 6)
	m.mainMenuList.Title = ""
	m.mainMenuList.SetShowStatusBar(false)
	m.mainMenuList.SetShowHelp(false)
	return m
}

func (m Model) initRegionList() Model {
	items := make([]list.Item, len(m.regions))
	for i, region := range m.regions {
		items[i] = regionItem{name: region}
	}

	m.regionList = list.New(items, regionDelegate{}, 50, 10)
	m.regionList.Title = ""
	m.regionList.SetShowStatusBar(false)
	m.regionList.SetShowHelp(false)
	return m
}

func (m Model) initClusterList() Model {
	region := m.credsYaml.Regions[m.selectedRegion]
	items := make([]list.Item, 0, len(region.Clusters))

	for clusterArn, cluster := range region.Clusters {
		key := fmt.Sprintf("%s|%s", m.selectedRegion, clusterArn)
		selected := m.isClusterSelected(key)
		items = append(items, clusterItem{
			key:      key,
			name:     cluster.ClusterName,
			selected: selected,
		})
	}

	m.clusterList = list.New(items, clusterDelegate{}, 60, 15)
	m.clusterList.Title = ""
	m.clusterList.SetShowStatusBar(false)
	m.clusterList.SetShowHelp(false)
	return m
}

func (m Model) refreshClusterList() Model {
	// Update the selected state of all items
	items := make([]list.Item, len(m.clusterList.Items()))
	for i, item := range m.clusterList.Items() {
		if clusterItem, ok := item.(clusterItem); ok {
			clusterItem.selected = m.isClusterSelected(clusterItem.key)
			items[i] = clusterItem
		}
	}
	m.clusterList.SetItems(items)
	return m
}

func (m Model) toggleClusterSelection(clusterKey string) {
	selections := m.regionSelections[m.selectedRegion]

	// Check if already selected
	for i, selected := range selections {
		if selected == clusterKey {
			// Remove it
			m.regionSelections[m.selectedRegion] = append(selections[:i], selections[i+1:]...)
			return
		}
	}

	// Add it
	m.regionSelections[m.selectedRegion] = append(selections, clusterKey)
}

func (m Model) isClusterSelected(clusterKey string) bool {
	for _, selected := range m.regionSelections[m.selectedRegion] {
		if selected == clusterKey {
			return true
		}
	}
	return false
}

func (m Model) collectAllSelectedClusters() []string {
	var all []string
	for _, clusters := range m.regionSelections {
		all = append(all, clusters...)
	}
	return all
}

// List item types and delegates

type menuItem struct {
	title  string
	action string
}

func (m menuItem) FilterValue() string { return m.title }

type menuDelegate struct{}

func (d menuDelegate) Height() int                             { return 1 }
func (d menuDelegate) Spacing() int                            { return 0 }
func (d menuDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d menuDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(menuItem)
	if !ok {
		return
	}

	str := fmt.Sprintf("  %s", item.title)
	if index == m.Index() {
		str = fmt.Sprintf("▶ %s", item.title)
	}

	fmt.Fprint(w, str)
}

type regionItem struct {
	name string
}

func (r regionItem) FilterValue() string { return r.name }

type regionDelegate struct{}

func (d regionDelegate) Height() int                             { return 1 }
func (d regionDelegate) Spacing() int                            { return 0 }
func (d regionDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d regionDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(regionItem)
	if !ok {
		return
	}

	str := fmt.Sprintf("  %s", item.name)
	if index == m.Index() {
		str = fmt.Sprintf("▶ %s", item.name)
	}

	fmt.Fprint(w, str)
}

type clusterItem struct {
	key      string
	name     string
	selected bool
}

func (c clusterItem) FilterValue() string { return c.name }

type clusterDelegate struct{}

func (d clusterDelegate) Height() int                             { return 1 }
func (d clusterDelegate) Spacing() int                            { return 0 }
func (d clusterDelegate) Update(_ tea.Msg, _ *list.Model) tea.Cmd { return nil }
func (d clusterDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	item, ok := listItem.(clusterItem)
	if !ok {
		return
	}

	checkbox := "☐"
	if item.selected {
		checkbox = "☑"
	}

	str := fmt.Sprintf("  %s %s", checkbox, item.name)
	if index == m.Index() {
		str = fmt.Sprintf("▶ %s %s", checkbox, item.name)
	}

	fmt.Fprint(w, str)
}

// validateCredentials ensures exactly one auth method is enabled per cluster
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
