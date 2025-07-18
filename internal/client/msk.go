package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
)

func NewMSKClient(region string) (*kafka.Client, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO())
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to load AWS config: %v", err)
	}

	if region != "" {
		cfg.Region = region
	}

	mskClient := kafka.NewFromConfig(cfg)

	return mskClient, nil
}
