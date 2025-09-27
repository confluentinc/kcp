package metrics

import (
	"testing"
	"time"
)

func TestCloudwatchTimeCalculator_Last24Hours(t *testing.T) {
	tests := []struct {
		name           string
		endTime        time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-day example from requirement (15:45 on 21st)",
			endTime:        time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 20, 15, 45, 0, 0, time.UTC), // 24 hours before endTime
			expectedEnd:    time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Start of hour (exactly 10:00)",
			endTime:        time.Date(2025, 9, 17, 10, 0, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 16, 10, 0, 0, 0, time.UTC), // 24 hours before endTime
			expectedEnd:    time.Date(2025, 9, 17, 10, 0, 0, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "End of day (23:59)",
			endTime:        time.Date(2025, 9, 17, 23, 59, 59, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 16, 23, 59, 59, 0, time.UTC), // 24 hours before baseTime
			expectedEnd:    time.Date(2025, 9, 17, 23, 59, 59, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Cross month boundary",
			endTime:        time.Date(2025, 10, 1, 8, 30, 0, 0, time.UTC), // Oct 1
			expectedStart:  time.Date(2025, 9, 30, 8, 30, 0, 0, time.UTC), // 24 hours before baseTime
			expectedEnd:    time.Date(2025, 10, 1, 8, 30, 0, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Cross year boundary",
			endTime:        time.Date(2025, 1, 1, 2, 15, 0, 0, time.UTC),   // Jan 1, 2025
			expectedStart:  time.Date(2024, 12, 31, 2, 15, 0, 0, time.UTC), // 24 hours before baseTime
			expectedEnd:    time.Date(2025, 1, 1, 2, 15, 0, 0, time.UTC),   // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Very early morning (00:30)",
			endTime:        time.Date(2025, 9, 17, 0, 30, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 16, 0, 30, 0, 0, time.UTC), // 24 hours before baseTime
			expectedEnd:    time.Date(2025, 9, 17, 0, 30, 0, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetTimeWindow(tt.endTime, Last24Hours)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !result.StartTime.Equal(tt.expectedStart) {
				t.Errorf("StartTime = %v, want %v", result.StartTime, tt.expectedStart)
			}
			if !result.EndTime.Equal(tt.expectedEnd) {
				t.Errorf("EndTime = %v, want %v", result.EndTime, tt.expectedEnd)
			}
			if result.Period != tt.expectedPeriod {
				t.Errorf("Period = %v, want %v", result.Period, tt.expectedPeriod)
			}

			// Verify it's exactly 24 hours
			duration := result.EndTime.Sub(result.StartTime)
			if duration != 24*time.Hour {
				t.Errorf("Duration = %v, want %v", duration, 24*time.Hour)
			}
		})
	}
}

func TestCloudwatchTimeCalculator_LastWeek(t *testing.T) {
	tests := []struct {
		name           string
		endTime        time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-week calculation",
			endTime:        time.Date(2025, 9, 17, 14, 30, 0, 0, time.UTC), // Wednesday
			expectedStart:  time.Date(2025, 9, 10, 14, 30, 0, 0, time.UTC), // 7 days before baseTime
			expectedEnd:    time.Date(2025, 9, 17, 14, 30, 0, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Start of month",
			endTime:        time.Date(2025, 10, 1, 9, 15, 0, 0, time.UTC), // Tuesday, Oct 1
			expectedStart:  time.Date(2025, 9, 24, 9, 15, 0, 0, time.UTC), // 7 days before baseTime
			expectedEnd:    time.Date(2025, 10, 1, 9, 15, 0, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "End of year",
			endTime:        time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), // New Year's Eve
			expectedStart:  time.Date(2025, 12, 24, 23, 59, 59, 0, time.UTC), // 7 days before baseTime
			expectedEnd:    time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Leap year calculation",
			endTime:        time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),  // Day after leap day
			expectedStart:  time.Date(2024, 2, 23, 12, 0, 0, 0, time.UTC), // 7 days before baseTime
			expectedEnd:    time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC),  // baseTime
			expectedPeriod: OneHourPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetTimeWindow(tt.endTime, LastWeek)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !result.StartTime.Equal(tt.expectedStart) {
				t.Errorf("StartTime = %v, want %v", result.StartTime, tt.expectedStart)
			}
			if !result.EndTime.Equal(tt.expectedEnd) {
				t.Errorf("EndTime = %v, want %v", result.EndTime, tt.expectedEnd)
			}
			if result.Period != tt.expectedPeriod {
				t.Errorf("Period = %v, want %v", result.Period, tt.expectedPeriod)
			}
		})
	}
}

