package types

import (
	"encoding/csv"
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestRegionCosts_AsJson(t *testing.T) {
	tests := []struct {
		name     string
		costs    *RegionCosts
		wantErr  bool
		validate func(t *testing.T, data []byte)
	}{
		{
			name: "successfully marshal empty costs",
			costs: &RegionCosts{
				Region:      "us-east-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled RegionCosts
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "us-east-1", unmarshaled.Region)
				assert.Equal(t, "MONTHLY", unmarshaled.Granularity)
			},
		},
		{
			name: "successfully marshal costs with data",
			costs: &RegionCosts{
				Region:      "us-west-2",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "DAILY",
				Tags: map[string][]string{
					"Environment": {"production"},
					"Project":     {"msk-migration"},
				},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-02T00:00:00Z",
							Service:         "AmazonMSK",
							Cost:            25.50,
							UsageType:       "MSK-BrokerInstance",
						},
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-02T00:00:00Z",
							Service:         "AmazonMSK",
							Cost:            12.75,
							UsageType:       "MSK-Storage",
						},
					},
					Total: 38.25,
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled RegionCosts
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "us-west-2", unmarshaled.Region)
				assert.Equal(t, "DAILY", unmarshaled.Granularity)
				assert.Len(t, unmarshaled.CostData.Costs, 2)
				assert.Equal(t, 38.25, unmarshaled.CostData.Total)
				assert.Len(t, unmarshaled.Tags, 2)
			},
		},
		{
			name: "successfully marshal costs with complex tags",
			costs: &RegionCosts{
				Region:      "eu-west-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags: map[string][]string{
					"Environment": {"production", "staging"},
					"Team":        {"data-engineering"},
					"CostCenter":  {"cc-12345"},
				},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            150.00,
							UsageType:       "MSK-BrokerInstance",
						},
					},
					Total: 150.00,
				},
			},
			wantErr: false,
			validate: func(t *testing.T, data []byte) {
				var unmarshaled RegionCosts
				err := json.Unmarshal(data, &unmarshaled)
				require.NoError(t, err)
				assert.Equal(t, "eu-west-1", unmarshaled.Region)
				assert.Len(t, unmarshaled.Tags, 3)
				assert.Contains(t, unmarshaled.Tags["Environment"], "production")
				assert.Contains(t, unmarshaled.Tags["Environment"], "staging")
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			data, err := tt.costs.AsJson()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)
			assert.NotNil(t, data)
			assert.True(t, len(data) > 0)

			if tt.validate != nil {
				tt.validate(t, data)
			}
		})
	}
}

func TestRegionCosts_WriteAsJson(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		costs   *RegionCosts
		wantErr bool
	}{
		{
			name: "successfully write to file",
			costs: &RegionCosts{
				Region:      "us-east-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            100.00,
							UsageType:       "MSK-BrokerInstance",
						},
					},
					Total: 100.00,
				},
			},
			wantErr: false,
		},
		{
			name: "write with empty region should succeed",
			costs: &RegionCosts{
				Region:      "",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to temp directory for testing
			originalWd, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalWd)

			err = os.Chdir(tempDir)
			require.NoError(t, err)

			err = tt.costs.WriteAsJson()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.costs.GetJsonPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content
			fileData, err := os.ReadFile(expectedPath)
			require.NoError(t, err)

			var unmarshaled RegionCosts
			err = json.Unmarshal(fileData, &unmarshaled)
			require.NoError(t, err)
			assert.Equal(t, tt.costs.Region, unmarshaled.Region)
			assert.Equal(t, tt.costs.Granularity, unmarshaled.Granularity)
		})
	}
}

