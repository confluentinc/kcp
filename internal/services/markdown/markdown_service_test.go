package markdown

import (
	"os"
	"strings"
	"testing"
)

func TestPrintMarkdownTable(t *testing.T) {
	// Test data with repeated values
	headers := []string{"Team", "Driver", "Nationality", "Points", "Position"}
	data := [][]string{
		{"Red Bull Racing", "Max Verstappen", "Dutch", "575", "1st"},
		{"Red Bull Racing", "Yuki Tsunoda", "Japanese", "285", "2nd"},
		{"Ferrari", "Charles Leclerc", "Monegasque", "206", "3rd"},
		{"Ferrari", "Lewis Hamilton", "British", "234", "4th"},
		{"Mercedes", "George Russell", "British", "175", "5th"},
		{"Mercedes", "Kimi Antonelli", "Italian", "97", "6th"},
		{"McLaren", "Lando Norris", "British", "205", "7th"},
		{"McLaren", "Oscar Piastri", "Australian", "97", "8th"},
	}

	t.Log("Basic table (all values shown):")
	New().AddTable(headers, data).Print()
}

func TestPrintMarkdownTableWithMissingData(t *testing.T) {
	// Test with incomplete data rows
	headers := []string{"Driver", "Team", "Points", "Wins"}
	data := [][]string{
		{"Max Verstappen", "Red Bull", "575"}, // Missing wins
		{"Lewis Hamilton", "Ferrari", "234", "0"},
		{"Charles Leclerc", "Ferrari"}, // Missing points and wins
		{"Lando Norris", "McLaren", "205", "0"},
	}

	New().AddTable(headers, data).Print()
}

func TestPrintMarkdownTableWithSkipRepeat(t *testing.T) {
	// Test with data that has repeated values in certain columns
	headers := []string{"Team", "Driver", "Nationality", "Points", "Position"}
	data := [][]string{
		{"Red Bull Racing", "Max Verstappen", "Dutch", "575", "1st"},
		{"Red Bull Racing", "Yuki Tsunoda", "Japanese", "285", "2nd"},
		{"Ferrari", "Charles Leclerc", "Monegasque", "206", "3rd"},
		{"Ferrari", "Lewis Hamilton", "British", "234", "4th"},
		{"Mercedes", "George Russell", "British", "175", "5th"},
		{"Mercedes", "Kimi Antonelli", "Italian", "97", "6th"},
		{"McLaren", "Lando Norris", "British", "205", "7th"},
		{"McLaren", "Oscar Piastri", "Australian", "97", "8th"},
		{"Aston Martin", "Fernando Alonso", "Spanish", "206", "9th"},
		{"Aston Martin", "Lance Stroll", "Canadian", "74", "10th"},
	}

	// Skip repeating values for Team (column 0) and Nationality (column 2)
	t.Log("Table with repeated values hidden:")
	New().AddTable(headers, data, 0, 2).Print()
}

func TestMarkdownBuilder(t *testing.T) {
	// Test the new builder pattern
	headers := []string{"Driver", "Team", "Points"}
	data := [][]string{
		{"Max Verstappen", "Red Bull Racing", "575"},
		{"Lewis Hamilton", "Ferrari", "234"},
		{"Charles Leclerc", "Ferrari", "206"},
		{"Lando Norris", "McLaren", "205"},
		{"Fernando Alonso", "Aston Martin", "206"},
	}

	t.Log("Builder pattern example:")
	md := New()
	md.AddHeading("F1 Championship Report 2025", 1)
	md.AddParagraph("This is a sample report showing F1 driver standings for the 2025 season.")
	md.AddTable(headers, data)
	md.AddHeading("Summary", 2)
	md.AddList([]string{"Total drivers: 5", "Average points: 285", "Teams: 5"})
	md.AddCodeBlock("fmt.Println(\"F1 Championship 2025\")", "go")
	md.AddHorizontalRule()
	md.AddParagraph("Report generated using the markdown builder.")
	md.Print()
}

