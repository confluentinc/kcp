package markdown

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"

	"github.com/charmbracelet/glamour"
)

// PrintOptions configures where and how to print the markdown
type PrintOptions struct {
	ToTerminal bool   // Print to terminal with glamour rendering
	ToFile     string // File path to save raw markdown (empty string = don't save to file)
}

// DefaultPrintOptions returns default options (terminal only)
func DefaultPrintOptions() PrintOptions {
	return PrintOptions{
		ToTerminal: true,
		ToFile:     "",
	}
}

// Markdown represents a markdown document that can be built incrementally
type Markdown struct {
	content strings.Builder
}

// New creates a new Markdown instance
func New() *Markdown {
	return &Markdown{
		content: strings.Builder{},
	}
}

// AddHeading adds a heading with the specified level (1-6)
func (m *Markdown) AddHeading(text string, level int) *Markdown {
	if level < 1 || level > 6 {
		level = 1
	}
	prefix := strings.Repeat("#", level)
	m.content.WriteString(fmt.Sprintf("%s %s\n\n", prefix, text))
	return m
}

// AddParagraph adds a paragraph of text
func (m *Markdown) AddParagraph(text string) *Markdown {
	m.content.WriteString(fmt.Sprintf("%s\n\n", text))
	return m
}

// AddTable adds a table with the given headers and data
// skipRepeatColumns is an optional slice of column indices (0-based) that should not repeat values
func (m *Markdown) AddTable(headers []string, data [][]string, skipRepeatColumns ...int) *Markdown {
	if len(headers) == 0 {
		return m
	}

	// Write headers
	m.content.WriteString("| " + strings.Join(headers, " | ") + " |\n")

	// Write separator row
	separators := make([]string, len(headers))
	for i := range headers {
		separators[i] = "---"
	}
	m.content.WriteString("| " + strings.Join(separators, " | ") + " |\n")

	// Create a map for quick lookup of columns to skip
	skipMap := make(map[int]bool)
	for _, col := range skipRepeatColumns {
		if col >= 0 && col < len(headers) {
			skipMap[col] = true
		}
	}

	// Track previous values for columns that should not repeat
	previousValues := make([]string, len(headers))

	// Write data rows
	for _, row := range data {
		// Pad row to match header length if needed
		paddedRow := make([]string, len(headers))
		copy(paddedRow, row)

		// Process each column
		for col := 0; col < len(headers); col++ {
			if skipMap[col] && col < len(paddedRow) {
				// Check if current value is same as previous
				if paddedRow[col] == previousValues[col] {
					paddedRow[col] = "" // Empty cell for repeated value
				} else {
					previousValues[col] = paddedRow[col] // Update previous value
				}
			}
		}

		m.content.WriteString("| " + strings.Join(paddedRow, " | ") + " |\n")
	}

	m.content.WriteString("\n")
	return m
}

// AddCodeBlock adds a code block with optional language specification
func (m *Markdown) AddCodeBlock(code string, language string) *Markdown {
	if language != "" {
		m.content.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", language, code))
	} else {
		m.content.WriteString(fmt.Sprintf("```\n%s\n```\n\n", code))
	}
	return m
}

// AddList adds a list of items
func (m *Markdown) AddList(items []string) *Markdown {
	for _, item := range items {
		m.content.WriteString(fmt.Sprintf("- %s\n", item))
	}
	m.content.WriteString("\n")
	return m
}

// AddHorizontalRule adds a horizontal rule
func (m *Markdown) AddHorizontalRule() *Markdown {
	m.content.WriteString("---\n\n")
	return m
}

// String returns the markdown content as a string
func (m *Markdown) String() string {
	return m.content.String()
}

// WriteTo writes the markdown content to the provided io.Writer
func (m *Markdown) WriteTo(w io.Writer) (int64, error) {
	content := m.content.String()
	n, err := w.Write([]byte(content))
	return int64(n), err
}

// WriteToTerminal writes the raw markdown content to stdout without glamour rendering
func (m *Markdown) WriteToTerminal() (int64, error) {
	return m.WriteTo(os.Stdout)
}

// WriteToTerminalWithGlamour writes the rendered markdown content to stdout using glamour
func (m *Markdown) WriteToTerminalWithGlamour() (int64, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(180),
	)
	if err != nil {
		// Fallback to raw output if glamour fails
		return m.WriteToTerminal()
	}

	out, err := renderer.Render(m.content.String())
	if err != nil {
		// Fallback to raw output if glamour fails
		return m.WriteToTerminal()
	}

	n, err := os.Stdout.Write([]byte(out + "\n"))
	return int64(n), err
}

// Print renders the markdown and outputs it according to the provided options
func (m *Markdown) Print(opts ...PrintOptions) error {
	var options PrintOptions
	if len(opts) > 0 {
		options = opts[0]
	} else {
		options = DefaultPrintOptions()
	}

	// Print to terminal if requested
	if options.ToTerminal {
		_, err := m.WriteToTerminalWithGlamour()
		if err != nil {
			return fmt.Errorf("failed to write to terminal: %v", err)
		}
	}

	// Save to file if requested
	if options.ToFile != "" {
		file, err := os.Create(options.ToFile)
		if err != nil {
			return fmt.Errorf("failed to create file %s: %v", options.ToFile, err)
		}
		defer file.Close()

		_, err = m.WriteTo(file)
		if err != nil {
			return fmt.Errorf("failed to write markdown to file %s: %v", options.ToFile, err)
		}
		slog.Info("Markdown saved to file", "file", options.ToFile)
	}

	return nil
}

// Render returns the rendered markdown as a string (without printing)
func (m *Markdown) Render() (string, error) {
	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(180),
	)
	if err != nil {
		return "", fmt.Errorf("failed to create glamour renderer: %v", err)
	}

	out, err := renderer.Render(m.content.String())
	if err != nil {
		return "", fmt.Errorf("failed to render markdown: %v", err)
	}

	return out, nil
}