func TestCloudwatchTimeCalculator_LastMonth(t *testing.T) {
	tests := []struct {
		name           string
		endTime        time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-month calculation",
			endTime:        time.Date(2025, 9, 17, 14, 30, 0, 0, time.UTC), // Sep 17
			expectedStart:  time.Date(2025, 8, 17, 14, 30, 0, 0, time.UTC), // 1 month before baseTime
			expectedEnd:    time.Date(2025, 9, 17, 14, 30, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "January (after December)",
			endTime:        time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),  // Jan 15
			expectedStart:  time.Date(2024, 12, 15, 10, 0, 0, 0, time.UTC), // 1 month before baseTime
			expectedEnd:    time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC),  // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "March (after February in leap year)",
			endTime:        time.Date(2024, 3, 10, 8, 45, 0, 0, time.UTC), // Mar 10, 2024 (leap year)
			expectedStart:  time.Date(2024, 2, 10, 8, 45, 0, 0, time.UTC), // 1 month before baseTime
			expectedEnd:    time.Date(2024, 3, 10, 8, 45, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "March (after February in non-leap year)",
			endTime:        time.Date(2025, 3, 10, 8, 45, 0, 0, time.UTC), // Mar 10, 2025 (non-leap year)
			expectedStart:  time.Date(2025, 2, 10, 8, 45, 0, 0, time.UTC), // 1 month before baseTime
			expectedEnd:    time.Date(2025, 3, 10, 8, 45, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetTimeWindow(tt.endTime, LastMonth)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !result.StartTime.Equal(tt.expectedStart) {
				t.Errorf("StartTime = %v, want %v", result.StartTime, tt.expectedStart)
			}
			if !result.EndTime.Equal(tt.expectedEnd) {
				t.Errorf("EndTime = %v, want %v", result.EndTime, tt.expectedEnd)
			}
			if result.Period != tt.expectedPeriod {
				t.Errorf("Period = %v, want %v", result.Period, tt.expectedPeriod)
			}
		})
	}
}

func TestCloudwatchTimeCalculator_LastYear(t *testing.T) {
	tests := []struct {
		name           string
		endTime        time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-year calculation",
			endTime:        time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC), // Jun 15, 2025
			expectedStart:  time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC), // 1 year before baseTime
			expectedEnd:    time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "Start of year",
			endTime:        time.Date(2025, 1, 5, 9, 0, 0, 0, time.UTC), // Jan 5, 2025
			expectedStart:  time.Date(2024, 1, 5, 9, 0, 0, 0, time.UTC), // 1 year before baseTime
			expectedEnd:    time.Date(2025, 1, 5, 9, 0, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "End of year",
			endTime:        time.Date(2025, 12, 25, 18, 45, 0, 0, time.UTC), // Dec 25, 2025
			expectedStart:  time.Date(2024, 12, 25, 18, 45, 0, 0, time.UTC), // 1 year before baseTime
			expectedEnd:    time.Date(2025, 12, 25, 18, 45, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "Leap year to non-leap year",
			endTime:        time.Date(2025, 2, 20, 12, 0, 0, 0, time.UTC), // Feb 20, 2025 (non-leap)
			expectedStart:  time.Date(2024, 2, 20, 12, 0, 0, 0, time.UTC), // 1 year before baseTime (leap year)
			expectedEnd:    time.Date(2025, 2, 20, 12, 0, 0, 0, time.UTC), // baseTime
			expectedPeriod: DailyPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetTimeWindow(tt.endTime, LastYear)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if !result.StartTime.Equal(tt.expectedStart) {
				t.Errorf("StartTime = %v, want %v", result.StartTime, tt.expectedStart)
			}
			if !result.EndTime.Equal(tt.expectedEnd) {
				t.Errorf("EndTime = %v, want %v", result.EndTime, tt.expectedEnd)
			}
			if result.Period != tt.expectedPeriod {
				t.Errorf("Period = %v, want %v", result.Period, tt.expectedPeriod)
			}
		})
	}
}