func TestMarkdownBuilderWithSkipRepeat(t *testing.T) {
	// Test builder pattern with skip repeat functionality
	headers := []string{"Team", "Driver", "Nationality", "Points"}
	data := [][]string{
		{"Red Bull Racing", "Max Verstappen", "Dutch", "575"},
		{"Red Bull Racing", "Yuki Tsunoda", "Japanese", "285"},
		{"Ferrari", "Charles Leclerc", "Monegasque", "206"},
		{"Ferrari", "Lewis Hamilton", "British", "234"},
		{"Mercedes", "George Russell", "British", "175"},
		{"Mercedes", "Kimi Antonelli", "Italian", "97"},
		{"McLaren", "Lando Norris", "British", "205"},
		{"McLaren", "Oscar Piastri", "Australian", "97"},
	}

	t.Log("Builder pattern with skip repeat:")
	md := New()
	md.AddHeading("F1 Team Report 2025", 1)
	md.AddParagraph("Showing drivers by team with skip repeat on team column.")
	md.AddTable(headers, data, 0) // Skip repeat on team column
	md.AddParagraph("Notice how the team name only shows once per group.")
	md.Print()
}

func TestPrintOptions(t *testing.T) {
	// Test different print options
	headers := []string{"Name", "Value"}
	data := [][]string{
		{"Test1", "100"},
		{"Test2", "200"},
	}

	md := New()
	md.AddHeading("Print Options Test", 1)
	md.AddTable(headers, data)

	// Test file output only
	tempFile := "test_output.md"
	defer os.Remove(tempFile) // Clean up after test

	t.Log("Testing file output only:")
	err := md.Print(PrintOptions{ToTerminal: false, ToFile: tempFile})
	if err != nil {
		t.Errorf("Failed to print to file: %v", err)
	}

	// Verify file was created and has content
	if _, err := os.Stat(tempFile); os.IsNotExist(err) {
		t.Errorf("File was not created: %s", tempFile)
	}

	// Test both terminal and file output
	t.Log("Testing both terminal and file output:")
	err = md.Print(PrintOptions{ToTerminal: true, ToFile: tempFile})
	if err != nil {
		t.Errorf("Failed to print to both terminal and file: %v", err)
	}

	// Test terminal output only (default)
	t.Log("Testing terminal output only (default):")
	err = md.Print()
	if err != nil {
		t.Errorf("Failed to print to terminal: %v", err)
	}
}

func TestWriteToMethod(t *testing.T) {
	// Test the new WriteTo method with io.Writer
	md := New()
	md.AddHeading("WriteTo Test", 1)
	md.AddParagraph("This is a test of the WriteTo method with io.Writer.")
	md.AddList([]string{"Item 1", "Item 2", "Item 3"})

	// Test writing to a buffer
	var buf strings.Builder
	bytesWritten, err := md.WriteTo(&buf)
	if err != nil {
		t.Errorf("Failed to write to buffer: %v", err)
	}

	content := buf.String()
	if bytesWritten == 0 {
		t.Errorf("Expected bytes to be written, got 0")
	}

	if !strings.Contains(content, "# WriteTo Test") {
		t.Errorf("Expected content to contain heading, got: %s", content)
	}

	if !strings.Contains(content, "- Item 1") {
		t.Errorf("Expected content to contain list item, got: %s", content)
	}

	t.Log("WriteTo method test passed")
	t.Log("Content written:")
	t.Log(content)
}

func TestWriteToTerminalMethod(t *testing.T) {
	// Test the new WriteToTerminal method
	md := New()
	md.AddHeading("WriteToTerminal Test", 1)
	md.AddParagraph("This is a test of the WriteToTerminal method.")
	md.AddTable([]string{"Name", "Value"}, [][]string{{"Test", "123"}})

	t.Log("Testing WriteToTerminal (raw markdown output):")
	bytesWritten, err := md.WriteToTerminal()
	if err != nil {
		t.Errorf("Failed to write to terminal: %v", err)
	}

	if bytesWritten == 0 {
		t.Errorf("Expected bytes to be written, got 0")
	}

	t.Logf("Successfully wrote %d bytes to terminal", bytesWritten)
}

func TestWriteToTerminalWithGlamourMethod(t *testing.T) {
	// Test the new WriteToTerminalWithGlamour method
	md := New()
	md.AddHeading("WriteToTerminalWithGlamour Test", 1)
	md.AddParagraph("This is a test of the WriteToTerminalWithGlamour method.")
	md.AddTable([]string{"Driver", "Team", "Points"}, [][]string{
		{"Max Verstappen", "Red Bull", "575"},
		{"Lewis Hamilton", "Ferrari", "234"},
	})

	t.Log("Testing WriteToTerminalWithGlamour (rendered output):")
	bytesWritten, err := md.WriteToTerminalWithGlamour()
	if err != nil {
		t.Errorf("Failed to write to terminal with glamour: %v", err)
	}

	if bytesWritten == 0 {
		t.Errorf("Expected bytes to be written, got 0")
	}

	t.Logf("Successfully wrote %d bytes to terminal with glamour", bytesWritten)
}
