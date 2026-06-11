package types

import (
	"fmt"
	"time"

	cloudwatchtypes "github.com/aws/aws-sdk-go-v2/service/cloudwatch/types"
)

// ----- metrics -----
type BrokerType string

const (
	BrokerTypeExpress  BrokerType = "express"
	BrokerTypeStandard BrokerType = "standard"
)

type ClusterMetrics struct {
	MetricMetadata MetricMetadata                     `json:"metadata"`
	Results        []cloudwatchtypes.MetricDataResult `json:"results"`
	QueryInfo      []MetricQueryInfo                  `json:"query_info"`
}

type MetricMetadata struct {
	ClusterType          string    `json:"cluster_type"`
	NumberOfBrokerNodes  int       `json:"number_of_broker_nodes"`
	KafkaVersion         string    `json:"kafka_version"`
	BrokerAzDistribution string    `json:"broker_az_distribution"`
	EnhancedMonitoring   string    `json:"enhanced_monitoring"`
	StartDate            time.Time `json:"start_date"`
	EndDate              time.Time `json:"end_date"`
	Period               int32     `json:"period"`

	FollowerFetching bool       `json:"follower_fetching"`
	InstanceType     string     `json:"instance_type"`
	TieredStorage    bool       `json:"tiered_storage"`
	BrokerType       BrokerType `json:"broker_type"`
}

type CloudWatchTimeWindow struct {
	StartTime time.Time
	EndTime   time.Time
	Period    int32
}

// MetricBackend represents the metrics collection backend
type MetricBackend string

const (
	MetricBackendCloudWatch MetricBackend = "cloudwatch"
	MetricBackendJolokia    MetricBackend = "jolokia"
	MetricBackendPrometheus MetricBackend = "prometheus"
)

type MetricQueryInfo struct {
	MetricName string        `json:"metric_name"`
	SourceType MetricBackend `json:"source_type,omitempty"`

	// CloudWatch fields
	Namespace         string `json:"namespace,omitempty"`
	Dimensions        string `json:"dimensions,omitempty"`
	Statistic         string `json:"statistic,omitempty"`
	Period            int32  `json:"period,omitempty"`
	SearchExpression  string `json:"search_expression,omitempty"`
	MathExpression    string `json:"math_expression,omitempty"`
	AWSCLICommand     string `json:"aws_cli_command,omitempty"`
	ConsoleSourceJSON string `json:"console_source_json,omitempty"`

	// Jolokia fields
	MBeanPath  string `json:"mbean_path,omitempty"`
	JolokiaURL string `json:"jolokia_url,omitempty"`

	// Prometheus fields
	PromQLQuery          string `json:"promql_query,omitempty"`
	PrometheusURL        string `json:"prometheus_url,omitempty"`
	PrometheusMetricName string `json:"prometheus_metric_name,omitempty"`

	// Shared fields
	CurlCommand     string            `json:"curl_command,omitempty"` // Jolokia curl or Prometheus API curl
	QueryDuration   string            `json:"query_duration,omitempty"`
	AggregationNote string            `json:"aggregation_note"`
	LabelFilter     map[string]string `json:"label_filter,omitempty"`
}

// FormatQueryDuration formats a duration for display, using days when >= 24h.
// Examples: "5m", "2h30m", "7d2h", "30d".
func FormatQueryDuration(d time.Duration) string {
	if d < 24*time.Hour {
		// Under a day: use compact h/m/s, dropping zero components
		d = d.Round(time.Second)
		h := int(d.Hours())
		m := int(d.Minutes()) % 60
		s := int(d.Seconds()) % 60
		switch {
		case h > 0 && m > 0:
			return fmt.Sprintf("%dh%dm", h, m)
		case h > 0:
			return fmt.Sprintf("%dh", h)
		case m > 0 && s > 0:
			return fmt.Sprintf("%dm%ds", m, s)
		case m > 0:
			return fmt.Sprintf("%dm", m)
		default:
			return fmt.Sprintf("%ds", s)
		}
	}
	days := int(d.Hours()) / 24
	remaining := d - time.Duration(days)*24*time.Hour
	hours := int(remaining.Hours())
	mins := int(remaining.Minutes()) % 60
	switch {
	case hours > 0 && mins > 0:
		return fmt.Sprintf("%dd%dh%dm", days, hours, mins)
	case hours > 0:
		return fmt.Sprintf("%dd%dh", days, hours)
	case mins > 0:
		return fmt.Sprintf("%dd%dm", days, mins)
	default:
		return fmt.Sprintf("%dd", days)
	}
}
