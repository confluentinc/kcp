package metrics

import (
	"fmt"
	"time"

	"github.com/confluentinc/kcp/internal/types"
)

type TimePeriod string

const (
	// debugging period
	OneMinutePeriodInSeconds int32 = 60
	FiveMinutePeriodInSeconds   int32 = 60 * 5 // 60 seconds * 5 minutes
	OneHourPeriodInSeconds     int32 = 60 * 60 // 60 seconds * 60 minutes
	OneDayPeriodInSeconds      int32 = 60 * 60 * 24 // 60 seconds * 60 minutes * 24 hours

	Last15Days TimePeriod = "last15Days"
	Last63Days    TimePeriod = "last63Days"
	Last365Days   TimePeriod = "last365Days"


)

// // GetTimeWindow calculates CloudWatch time windows for different periods based on a end time
// func GetTimeWindow(endTime time.Time, desiredPeriod TimePeriod) (types.CloudWatchTimeWindow, error) {
// 	switch desiredPeriod {
// 	case Last15Days:
// 		return calculateLast15Days(endTime), nil
// 	case Last63Days:
// 		return calculateLast63Days(endTime), nil
// 	case Last365Days:
// 		return calculateLast365Days(endTime), nil
// 	default:
// 		return types.CloudWatchTimeWindow{}, fmt.Errorf("unsupported time period: %s", desiredPeriod)
// 	}
// }

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
