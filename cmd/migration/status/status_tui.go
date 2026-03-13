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
	"github.com/confluentinc/kcp/internal/services/offset"
)

// --- Messages ---

type fetchResultMsg struct {
	topics       []clusterlink.MirrorTopic
	topicOffsets map[string]*topicOffsetData
	err          error
}

type tickMsg time.Time
type flashDoneMsg struct{}

// --- Model ---

type topicOffsetData struct {
	sourceOffsets      map[int32]int64
	destinationOffsets map[int32]int64
}

type model struct {
	sourceOffset       *offset.Service
	destinationOffset  *offset.Service
	clService      clusterlink.Service
	clConfig       clusterlink.Config
	region         string
	topics         []clusterlink.MirrorTopic
	topicOffsets   map[string]*topicOffsetData
	err            error
	loading        bool
	lastUpdated    time.Time
	pollSeconds    int
	showPartitions bool
	scrollOffset   int
	width          int
	height         int
}

func newModel(sourceOffset, destinationOffset *offset.Service, clSvc clusterlink.Service, clCfg clusterlink.Config, region string, pollSeconds int) model {
	return model{
		sourceOffset:      sourceOffset,
		destinationOffset: destinationOffset,
		clService:    clSvc,
		clConfig:     clCfg,
		region:       region,
		pollSeconds:  pollSeconds,
		topicOffsets: make(map[string]*topicOffsetData),
		loading:      true,
	}
}

func newProgram(m model) *tea.Program {
	return tea.NewProgram(m, tea.WithAltScreen())
}

// --- Init ---

func (m model) Init() tea.Cmd {
	return tea.Batch(fetchCmd(m.sourceOffset, m.destinationOffset, m.clService, m.clConfig), tea.WindowSize())
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
				return m, fetchCmd(m.sourceOffset, m.destinationOffset, m.clService, m.clConfig)
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
			return m, scheduleTick(m.pollSeconds)
		}
		m.err = nil
		m.topics = msg.topics
		m.topicOffsets = msg.topicOffsets
		m.lastUpdated = time.Now()

		return m, tea.Batch(scheduleTick(m.pollSeconds), scheduleFlashDone())

	case flashDoneMsg:
		return m, nil

	case tickMsg:
		if m.loading {
			return m, nil
		}
		m.loading = true
		return m, fetchCmd(m.sourceOffset, m.destinationOffset, m.clService, m.clConfig)
	}

	return m, nil
}

// --- View ---

