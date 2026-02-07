package status

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/confluentinc/kcp/internal/services/clusterlink"
)

const maxHistory = 30

// sparkline characters from lowest to highest
var sparkBlocks = []rune{'▁', '▂', '▃', '▄', '▅', '▆', '▇', '█'}

// --- Messages ---

type fetchResultMsg struct {
	topics []clusterlink.MirrorTopic
	err    error
}

type tickMsg time.Time
type flashDoneMsg struct{}

// --- Model ---

type model struct {
	service        clusterlink.Service
	config         clusterlink.Config
	topics         []clusterlink.MirrorTopic
	err            error
	loading        bool
	lastUpdated    time.Time
	lagHistory     map[string][]int
	pollSeconds    int
	showPartitions bool
	flashUpdated   bool
	scrollOffset   int
	width          int
	height         int
}

func newModel(svc clusterlink.Service, cfg clusterlink.Config, pollSeconds int) model {
	return model{
		service:     svc,
		config:      cfg,
		pollSeconds: pollSeconds,
		lagHistory:  make(map[string][]int),
		loading:     true,
	}
}

func newProgram(m model) *tea.Program {
	return tea.NewProgram(m, tea.WithAltScreen())
}

// --- Init ---

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.service, m.config), tea.WindowSize())
}

// --- Update ---

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "ctrl+c":
			return m, tea.Quit
		case "p":
			m.showPartitions = !m.showPartitions
			return m, nil
		case "r":
			if !m.loading {
				m.loading = true
				return m, fetchCmd(m.service, m.config)
			}
			return m, nil
		case "+", "=":
			if m.pollSeconds < 60 {
				m.pollSeconds++
			}
			return m, nil
		case "-", "_":
			if m.pollSeconds > 1 {
				m.pollSeconds--
			}
			return m, nil
		case "up", "k":
			if m.scrollOffset > 0 {
				m.scrollOffset--
			}
			return m, nil
		case "down", "j":
			m.scrollOffset++
			return m, nil
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case fetchResultMsg:
		m.loading = false
		if msg.err != nil {
			m.err = msg.err
			// keep retrying on next tick
			return m, scheduleTick(m.pollSeconds)
		}
		m.err = nil
		m.topics = msg.topics
		m.lastUpdated = time.Now()
		m.flashUpdated = true

		// update lag history
		for _, t := range m.topics {
			total := 0
			for _, l := range t.MirrorLags {
				total += l.Lag
			}
			h := m.lagHistory[t.MirrorTopicName]
			h = append(h, total)
			if len(h) > maxHistory {
				h = h[len(h)-maxHistory:]
			}
			m.lagHistory[t.MirrorTopicName] = h
		}

		return m, tea.Batch(scheduleTick(m.pollSeconds), scheduleFlashDone())

	case flashDoneMsg:
		m.flashUpdated = false
		return m, nil

	case tickMsg:
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, fetchCmd(m.service, m.config)
	}

	return m, nil
}

// --- View ---

// Confluent brand-inspired color palette
const (
	confluentNavy    = "#172B4D" // dark navy – title bar background
	confluentBlue    = "#1993D1" // medium blue – primary accent
	confluentLtBlue  = "#6CB4EE" // light blue – config labels
	confluentTeal    = "#17B8A6" // teal – sparklines, data-streaming accent
	confluentSlate   = "#8B9CB6" // blue-grey – table headers, help text
	confluentMutedFg = "#7B8CA5" // muted blue-grey – partition details
	confluentGreen   = "#2ECC71" // green – success, ACTIVE, zero lag
	confluentAmber   = "#F5A623" // amber – positive lag, warnings
	confluentRed     = "#E74C3C" // red – errors, FAILED
	confluentYellow  = "#F1C40F" // yellow – PAUSED status
	confluentWhite   = "#FFFFFF" // white – title text, config values
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color(confluentWhite)).
			Background(lipgloss.Color(confluentNavy)).
			Padding(0, 1)

	configLabelStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentLtBlue)).
				Bold(true)

	configValueStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentWhite))

	headerStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentSlate)).
			Bold(true)

	statusActiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentGreen))

	statusPausedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentYellow))

	statusFailedStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentRed))

	statusOtherStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentSlate))

	lagZeroStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentGreen))

	lagPositiveStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentAmber))

	sparkStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentBlue))

	indicatorRunning = lipgloss.NewStyle().
				Foreground(lipgloss.Color(confluentBlue)).
				Render("●")

	indicatorError = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentRed)).
			Render("●")

	partitionStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentMutedFg))

	errorStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentRed)).
			Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color(confluentSlate))
)

