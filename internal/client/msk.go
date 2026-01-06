package client

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	"golang.org/x/time/rate"
)

type RateLimitedMSKClient struct {
	*kafka.Client
	limiter *rate.Limiter
}

func NewMSKClient(region string, requestsPerSecond float64, burstSize int) (*RateLimitedMSKClient, error) {
	cfg, err := config.LoadDefaultConfig(context.TODO(),
		// https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-retries-timeouts.html
		config.WithRetryer(func() aws.Retryer {
			return retry.NewStandard(func(opts *retry.StandardOptions) {
				opts.MaxAttempts = 3
				opts.MaxBackoff = 20 * time.Second
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
	limiter := rate.NewLimiter(rate.Limit(requestsPerSecond), burstSize)

	return &RateLimitedMSKClient{
		Client:  mskClient,
		limiter: limiter,
	}, nil
}

func (c *RateLimitedMSKClient) Wait(ctx context.Context) error {
	return c.limiter.Wait(ctx)
}

// Clusters with high topic count are likely to hit the 429 rate limnit errors. Therefore,
// we implement an additional layer of retries outside the AWS SDK's standard retryer.
// If we hit a 429 or quota error, we wait for a new token from our rate limiter and try again.
func (c *RateLimitedMSKClient) DescribeTopic(ctx context.Context, params *kafka.DescribeTopicInput, optFns ...func(*kafka.Options)) (*kafka.DescribeTopicOutput, error) {
	const maxExtraRetries = 5
	var lastErr error

	for i := 0; i <= maxExtraRetries; i++ {
		if err := c.Wait(ctx); err != nil {
			return nil, fmt.Errorf("rate limiter cancelled: %w", err)
		}

		output, err := c.Client.DescribeTopic(ctx, params, optFns...)
		if err == nil {
			return output, nil
		}

		lastErr = err
		// Check for 429 (TooManyRequestsException) or retry quota exceeded
		errMsg := err.Error()
		if strings.Contains(errMsg, "TooManyRequestsException") ||
			strings.Contains(errMsg, "retry quota exceeded") {

			// If we have retries left, loop again (which will wait on c.Wait(ctx))
			if i < maxExtraRetries {
				continue
			}
		} else {
			// Not a rate limit error, return immediately
			return nil, err
		}
	}

	return nil, lastErr
}
