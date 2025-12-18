package client

import (
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
)

func NewMSKClient(region string) (*kafka.Client, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		// https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-retries-timeouts.html
		config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(opts *retry.StandardOptions) {
				opts.MaxAttempts = 10
				opts.MaxBackoff = 5 * time.Second
			})
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to load AWS config: %v", err)
	}

	if region != "" {
		cfg.Region = region
	}

	mskClient := kafka.NewFromConfig(cfg)

	return mskClient, nil
}
