package discover_v2

import (
	"fmt"
	"time"
)

const (
	// testing using daily periond with 7 days of data
	DailyPeriodInSeconds int32 = 60 * 60 * 24 // 60 seconds * 60 minutes * 24 hours
	// we will want to use monthly period with 12 months of data
	MonthlyPeriodInSeconds int32 = 60 * 60 * 24 * 30 // 60 seconds * 60 minutes * 24 hours * 30 days
	// debugging period
	TwoHoursPeriodInSeconds int32 = 60 * 60 * 2 // 60 seconds * 60 minutes * 2 hours

	// Period constant for weekly periods (specific to time period calculations)
	WeeklyPeriodInSeconds int32 = 60 * 60 * 24 * 7 // 60 seconds * 60 minutes * 24 hours * 7 days
)

// TimeWindow represents a time period with start, end times and the appropriate CloudWatch period
type TimeWindow struct {
	StartTime time.Time
	EndTime   time.Time
	Period    int32
}

// TimePeriodCalculator handles calculation of time windows for metrics collection
type TimePeriodCalculator struct {
	baseTime time.Time
}

// NewTimePeriodCalculator creates a new TimePeriodCalculator with the current time as base
func NewTimePeriodCalculator() *TimePeriodCalculator {
	return &TimePeriodCalculator{
		baseTime: time.Now().UTC(),
	}
}

// NewTimePeriodCalculatorWithBase creates a TimePeriodCalculator with a specific base time (useful for testing)
func NewTimePeriodCalculatorWithBase(baseTime time.Time) *TimePeriodCalculator {
	return &TimePeriodCalculator{
		baseTime: baseTime.UTC(),
	}
}

// LastWeek returns time window for the last FULL 7 days
// End: start of current day, Start: 7 days before end, Period: 1 day
func (tpc *TimePeriodCalculator) LastWeek() TimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), tpc.baseTime.Day(), 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(0, 0, -7)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    DailyPeriodInSeconds,
	}
}

// LastMonth returns time window for the last FULL month
// End: start of current month, Start: start of previous month, Period: 1 week
func (tpc *TimePeriodCalculator) LastMonth() TimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(0, -1, 0)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    WeeklyPeriodInSeconds,
	}
}

// LastYear returns time window for the last 12 months
// End: start of current month, Start: 12 months before, Period: 1 month
func (tpc *TimePeriodCalculator) LastYear() TimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(-1, 0, 0)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    MonthlyPeriodInSeconds,
	}
}

// GetTimeWindow returns the appropriate time window based on period string
func (tpc *TimePeriodCalculator) GetTimeWindow(periodStr string) (TimeWindow, error) {
	switch periodStr {
	case "lastWeek":
		return tpc.LastWeek(), nil
	case "lastMonth":
		return tpc.LastMonth(), nil
	case "lastYear":
		return tpc.LastYear(), nil
	default:
		return TimeWindow{}, fmt.Errorf("unsupported period: %s. Supported periods: lastWeek, lastMonth, lastYear", periodStr)
	}
}
