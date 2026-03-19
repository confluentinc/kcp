package types

import "time"

// JMXMetrics holds time-series JMX metric data collected during a scan
type JMXMetrics struct {
	ScanDuration  string              `json:"scan_duration"`
	ScanStartTime time.Time           `json:"scan_start_time"`
	Snapshots     []JMXMetricSnapshot `json:"snapshots"`
}

// JMXMetricSnapshot is a single point-in-time reading of all JMX metrics
type JMXMetricSnapshot struct {
	Timestamp time.Time          `json:"timestamp"`
	Metrics   map[string]float64 `json:"metrics"`
}
