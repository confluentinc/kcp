package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/costexplorer"
)

func NewCostExplorerClient(region string) (*costexplorer.Client, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to load AWS config: %v", err)
	}

	if region != "" {
		cfg.Region = region
	}

	costExplorerClient := costexplorer.NewFromConfig(cfg)

	return costExplorerClient, nil
}