func TestRegionCosts_AsMarkdown(t *testing.T) {
	tests := []struct {
		name     string
		costs    *RegionCosts
		validate func(t *testing.T, md *markdown.Markdown)
	}{
		{
			name: "generate markdown for empty costs",
			costs: &RegionCosts{
				Region:      "us-east-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{},
					Total: 0.0,
				},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
		{
			name: "generate markdown for costs with data",
			costs: &RegionCosts{
				Region:      "us-west-2",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "DAILY",
				Tags: map[string][]string{
					"Environment": {"production"},
					"Project":     {"msk-migration"},
				},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-02T00:00:00Z",
							Service:         "AmazonMSK",
							Cost:            25.50,
							UsageType:       "MSK-BrokerInstance",
						},
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-02T00:00:00Z",
							Service:         "AmazonMSK",
							Cost:            12.75,
							UsageType:       "MSK-Storage",
						},
					},
					Total: 38.25,
				},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
		{
			name: "generate markdown for costs with multiple services",
			costs: &RegionCosts{
				Region:      "eu-west-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags: map[string][]string{
					"Environment": {"production", "staging"},
					"Team":        {"data-engineering"},
				},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            150.00,
							UsageType:       "MSK-BrokerInstance",
						},
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonEC2",
							Cost:            75.00,
							UsageType:       "EC2-Instance",
						},
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            50.00,
							UsageType:       "MSK-Storage",
						},
					},
					Total: 275.00,
				},
			},
			validate: func(t *testing.T, md *markdown.Markdown) {
				assert.NotNil(t, md)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			md := tt.costs.AsMarkdown()
			assert.NotNil(t, md)

			if tt.validate != nil {
				tt.validate(t, md)
			}
		})
	}
}

func TestRegionCosts_WriteAsMarkdown(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		costs   *RegionCosts
		wantErr bool
	}{
		{
			name: "successfully write markdown to file",
			costs: &RegionCosts{
				Region:      "us-east-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            100.00,
							UsageType:       "MSK-BrokerInstance",
						},
					},
					Total: 100.00,
				},
			},
			wantErr: false,
		},
		{
			name: "write with empty region should succeed",
			costs: &RegionCosts{
				Region:      "",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to temp directory for testing
			originalWd, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalWd)

			err = os.Chdir(tempDir)
			require.NoError(t, err)

			err = tt.costs.WriteAsMarkdown(true)
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.costs.GetMarkdownPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify content contains markdown
			fileData, err := os.ReadFile(expectedPath)
			require.NoError(t, err)
			content := string(fileData)
			assert.Contains(t, content, "# AWS MSK Cost Report")
			assert.Contains(t, content, tt.costs.Region)
		})
	}
}

func TestRegionCosts_AsCSVRecords(t *testing.T) {
	tests := []struct {
		name     string
		costs    *RegionCosts
		validate func(t *testing.T, records [][]string)
	}{
		{
			name: "generate CSV records for empty costs",
			costs: &RegionCosts{
				Region:      "us-east-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{},
					Total: 0.0,
				},
			},
			validate: func(t *testing.T, records [][]string) {
				assert.NotNil(t, records)
				assert.True(t, len(records) >= 5) // SUMMARY, header, empty rows, DETAILED BREAKDOWN, header
			},
		},
		{
			name: "generate CSV records for costs with data",
			costs: &RegionCosts{
				Region:      "us-west-2",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "DAILY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-02T00:00:00Z",
							Service:         "AmazonMSK",
							Cost:            25.50,
							UsageType:       "MSK-BrokerInstance",
						},
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-02T00:00:00Z",
							Service:         "AmazonMSK",
							Cost:            12.75,
							UsageType:       "MSK-Storage",
						},
					},
					Total: 38.25,
				},
			},
			validate: func(t *testing.T, records [][]string) {
				assert.NotNil(t, records)
				// Should have: SUMMARY, header, 2 cost rows, 1 service total, 1 grand total, empty rows, DETAILED BREAKDOWN, header, 2 cost rows
				assert.True(t, len(records) >= 10)

				// Check summary section
				assert.Equal(t, "SUMMARY", records[0][0])
				assert.Equal(t, "Service", records[1][0])
				assert.Equal(t, "Usage Type", records[1][1])
				assert.Equal(t, "Total Cost (USD)", records[1][2])

				// Check detailed breakdown section
				foundDetailedBreakdown := false
				for _, record := range records {
					if len(record) > 0 && record[0] == "DETAILED BREAKDOWN" {
						foundDetailedBreakdown = true
						break
					}
				}
				assert.True(t, foundDetailedBreakdown)
			},
		},
		{
			name: "generate CSV records for costs with multiple services",
			costs: &RegionCosts{
				Region:      "eu-west-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            150.00,
							UsageType:       "MSK-BrokerInstance",
						},
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonEC2",
							Cost:            75.00,
							UsageType:       "EC2-Instance",
						},
					},
					Total: 225.00,
				},
			},
			validate: func(t *testing.T, records [][]string) {
				assert.NotNil(t, records)
				assert.True(t, len(records) >= 10)

				// Check that we have service totals
				foundServiceTotal := false
				for _, record := range records {
					if len(record) >= 2 && record[1] == "TOTAL" {
						foundServiceTotal = true
						break
					}
				}
				assert.True(t, foundServiceTotal)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			records := tt.costs.AsCSVRecords()
			assert.NotNil(t, records)

			if tt.validate != nil {
				tt.validate(t, records)
			}
		})
	}
}

