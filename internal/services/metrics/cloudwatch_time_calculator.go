package metrics

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type TimePeriod string

const (
	// debugging period
	OneHourPeriodInSeconds int32 = 60 * 60      // 60 seconds * 60 minutes
	DailyPeriodInSeconds   int32 = 60 * 60 * 24 // 60 seconds * 60 minutes * 24 hours

	Last24Hours TimePeriod = "last24Hours"
	LastWeek    TimePeriod = "lastWeek"
	LastMonth   TimePeriod = "lastMonth"
	LastYear    TimePeriod = "lastYear"
)

// GetTimeWindow calculates CloudWatch time windows for different periods based on a end time
func GetTimeWindow(endTime time.Time, desiredPeriod TimePeriod) (types.CloudWatchTimeWindow, error) {
	switch desiredPeriod {
	case Last24Hours:
		return calculateLast24Hours(endTime), nil
	case LastWeek:
		return calculateLastWeek(endTime), nil
	case LastMonth:
		return calculateLastMonth(endTime), nil
	case LastYear:
		return calculateLastYear(endTime), nil
	default:
		return types.CloudWatchTimeWindow{}, fmt.Errorf("unsupported time period: %s", desiredPeriod)
	}
}

// calculateLast24Hours returns time window for the last 24 hours
// End: endTime, Start: 24 hours before endTime, Period: 1 hour
func calculateLast24Hours(endTime time.Time) types.CloudWatchTimeWindow {
	startTime := endTime.Add(-24 * time.Hour)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    OneHourPeriodInSeconds,
	}
}

// calculateLastWeek returns time window for the last 7 days
// End: endTime, Start: 7 days before endTime, Period: 1 hour
func calculateLastWeek(endTime time.Time) types.CloudWatchTimeWindow {
	startTime := endTime.AddDate(0, 0, -7)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    OneHourPeriodInSeconds,
	}
}

// calculateLastMonth returns time window for the last month
// End: endTime, Start: 1 month before endTime, Period: 1 day
func calculateLastMonth(endTime time.Time) types.CloudWatchTimeWindow {
	startTime := endTime.AddDate(0, -1, 0)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    DailyPeriodInSeconds,
	}
}

// calculateLastYear returns time window for the last 12 months
// End: endTime, Start: 1 year before endTime, Period: 1 day
func calculateLastYear(endTime time.Time) types.CloudWatchTimeWindow {
	startTime := endTime.AddDate(-1, 0, 0)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    DailyPeriodInSeconds,
	}
}