// Confluent brand-inspired color palette
const (
	confluentNavy    = "#172B4D"
	confluentBlue    = "#1993D1"
	confluentLtBlue  = "#6CB4EE"
	confluentSlate   = "#8B9CB6"
	confluentMutedFg = "#7B8CA5"
	confluentGreen   = "#2ECC71"
	confluentAmber   = "#F5A623"
	confluentRed     = "#E74C3C"
	confluentYellow  = "#F1C40F"
	confluentWhite   = "#FFFFFF"
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
	b.WriteString(titleStyle.Render("Migration Lag Monitor"))
	b.WriteString("\n\n")

	// Config summary
	b.WriteString(configLabelStyle.Render("     Source: "))
	b.WriteString(configValueStyle.Render(fmt.Sprintf("MSK (%s)", m.region)))
	b.WriteString("\n")
	b.WriteString(configLabelStyle.Render("       Dest: "))
	b.WriteString(configValueStyle.Render("Confluent Cloud"))
	b.WriteString("\n")
	b.WriteString(configLabelStyle.Render("       Link: "))
	b.WriteString(configValueStyle.Render(m.clConfig.LinkName))
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
		b.WriteString(fmt.Sprintf("  |  Last updated: %s", ts))
	}
	b.WriteString("\n\n")

	// Error
	if m.err != nil {
		b.WriteString(errorStyle.Render(fmt.Sprintf("  Error: %v", m.err)))
		b.WriteString("\n\n")
	}

	// Table
	if len(m.topics) > 0 {
		b.WriteString(renderTable(m.topics, m.topicOffsets, m.showPartitions))
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

// topicRow holds pre-computed values for a single topic row.
type topicRow struct {
	name   string
	status string
	source int64
	dest   int64
	lag    int64
}

func renderTable(topics []clusterlink.MirrorTopic, offsets map[string]*topicOffsetData, showPartitions bool) string {
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

	// Pre-compute row data
	rows := make([]topicRow, len(sorted))
	for i, t := range sorted {
		od := offsets[t.MirrorTopicName]
		var src, dst, lag int64
		if od != nil {
			src = sumOffsets(od.sourceOffsets)
			dst = sumOffsets(od.destinationOffsets)
			lag = totalOffsetLag(od)
		}
		rows[i] = topicRow{
			name:   t.MirrorTopicName,
			status: t.MirrorStatus,
			source: src,
			dest:   dst,
			lag:    lag,
		}
	}

	// Calculate column widths from header labels and data
	nameW := len("TOPIC NAME")
	statusW := len("STATUS")
	sourceW := len("SOURCE")
	destW := len("DESTINATION")
	lagW := len("LAG")

	var totalLagVal int64

	for _, r := range rows {
		if len(r.name) > nameW {
			nameW = len(r.name)
		}
		if len(r.status) > statusW {
			statusW = len(r.status)
		}
		if w := len(formatLag64(r.source)); w > sourceW {
			sourceW = w
		}
		if w := len(formatLag64(r.dest)); w > destW {
			destW = w
		}
		if w := len(formatLag64(r.lag)); w > lagW {
			lagW = w
		}
		totalLagVal += r.lag
	}

	totalLagStr := formatLag64(totalLagVal)
	if len(totalLagStr) > lagW {
		lagW = len(totalLagStr)
	}
	if len("Total Lag:") > nameW {
		nameW = len("Total Lag:")
	}

	// Header
	var b strings.Builder
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(padRight("TOPIC NAME", nameW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(padRight("STATUS", statusW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(padLeft("SOURCE", sourceW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(padLeft("DESTINATION", destW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(padLeft("LAG", lagW)))
	b.WriteString("\n")

	// Separator
	b.WriteString("  ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", nameW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", statusW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", sourceW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", destW)))
	b.WriteString("   ")
	b.WriteString(headerStyle.Render(strings.Repeat("─", lagW)))
	b.WriteString("\n")

	// Pre-compute partition column widths if needed
	var partNumW, partSrcW, partDstW, partLagW int
	if showPartitions {
		partNumW = 2
		partSrcW = 1
		partDstW = 1
		partLagW = 1
		for _, t := range sorted {
			od := offsets[t.MirrorTopicName]
			if od == nil {
				continue
			}
			for _, p := range offset.SortedPartitionIDs(od.sourceOffsets, od.destinationOffsets) {
				if w := len(fmt.Sprintf("P%d", p)); w > partNumW {
					partNumW = w
				}
				srcVal := od.sourceOffsets[p]
				dstVal := od.destinationOffsets[p]
				pLag := srcVal - dstVal
				if pLag < 0 {
					pLag = 0
				}
				if w := len(formatLag64(srcVal)); w > partSrcW {
					partSrcW = w
				}
				if w := len(formatLag64(dstVal)); w > partDstW {
					partDstW = w
				}
				if w := len(formatLag64(pLag)); w > partLagW {
					partLagW = w
				}
			}
		}
	}

	// Data rows
	for _, r := range rows {
		srcStr := formatLag64(r.source)
		dstStr := formatLag64(r.dest)
		lagStr := formatLag64(r.lag)

		b.WriteString("  ")
		b.WriteString(padRight(r.name, nameW))
		b.WriteString("   ")
		b.WriteString(padRight(styledStatus(r.status), statusW+statusStyleExtraWidth(r.status)))
		b.WriteString("   ")
		b.WriteString(padLeft(srcStr, sourceW))
		b.WriteString("   ")
		b.WriteString(padLeft(dstStr, destW))
		b.WriteString("   ")
		b.WriteString(padLeftStyled(styledLag64(r.lag, lagStr), lagW, len(lagStr)))
		b.WriteString("\n")

		// Partition detail rows
		if showPartitions {
			od := offsets[r.name]
			if od != nil {
				for _, p := range offset.SortedPartitionIDs(od.sourceOffsets, od.destinationOffsets) {
					pnStr := fmt.Sprintf("P%d", p)
					srcVal := od.sourceOffsets[p]
					dstVal := od.destinationOffsets[p]
					pLag := srcVal - dstVal
					if pLag < 0 {
						pLag = 0
					}

					line := fmt.Sprintf("      %s   src: %s   dst: %s   lag: %s",
						padRight(pnStr, partNumW),
						padLeft(formatLag64(srcVal), partSrcW),
						padLeft(formatLag64(dstVal), partDstW),
						padLeft(formatLag64(pLag), partLagW),
					)
					b.WriteString(partitionStyle.Render(line))
					b.WriteString("\n")
				}
			}
		}
	}

	// Total row
	b.WriteString("\n  ")
	b.WriteString(padRight("Total Lag:", nameW))
	b.WriteString("   ")
	b.WriteString(strings.Repeat(" ", statusW))
	b.WriteString("   ")
	b.WriteString(strings.Repeat(" ", sourceW))
	b.WriteString("   ")
	b.WriteString(strings.Repeat(" ", destW))
	b.WriteString("   ")
	b.WriteString(padLeftStyled(styledLag64(totalLagVal, totalLagStr), lagW, len(totalLagStr)))
	b.WriteString("\n")

	return b.String()
}

// --- Helpers ---

func sumOffsets(offsets map[int32]int64) int64 {
	var total int64
	for _, v := range offsets {
		total += v
	}
	return total
}

func totalOffsetLag(od *topicOffsetData) int64 {
	var total int64
	for partition, sourceOffset := range od.sourceOffsets {
		destinationOffset := od.destinationOffsets[partition]
		lag := sourceOffset - destinationOffset
		if lag > 0 {
			total += lag
		}
	}
	return total
}

func formatLag64(lag int64) string {
	if lag == 0 {
		return "0"
	}
	negative := lag < 0
	if negative {
		lag = -lag
	}
	s := fmt.Sprintf("%d", lag)
	n := len(s)
	if n <= 3 {
		if negative {
			return "-" + s
		}
		return s
	}
	var result strings.Builder
	for i, c := range s {
		if i > 0 && (n-i)%3 == 0 {
			result.WriteByte(',')
		}
		result.WriteRune(c)
	}
	r := result.String()
	if negative {
		return "-" + r
	}
	return r
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

// statusStyleExtraWidth returns the number of extra bytes lipgloss adds beyond the visible text.
func statusStyleExtraWidth(status string) int {
	styled := styledStatus(status)
	return len(styled) - len(status)
}

func styledLag64(lag int64, lagStr string) string {
	if lag > 0 {
		return lagPositiveStyle.Render(lagStr)
	}
	return lagZeroStyle.Render(lagStr)
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

// padLeftStyled right-aligns a styled string given its visible width.
func padLeftStyled(styled string, width int, visibleLen int) string {
	if visibleLen >= width {
		return styled
	}
	return strings.Repeat(" ", width-visibleLen) + styled
}

// --- Commands ---

func fetchCmd(sourceOffset, destinationOffset *offset.Service, clSvc clusterlink.Service, clCfg clusterlink.Config) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()

		// Get mirror topics
		topics, err := clSvc.ListMirrorTopics(ctx, clCfg)
		if err != nil {
			return fetchResultMsg{err: fmt.Errorf("cluster link: %w", err)}
		}

		// Get offsets for each mirror topic
		topicOffsets := make(map[string]*topicOffsetData)
		for _, t := range topics {
			sourceOffsets, err := sourceOffset.Get(t.MirrorTopicName)
			if err != nil {
				return fetchResultMsg{err: fmt.Errorf("source offsets for %s: %w", t.MirrorTopicName, err)}
			}
			destinationOffsets, err := destinationOffset.Get(t.MirrorTopicName)
			if err != nil {
				return fetchResultMsg{err: fmt.Errorf("dest offsets for %s: %w", t.MirrorTopicName, err)}
			}
			topicOffsets[t.MirrorTopicName] = &topicOffsetData{
				sourceOffsets:      sourceOffsets,
				destinationOffsets: destinationOffsets,
			}
		}

		return fetchResultMsg{topics: topics, topicOffsets: topicOffsets}
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