func TestRegionCosts_WriteAsCSV(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	tests := []struct {
		name    string
		costs   *RegionCosts
		wantErr bool
	}{
		{
			name: "successfully write CSV to file",
			costs: &RegionCosts{
				Region:      "us-east-1",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
				Tags:        map[string][]string{},
				CostData: CostData{
					Costs: []Cost{
						{
							TimePeriodStart: "2023-01-01T00:00:00Z",
							TimePeriodEnd:   "2023-01-31T23:59:59Z",
							Service:         "AmazonMSK",
							Cost:            100.00,
							UsageType:       "MSK-BrokerInstance",
						},
					},
					Total: 100.00,
				},
			},
			wantErr: false,
		},
		{
			name: "write with empty region should succeed",
			costs: &RegionCosts{
				Region:      "",
				StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
				EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
				Granularity: "MONTHLY",
			},
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Change to temp directory for testing
			originalWd, err := os.Getwd()
			require.NoError(t, err)
			defer os.Chdir(originalWd)

			err = os.Chdir(tempDir)
			require.NoError(t, err)

			err = tt.costs.WriteAsCSV()
			if tt.wantErr {
				assert.Error(t, err)
				return
			}

			assert.NoError(t, err)

			// Verify file was created and contains expected content
			expectedPath := tt.costs.GetCSVPath()
			fileInfo, err := os.Stat(expectedPath)
			require.NoError(t, err)
			assert.True(t, fileInfo.Size() > 0)

			// Read and verify CSV content
			file, err := os.Open(expectedPath)
			require.NoError(t, err)
			defer file.Close()

			reader := csv.NewReader(file)
			records, err := reader.ReadAll()
			require.NoError(t, err)
			assert.True(t, len(records) > 0)
			assert.Equal(t, "SUMMARY", records[0][0])
		})
	}
}

