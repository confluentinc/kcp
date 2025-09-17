package discover_v2

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type TimePeriod string

const (
	// debugging period
	OneHourPeriodInSeconds int32 = 60 * 60 // 60 seconds * 60 minutes
	// testing using daily period with 7 days of data
	DailyPeriodInSeconds   int32 = 60 * 60 * 24      // 60 seconds * 60 minutes * 24 hours
	MonthlyPeriodInSeconds int32 = 60 * 60 * 24 * 30 // 60 seconds * 60 minutes * 24 hours * 30 days
	WeeklyPeriodInSeconds  int32 = 60 * 60 * 24 * 7  // 60 seconds * 60 minutes * 24 hours * 7 days

	Last24Hours TimePeriod = "last24Hours"
	LastWeek    TimePeriod = "lastWeek"
	LastMonth   TimePeriod = "lastMonth"
	LastYear    TimePeriod = "lastYear"
)

type CloudwatchTimeCalculator struct {
	baseTime time.Time
}

func NewCloudwatchTimeCalculator(baseTime time.Time) *CloudwatchTimeCalculator {
	return &CloudwatchTimeCalculator{
		baseTime: baseTime.UTC(),
	}
}

func (tpc *CloudwatchTimeCalculator) GetTimeWindow(desiredPeriod TimePeriod) (types.CloudWatchTimeWindow, error) {
	switch desiredPeriod {
	case Last24Hours:
		return tpc.last24Hours(), nil
	case LastWeek:
		return tpc.lastWeek(), nil
	case LastMonth:
		return tpc.lastMonth(), nil
	case LastYear:
		return tpc.lastYear(), nil
	default:
		return types.CloudWatchTimeWindow{}, fmt.Errorf("unsupported period: %s. Supported periods: lastWeek, lastMonth, lastYear, last24Hours", desiredPeriod)
	}
}

// last24Hours returns time window for the last 24 COMPLETE hours
// End: start of current hour, Start: 24 hours before end, Period: 1 hour
func (tpc *CloudwatchTimeCalculator) last24Hours() types.CloudWatchTimeWindow {
	// Get the start of the current hour (e.g., 15:45 -> 15:00)
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), tpc.baseTime.Day(), tpc.baseTime.Hour(), 0, 0, 0, time.UTC)
	// Go back 24 hours from end time
	startTime := endTime.Add(-24 * time.Hour)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    OneHourPeriodInSeconds,
	}
}

// lastWeek returns time window for the last FULL 7 days
// End: start of current day, Start: 7 days before end, Period: 1 day
func (tpc *CloudwatchTimeCalculator) lastWeek() types.CloudWatchTimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), tpc.baseTime.Day(), 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(0, 0, -7)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    DailyPeriodInSeconds,
	}
}

// lastMonth returns time window for the last FULL month
// End: start of current month, Start: start of previous month, Period: 1 week
func (tpc *CloudwatchTimeCalculator) lastMonth() types.CloudWatchTimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(0, -1, 0)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    WeeklyPeriodInSeconds,
	}
}

// lastYear returns time window for the last 12 months
// End: start of current month, Start: 12 months before, Period: 1 month
func (tpc *CloudwatchTimeCalculator) lastYear() types.CloudWatchTimeWindow {
	endTime := time.Date(tpc.baseTime.Year(), tpc.baseTime.Month(), 1, 0, 0, 0, 0, time.UTC)
	startTime := endTime.AddDate(-1, 0, 0)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    MonthlyPeriodInSeconds,
	}
}
