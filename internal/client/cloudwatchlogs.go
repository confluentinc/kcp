package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

func NewCloudWatchLogsClient(region string) (*cloudwatchlogs.Client, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %v", err)
	}

	if region != "" {
		cfg.Region = region
	}

	cloudWatchLogsClient := cloudwatchlogs.NewFromConfig(cfg)

	return cloudWatchLogsClient, nil
}