func TestRegionCosts_Integration(t *testing.T) {
	// Create a temporary directory for test files
	tempDir := t.TempDir()

	// Create a comprehensive test costs object
	costs := &RegionCosts{
		Region:      "us-east-1",
		StartDate:   time.Date(2023, 1, 1, 0, 0, 0, 0, time.UTC),
		EndDate:     time.Date(2023, 1, 31, 23, 59, 59, 0, time.UTC),
		Granularity: "MONTHLY",
		Tags: map[string][]string{
			"Environment": {"production"},
			"Project":     {"msk-migration"},
			"Team":        {"data-engineering"},
		},
		CostData: CostData{
			Costs: []Cost{
				{
					TimePeriodStart: "2023-01-01T00:00:00Z",
					TimePeriodEnd:   "2023-01-31T23:59:59Z",
					Service:         "AmazonMSK",
					Cost:            150.00,
					UsageType:       "MSK-BrokerInstance",
				},
				{
					TimePeriodStart: "2023-01-01T00:00:00Z",
					TimePeriodEnd:   "2023-01-31T23:59:59Z",
					Service:         "AmazonMSK",
					Cost:            50.00,
					UsageType:       "MSK-Storage",
				},
				{
					TimePeriodStart: "2023-01-01T00:00:00Z",
					TimePeriodEnd:   "2023-01-31T23:59:59Z",
					Service:         "AmazonEC2",
					Cost:            75.00,
					UsageType:       "EC2-Instance",
				},
			},
			Total: 275.00,
		},
	}

	// Test JSON serialization
	t.Run("JSON serialization", func(t *testing.T) {
		jsonData, err := costs.AsJson()
		require.NoError(t, err)
		assert.NotNil(t, jsonData)

		// Verify we can unmarshal it back
		var unmarshaled RegionCosts
		err = json.Unmarshal(jsonData, &unmarshaled)
		require.NoError(t, err)
		assert.Equal(t, costs.Region, unmarshaled.Region)
		assert.Equal(t, costs.Granularity, unmarshaled.Granularity)
		assert.Len(t, unmarshaled.CostData.Costs, 3)
		assert.Equal(t, costs.CostData.Total, unmarshaled.CostData.Total)
		assert.Len(t, unmarshaled.Tags, 3)
	})

	// Test JSON file writing
	t.Run("JSON file writing", func(t *testing.T) {
		// Change to temp directory for testing
		originalWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(originalWd)

		err = os.Chdir(tempDir)
		require.NoError(t, err)

		err = costs.WriteAsJson()
		require.NoError(t, err)

		// Verify file exists and has content
		expectedPath := costs.GetJsonPath()
		fileInfo, err := os.Stat(expectedPath)
		require.NoError(t, err)
		assert.True(t, fileInfo.Size() > 0)
	})

	// Test Markdown generation
	t.Run("Markdown generation", func(t *testing.T) {
		md := costs.AsMarkdown()
		require.NotNil(t, md)
	})

	// Test Markdown file writing
	t.Run("Markdown file writing", func(t *testing.T) {
		// Change to temp directory for testing
		originalWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(originalWd)

		err = os.Chdir(tempDir)
		require.NoError(t, err)

		err = costs.WriteAsMarkdown(true)
		require.NoError(t, err)

		// Verify file exists and has content
		expectedPath := costs.GetMarkdownPath()
		fileInfo, err := os.Stat(expectedPath)
		require.NoError(t, err)
		assert.True(t, fileInfo.Size() > 0)

		// Verify content contains expected markdown
		fileData, err := os.ReadFile(expectedPath)
		require.NoError(t, err)
		content := string(fileData)
		assert.Contains(t, content, "# AWS MSK Cost Report")
		assert.Contains(t, content, "us-east-1")
		assert.Contains(t, content, "AmazonMSK")
	})

	// Test CSV generation
	t.Run("CSV generation", func(t *testing.T) {
		records := costs.AsCSVRecords()
		require.NotNil(t, records)
		assert.True(t, len(records) > 0)
	})

	// Test CSV file writing
	t.Run("CSV file writing", func(t *testing.T) {
		// Change to temp directory for testing
		originalWd, err := os.Getwd()
		require.NoError(t, err)
		defer os.Chdir(originalWd)

		err = os.Chdir(tempDir)
		require.NoError(t, err)

		err = costs.WriteAsCSV()
		require.NoError(t, err)

		// Verify file exists and has content
		expectedPath := costs.GetCSVPath()
		fileInfo, err := os.Stat(expectedPath)
		require.NoError(t, err)
		assert.True(t, fileInfo.Size() > 0)

		// Verify CSV content
		file, err := os.Open(expectedPath)
		require.NoError(t, err)
		defer file.Close()

		reader := csv.NewReader(file)
		records, err := reader.ReadAll()
		require.NoError(t, err)
		assert.True(t, len(records) > 0)
		assert.Equal(t, "SUMMARY", records[0][0])

		// Find DETAILED BREAKDOWN section
		foundDetailedBreakdown := false
		for _, record := range records {
			if len(record) > 0 && record[0] == "DETAILED BREAKDOWN" {
				foundDetailedBreakdown = true
				break
			}
		}
		assert.True(t, foundDetailedBreakdown)
	})
}

func TestCost_Struct(t *testing.T) {
	cost := Cost{
		TimePeriodStart: "2023-01-01T00:00:00Z",
		TimePeriodEnd:   "2023-01-31T23:59:59Z",
		Service:         "AmazonMSK",
		Cost:            150.00,
		UsageType:       "MSK-BrokerInstance",
	}

	assert.Equal(t, "2023-01-01T00:00:00Z", cost.TimePeriodStart)
	assert.Equal(t, "2023-01-31T23:59:59Z", cost.TimePeriodEnd)
	assert.Equal(t, "AmazonMSK", cost.Service)
	assert.Equal(t, 150.00, cost.Cost)
	assert.Equal(t, "MSK-BrokerInstance", cost.UsageType)
}

func TestCostData_Struct(t *testing.T) {
	costData := CostData{
		Costs: []Cost{
			{
				TimePeriodStart: "2023-01-01T00:00:00Z",
				TimePeriodEnd:   "2023-01-31T23:59:59Z",
				Service:         "AmazonMSK",
				Cost:            150.00,
				UsageType:       "MSK-BrokerInstance",
			},
		},
		Total: 150.00,
	}

	assert.Len(t, costData.Costs, 1)
	assert.Equal(t, 150.00, costData.Total)
	assert.Equal(t, "AmazonMSK", costData.Costs[0].Service)
}
