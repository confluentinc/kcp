package types

import (
	"time"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
)

type CostQueryTimePeriod struct {
	Start string `json:"start"`
	End   string `json:"end"`
}

type CostQueryInfo struct {
	TimePeriod      CostQueryTimePeriod `json:"time_period"`
	Granularity     string              `json:"granularity"`
	Services        []string            `json:"services"`
	Regions         []string            `json:"regions"`
	GroupBy         []string            `json:"group_by"`
	Metrics         []string            `json:"metrics"`
	Tags            map[string][]string `json:"tags,omitempty"`
	AWSCLICommand   string              `json:"aws_cli_command"`
	ConsoleURL      string              `json:"console_url"`
	AggregationNote string              `json:"aggregation_note"`
}

type CostInformation struct {
	CostMetadata CostMetadata                     `json:"metadata"`
	CostResults  []costexplorertypes.ResultByTime `json:"results"`
	QueryInfo    CostQueryInfo                    `json:"query_info"`
}

type CostMetadata struct {
	StartDate   time.Time           `json:"start_date"`
	EndDate     time.Time           `json:"end_date"`
	Granularity string              `json:"granularity"`
	Tags        map[string][]string `json:"tags"`
	Services    []string            `json:"services"`
}