func (m model) View() string {
	var b strings.Builder

	// Title
	b.WriteString(titleStyle.Render("Mirror Topic Lag Monitor"))
	b.WriteString("\n\n")

	// Config summary
	b.WriteString(configLabelStyle.Render("  REST Endpoint: "))
	b.WriteString(configValueStyle.Render(m.config.RestEndpoint))
	b.WriteString("\n")
	b.WriteString(configLabelStyle.Render("     Cluster ID: "))
	b.WriteString(configValueStyle.Render(m.config.ClusterID))
	b.WriteString("\n")
	b.WriteString(configLabelStyle.Render("      Link Name: "))
	b.WriteString(configValueStyle.Render(m.config.LinkName))
	b.WriteString("\n\n")

	// Status line
	b.WriteString("  ")
	if m.err != nil {
		b.WriteString(indicatorError)
		b.WriteString(fmt.Sprintf(" Refreshing every %ds (error, retrying...)", m.pollSeconds))
	} else {
		b.WriteString(indicatorRunning)
		b.WriteString(fmt.Sprintf(" Refreshing every %ds", m.pollSeconds))
	}

	if !m.lastUpdated.IsZero() {
		ts := m.lastUpdated.Format("15:04:05")
		if m.flashUpdated {
			b.WriteString("  |  ")
			b.WriteString(statusActiveStyle.Render(fmt.Sprintf("Last updated: %s", ts)))
		} else {
			b.WriteString(fmt.Sprintf("  |  Last updated: %s", ts))
		}
	}
	b.WriteString("\n\n")

	// Error
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n")
		b.WriteString("  Check your cluster link credentials and REST endpoint.\n\n")
	}

	// Table
	if len(m.topics) > 0 {
		b.WriteString(renderTable(m.topics, m.lagHistory, m.showPartitions))
	} else if m.err == nil && !m.loading {
		b.WriteString("  No mirror topics found.\n")
	}

	// Build full content, then apply scroll viewport
	content := b.String()
	lines := strings.Split(content, "\n")

	// Help bar (rendered outside scrollable area)
	helpLine := helpStyle.Render("  q quit  •  p partitions  •  r refresh  •  +/- interval  •  ↑↓ scroll")

	// Calculate viewport
	helpLines := 2 // help line + trailing newline
	viewportHeight := m.height - helpLines
	if viewportHeight < 1 {
		viewportHeight = 1
	}

	// Clamp scroll offset
	maxOffset := len(lines) - viewportHeight
	if maxOffset < 0 {
		maxOffset = 0
	}
	if m.scrollOffset > maxOffset {
		m.scrollOffset = maxOffset
	}

	// Slice visible lines
	end := m.scrollOffset + viewportHeight
	if end > len(lines) {
		end = len(lines)
	}
	visible := lines[m.scrollOffset:end]

	var out strings.Builder
	out.WriteString(strings.Join(visible, "\n"))
	out.WriteString("\n")

	// Scroll indicator in help bar
	if maxOffset > 0 {
		helpLine += helpStyle.Render(fmt.Sprintf("  (%d/%d)", m.scrollOffset+1, maxOffset+1))
	}
	out.WriteString(helpLine)
	out.WriteString("\n")

	return out.String()
}

// --- Table Rendering ---

