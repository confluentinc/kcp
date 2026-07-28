package metrics

import (
	"testing"
	"time"
)

func TestGetTimeWindowForGranularity(t *testing.T) {
	tests := []struct {
		name           string
		granularity    string
		endTime        time.Time
		expectedStart  time.Time
		expectedEnd    time.Time
		expectedPeriod int32
	}{
		{
			name:           "60s granularity — 15 day window at 1 minute period",
			granularity:    "60s",
			endTime:        time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 6, 15, 45, 0, 0, time.UTC),
			expectedEnd:    time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC),
			expectedPeriod: OneMinutePeriodInSeconds,
		},
		{
			name:           "60s granularity — crosses month boundary",
			granularity:    "60s",
			endTime:        time.Date(2025, 10, 5, 0, 0, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 9, 20, 0, 0, 0, 0, time.UTC),
			expectedEnd:    time.Date(2025, 10, 5, 0, 0, 0, 0, time.UTC),
			expectedPeriod: OneMinutePeriodInSeconds,
		},
		{
			name:           "5m granularity — 63 day window at 5 minute period",
			granularity:    "5m",
			endTime:        time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC),
			expectedStart:  time.Date(2025, 7, 20, 15, 45, 0, 0, time.UTC),
			expectedEnd:    time.Date(2025, 9, 21, 15, 45, 0, 0, time.UTC),
			expectedPeriod: FiveMinutePeriodInSeconds,
		},
		{
			name:           "1h granularity — 1 year window at 1 hour period",
			granularity:    "1h",
			endTime:        time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			expectedStart:  time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC),
			expectedEnd:    time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			expectedPeriod: OneHourPeriodInSeconds,
		},
		{
			name:           "1d granularity — 1 year window at 1 day period",
			granularity:    "1d",
			endTime:        time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			expectedStart:  time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC),
			expectedEnd:    time.Date(2025, 6, 15, 14, 30, 0, 0, time.UTC),
			expectedPeriod: OneDayPeriodInSeconds,
		},
		{
			name:           "1d granularity — leap year boundary",
			granularity:    "1d",
			endTime:        time.Date(2025, 2, 20, 12, 0, 0, 0, time.UTC),
			expectedStart:  time.Date(2024, 2, 20, 12, 0, 0, 0, time.UTC),
			expectedEnd:    time.Date(2025, 2, 20, 12, 0, 0, 0, time.UTC),
			expectedPeriod: OneDayPeriodInSeconds,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := GetTimeWindowForGranularity(tt.endTime, tt.granularity)
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

func TestGetTimeWindowForGranularity_InvalidGranularity(t *testing.T) {
	_, err := GetTimeWindowForGranularity(time.Now(), "30s")
	if err == nil {
		t.Fatal("expected error for unsupported granularity, got nil")
	}
}
