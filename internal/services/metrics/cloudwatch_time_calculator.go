package metrics

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

// CloudWatch query periods (in seconds) selected per metrics granularity.
const (
	OneMinutePeriodInSeconds  int32 = 60
	FiveMinutePeriodInSeconds int32 = 60 * 5       // 60 seconds * 5 minutes
	OneHourPeriodInSeconds    int32 = 60 * 60      // 60 seconds * 60 minutes
	OneDayPeriodInSeconds     int32 = 60 * 60 * 24 // 60 seconds * 60 minutes * 24 hours
)

// GetTimeWindowForGranularity returns the CloudWatch query window ending at
// endTime for the requested metrics granularity. The window length is bounded
// by CloudWatch's per-period data retention: 60s→15 days, 5m→63 days, and both
// 1h and 1d→365 days. The returned window's Period matches the granularity.
func GetTimeWindowForGranularity(endTime time.Time, granularity string) (types.CloudWatchTimeWindow, error) {
	switch granularity {
	case "60s":
		return calculateLast15Days(endTime), nil
	case "5m":
		return calculateLast63Days(endTime), nil
	case "1h":
		return calculateLast365Days(endTime, OneHourPeriodInSeconds), nil
	case "1d":
		return calculateLast365Days(endTime, OneDayPeriodInSeconds), nil
	default:
		return types.CloudWatchTimeWindow{}, fmt.Errorf("unsupported metrics granularity: %s", granularity)
	}
}

func calculateLast15Days(endTime time.Time) types.CloudWatchTimeWindow {
	startTime := endTime.AddDate(0, 0, -15)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    OneMinutePeriodInSeconds,
	}
}

func calculateLast63Days(endTime time.Time) types.CloudWatchTimeWindow {
	startTime := endTime.AddDate(0, 0, -63)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    FiveMinutePeriodInSeconds,
	}
}

func calculateLast365Days(endTime time.Time, period int32) types.CloudWatchTimeWindow {
	startTime := endTime.AddDate(-1, 0, 0)
	return types.CloudWatchTimeWindow{
		StartTime: startTime,
		EndTime:   endTime,
		Period:    period,
	}
}
