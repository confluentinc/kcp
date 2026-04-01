package client

import (
	"context"
	"fmt"

	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/glue"
)

func NewGlueClient(ctx context.Context, region string) (*glue.Client, error) {
	cfg, err := config.LoadDefaultConfig(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to load AWS config: %w", err)
	}

	if region != "" {
		cfg.Region = region
	}

	glueClient := glue.NewFromConfig(cfg)

	return glueClient, nil
}