func renderTable(topics []clusterlink.MirrorTopic, history map[string][]int, showPartitions bool) string {
	// Sort: ACTIVE first, then alphabetically
	sorted := make([]clusterlink.MirrorTopic, len(topics))
	copy(sorted, topics)
	sort.Slice(sorted, func(i, j int) bool {
		ai := sorted[i].MirrorStatus == "ACTIVE"
		aj := sorted[j].MirrorStatus == "ACTIVE"
		if ai != aj {
			return ai
		}
		return sorted[i].MirrorTopicName < sorted[j].MirrorTopicName
	})

	// Calculate column widths
	nameW := len("TOPIC NAME")
	statusW := len("STATUS")
	lagW := len("TOTAL LAG")
	trendW := maxHistory // sparkline width

	for _, t := range sorted {
		if len(t.MirrorTopicName) > nameW {
			nameW = len(t.MirrorTopicName)
		}
		if len(t.MirrorStatus) > statusW {
			statusW = len(t.MirrorStatus)
		}
		lagStr := formatLag(totalLag(t))
		if len(lagStr) > lagW {
			lagW = len(lagStr)
		}
	}

	// Header
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(padRight("TOPIC NAME", nameW)))
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(padRight("STATUS", statusW)))
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(padLeft("TOTAL LAG", lagW)))
	b.WriteString("  ")
	b.WriteString(headerStyle.Render("LAG TREND"))
	b.WriteString("\n")

	// Separator
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", nameW)))
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", statusW)))
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", lagW)))
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", trendW)))
	b.WriteString("\n")

	// Pre-compute partition column widths across all topics
	partNumW := 2 // minimum "P0"
	partLagW := 1 // minimum "0"
	partOffW := 1 // minimum "0"
	if showPartitions {
		for _, t := range sorted {
			for _, p := range t.MirrorLags {
				pnStr := fmt.Sprintf("P%d", p.Partition)
				if len(pnStr) > partNumW {
					partNumW = len(pnStr)
				}
				plStr := formatLag(p.Lag)
				if len(plStr) > partLagW {
					partLagW = len(plStr)
				}
				poStr := formatLag(p.LastSourceFetchOffset)
				if len(poStr) > partOffW {
					partOffW = len(poStr)
				}
			}
		}
	}

	// Rows
	for _, t := range sorted {
		lag := totalLag(t)
		lagStr := formatLag(lag)
		spark := renderSparkline(history[t.MirrorTopicName])

		b.WriteString("  ")
		b.WriteString(padRight(t.MirrorTopicName, nameW))
		b.WriteString("  ")
		b.WriteString(padRight(styledStatus(t.MirrorStatus), statusW+statusStyleExtraWidth(t.MirrorStatus)))
		b.WriteString("  ")
		b.WriteString(padLeftStyled(styledLag(lag, lagStr), lagW, len(lagStr)))
		b.WriteString("  ")
		b.WriteString(sparkStyle.Render(spark))
		b.WriteString("\n")

		// Partition detail rows
		if showPartitions && len(t.MirrorLags) > 0 {
			// Sort partitions by partition number
			parts := make([]clusterlink.MirrorLag, len(t.MirrorLags))
			copy(parts, t.MirrorLags)
			sort.Slice(parts, func(i, j int) bool {
				return parts[i].Partition < parts[j].Partition
			})
			for _, p := range parts {
				pLagStr := formatLag(p.Lag)
				offsetStr := formatLag(p.LastSourceFetchOffset)
				pnStr := fmt.Sprintf("P%d", p.Partition)
				b.WriteString(partitionStyle.Render(fmt.Sprintf("      %s  lag: ", padRight(pnStr, partNumW))))
				b.WriteString(padLeftStyled(styledLag(p.Lag, pLagStr), partLagW, len(pLagStr)))
				b.WriteString(partitionStyle.Render(fmt.Sprintf("  last fetched offset: %s", padLeft(offsetStr, partOffW))))
				b.WriteString("\n")
			}
		}
	}

	return b.String()
}

// --- Helpers ---

func totalLag(t clusterlink.MirrorTopic) int {
	total := 0
	for _, l := range t.MirrorLags {
		total += l.Lag
	}
	return total
}

func formatLag(lag int) string {
	if lag == 0 {
		return "0"
	}
	// Format with thousands separators
	s := fmt.Sprintf("%d", lag)
	n := len(s)
	if n <= 3 {
		return s
	}
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteByte(',')
		}
		result.WriteRune(c)
	}
	return result.String()
}

func styledStatus(status string) string {
	switch strings.ToUpper(status) {
	case "ACTIVE":
		return statusActiveStyle.Render(status)
	case "PAUSED":
		return statusPausedStyle.Render(status)
	case "FAILED":
		return statusFailedStyle.Render(status)
	default:
		return statusOtherStyle.Render(status)
	}
}

// statusStyleExtraWidth returns the number of extra bytes lipgloss adds beyond the visible text
func statusStyleExtraWidth(status string) int {
	styled := styledStatus(status)
	return len(styled) - len(status)
}

func styledLag(lag int, lagStr string) string {
	if lag > 0 {
		return lagPositiveStyle.Render(lagStr)
	}
	return lagZeroStyle.Render(lagStr)
}

func renderSparkline(data []int) string {
	if len(data) == 0 {
		return "-"
	}
	maxVal := 0
	for _, v := range data {
		if v > maxVal {
			maxVal = v
		}
	}
	if maxVal == 0 {
		// all zeros
		return strings.Repeat(string(sparkBlocks[0]), len(data))
	}
	var sb strings.Builder
	for _, v := range data {
		idx := v * (len(sparkBlocks) - 1) / maxVal
		if idx >= len(sparkBlocks) {
			idx = len(sparkBlocks) - 1
		}
		sb.WriteRune(sparkBlocks[idx])
	}
	return sb.String()
}

func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
}

func padLeft(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return strings.Repeat(" ", width-len(s)) + s
}

// padLeftStyled right-aligns a styled string given its visible width
func padLeftStyled(styled string, width int, visibleLen int) string {
	if visibleLen >= width {
		return styled
	}
	return strings.Repeat(" ", width-visibleLen) + styled
}

// --- Commands ---

func fetchCmd(svc clusterlink.Service, cfg clusterlink.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		topics, err := svc.ListMirrorTopics(ctx, cfg)
		return fetchResultMsg{topics: topics, err: err}
	}
}

func scheduleTick(seconds int) tea.Cmd {
	return tea.Tick(time.Duration(seconds)*time.Second, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func scheduleFlashDone() tea.Cmd {
	return tea.Tick(500*time.Millisecond, func(t time.Time) tea.Msg {
		return flashDoneMsg{}
	})
}
