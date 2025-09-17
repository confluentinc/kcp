package discover_v2

import (
	"testing"
	"time"
)

func TestCloudwatchTimeCalculator_Last24Hours(t *testing.T) {
	tests := []struct {
		name           string
		baseTime       time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-day example from requirement (15:45 on 21st)",
			baseTime:       time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 20, 15, 0, 0, 0, time.UTC), // 15:00 on 20th
			expectedEnd:    time.Date(2025, 9, 21, 15, 0, 0, 0, time.UTC), // 15:00 on 21st
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Start of hour (exactly 10:00)",
			baseTime:       time.Date(2025, 9, 17, 10, 0, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 16, 10, 0, 0, 0, time.UTC), // 10:00 on 16th
			expectedEnd:    time.Date(2025, 9, 17, 10, 0, 0, 0, time.UTC), // 10:00 on 17th
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "End of day (23:59)",
			baseTime:       time.Date(2025, 9, 17, 23, 59, 59, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 16, 23, 0, 0, 0, time.UTC), // 23:00 on 16th
			expectedEnd:    time.Date(2025, 9, 17, 23, 0, 0, 0, time.UTC), // 23:00 on 17th
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Cross month boundary",
			baseTime:       time.Date(2025, 10, 1, 8, 30, 0, 0, time.UTC), // Oct 1
			expectedStart:  time.Date(2025, 9, 30, 8, 0, 0, 0, time.UTC),  // Sep 30
			expectedEnd:    time.Date(2025, 10, 1, 8, 0, 0, 0, time.UTC),  // Oct 1
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Cross year boundary",
			baseTime:       time.Date(2025, 1, 1, 2, 15, 0, 0, time.UTC),  // Jan 1, 2025
			expectedStart:  time.Date(2024, 12, 31, 2, 0, 0, 0, time.UTC), // Dec 31, 2024
			expectedEnd:    time.Date(2025, 1, 1, 2, 0, 0, 0, time.UTC),   // Jan 1, 2025
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "Very early morning (00:30)",
			baseTime:       time.Date(2025, 9, 17, 0, 30, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 16, 0, 0, 0, 0, time.UTC), // 00:00 on 16th
			expectedEnd:    time.Date(2025, 9, 17, 0, 0, 0, 0, time.UTC), // 00:00 on 17th
			expectedPeriod: OneHourPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCloudwatchTimeCalculator(tt.baseTime)
			result, err := calc.GetTimeWindow(Last24Hours)
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
		baseTime       time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-week calculation",
			baseTime:       time.Date(2025, 9, 17, 14, 30, 0, 0, time.UTC), // Wednesday
			expectedStart:  time.Date(2025, 9, 10, 0, 0, 0, 0, time.UTC),   // Previous Wednesday
			expectedEnd:    time.Date(2025, 9, 17, 0, 0, 0, 0, time.UTC),   // Start of current day
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "Start of month",
			baseTime:       time.Date(2025, 10, 1, 9, 15, 0, 0, time.UTC), // Tuesday, Oct 1
			expectedStart:  time.Date(2025, 9, 24, 0, 0, 0, 0, time.UTC),  // Sep 24
			expectedEnd:    time.Date(2025, 10, 1, 0, 0, 0, 0, time.UTC),  // Oct 1 start of day
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "End of year",
			baseTime:       time.Date(2025, 12, 31, 23, 59, 59, 0, time.UTC), // New Year's Eve
			expectedStart:  time.Date(2025, 12, 24, 0, 0, 0, 0, time.UTC),    // Dec 24
			expectedEnd:    time.Date(2025, 12, 31, 0, 0, 0, 0, time.UTC),    // Dec 31 start of day
			expectedPeriod: DailyPeriodInSeconds,
		},
		{
			name:           "Leap year calculation",
			baseTime:       time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), // Day after leap day
			expectedStart:  time.Date(2024, 2, 23, 0, 0, 0, 0, time.UTC), // Feb 23
			expectedEnd:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),  // Mar 1 start of day
			expectedPeriod: DailyPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCloudwatchTimeCalculator(tt.baseTime)
			result, err := calc.GetTimeWindow(LastWeek)
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
		baseTime       time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-month calculation",
			baseTime:       time.Date(2025, 9, 17, 14, 30, 0, 0, time.UTC), // Sep 17
			expectedStart:  time.Date(2025, 8, 1, 0, 0, 0, 0, time.UTC),    // Aug 1
			expectedEnd:    time.Date(2025, 9, 1, 0, 0, 0, 0, time.UTC),    // Sep 1
			expectedPeriod: WeeklyPeriodInSeconds,
		},
		{
			name:           "January (after December)",
			baseTime:       time.Date(2025, 1, 15, 10, 0, 0, 0, time.UTC), // Jan 15
			expectedStart:  time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),  // Dec 1 previous year
			expectedEnd:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),   // Jan 1
			expectedPeriod: WeeklyPeriodInSeconds,
		},
		{
			name:           "March (after February in leap year)",
			baseTime:       time.Date(2024, 3, 10, 8, 45, 0, 0, time.UTC), // Mar 10, 2024 (leap year)
			expectedStart:  time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),   // Feb 1
			expectedEnd:    time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),   // Mar 1
			expectedPeriod: WeeklyPeriodInSeconds,
		},
		{
			name:           "March (after February in non-leap year)",
			baseTime:       time.Date(2025, 3, 10, 8, 45, 0, 0, time.UTC), // Mar 10, 2025 (non-leap year)
			expectedStart:  time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),   // Feb 1
			expectedEnd:    time.Date(2025, 3, 1, 0, 0, 0, 0, time.UTC),   // Mar 1
			expectedPeriod: WeeklyPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCloudwatchTimeCalculator(tt.baseTime)
			result, err := calc.GetTimeWindow(LastMonth)
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
		baseTime       time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "Mid-year calculation",
			baseTime:       time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC), // Jun 15, 2025
			expectedStart:  time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC),    // Jun 1, 2024
			expectedEnd:    time.Date(2025, 6, 1, 0, 0, 0, 0, time.UTC),    // Jun 1, 2025
			expectedPeriod: MonthlyPeriodInSeconds,
		},
		{
			name:           "Start of year",
			baseTime:       time.Date(2025, 1, 5, 9, 0, 0, 0, time.UTC), // Jan 5, 2025
			expectedStart:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), // Jan 1, 2024
			expectedEnd:    time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC), // Jan 1, 2025
			expectedPeriod: MonthlyPeriodInSeconds,
		},
		{
			name:           "End of year",
			baseTime:       time.Date(2025, 12, 25, 18, 45, 0, 0, time.UTC), // Dec 25, 2025
			expectedStart:  time.Date(2024, 12, 1, 0, 0, 0, 0, time.UTC),    // Dec 1, 2024
			expectedEnd:    time.Date(2025, 12, 1, 0, 0, 0, 0, time.UTC),    // Dec 1, 2025
			expectedPeriod: MonthlyPeriodInSeconds,
		},
		{
			name:           "Leap year to non-leap year",
			baseTime:       time.Date(2025, 2, 20, 12, 0, 0, 0, time.UTC), // Feb 20, 2025 (non-leap)
			expectedStart:  time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),   // Feb 1, 2024 (leap year)
			expectedEnd:    time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC),   // Feb 1, 2025
			expectedPeriod: MonthlyPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calc := NewCloudwatchTimeCalculator(tt.baseTime)
			result, err := calc.GetTimeWindow(LastYear)
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
