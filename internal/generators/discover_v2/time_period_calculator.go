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

type TimePeriodCalculator struct {
	baseTime time.Time
}

func NewTimePeriodCalculator(baseTime time.Time) *TimePeriodCalculator {
	return &TimePeriodCalculator{
		baseTime: baseTime.UTC(),
	}
}

// GetTimeWindow returns the appropriate time window based on period string
func (tpc *TimePeriodCalculator) GetTimeWindow(desiredPeriod string) (TimeWindow, error) {
	switch desiredPeriod {
	case "lastWeek":
		return tpc.lastWeek(), nil
	case "lastMonth":
		return tpc.lastMonth(), nil
	case "lastYear":
		return tpc.lastYear(), nil
	case "last24Hours":
		return tpc.last24Hours(), nil
	default:
		return TimeWindow{}, fmt.Errorf("unsupported period: %s. Supported periods: lastWeek, lastMonth, lastYear, last24Hours", desiredPeriod)
	}
}

// lastWeek returns time window for the last FULL 7 days
// End: start of current day, Start: 7 days before end, Period: 1 day
func (tpc *TimePeriodCalculator) lastWeek() TimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), tpc.baseTime.Day(), 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(0, 0, -7)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    DailyPeriodInSeconds,
	}
}

// lastMonth returns time window for the last FULL month
// End: start of current month, Start: start of previous month, Period: 1 week
func (tpc *TimePeriodCalculator) lastMonth() TimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(0, -1, 0)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    WeeklyPeriodInSeconds,
	}
}

// lastYear returns time window for the last 12 months
// End: start of current month, Start: 12 months before, Period: 1 month
func (tpc *TimePeriodCalculator) lastYear() TimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(-1, 0, 0)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    MonthlyPeriodInSeconds,
	}
}

// last24Hours returns time window for the last 24 COMPLETE hours
// End: start of current hour, Start: 24 hours before end, Period: 2 hours
func (tpc *TimePeriodCalculator) last24Hours() TimeWindow {
	// Get the start of the current hour (e.g., 15:45 -> 15:00)
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), tpc.baseTime.Day(), tpc.baseTime.Hour(), 0, 0, 0, time.UTC)
	// Go back 24 hours from end time
	startTime := endTime.Add(-24 * time.Hour)
	return TimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    TwoHoursPeriodInSeconds,
	}
}
