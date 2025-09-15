package discover_v2

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/aws/aws-sdk-go-v2/service/kafka"
	kafkatypes "github.com/aws/aws-sdk-go-v2/service/kafka/types"

	"github.com/confluentinc/kcp/internal/types"
)

type RegionDiscovererMSKService interface {
	ListClusters(ctx context.Context, maxResults int32) ([]kafkatypes.Cluster, error)
	GetConfigurations(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error)
}

type RegionDiscovererCostService interface {
	GetCostsForTimeRange(ctx context.Context, region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.RegionCosts, error)
}

type RegionDiscoverer struct {
	mskService  RegionDiscovererMSKService
	costService RegionDiscovererCostService
}

func NewRegionDiscoverer(mskService RegionDiscovererMSKService, costService RegionDiscovererCostService) *RegionDiscoverer {
	return &RegionDiscoverer{
		mskService:  mskService,
		costService: costService,
	}
}

func (rd *RegionDiscoverer) Discover(ctx context.Context, region string) (*types.DiscoveredRegion, error) {
	slog.Info("🔍 discovering region", "region", region)
	discoveredRegion := types.DiscoveredRegion{
		Name: region,
	}

	maxResults := int32(250)

	configurations, err := rd.discoverConfigurations(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	discoveredRegion.Configurations = configurations

	regionCosts, err := rd.discoverCosts(ctx, region)
	if err != nil {
		return nil, err
	}
	discoveredRegion.Costs = *regionCosts

	clusterArns, err := rd.discoverClusterArns(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	discoveredRegion.ClusterArns = clusterArns

	return &discoveredRegion, nil
}

func (rd *RegionDiscoverer) discoverConfigurations(ctx context.Context, maxResults int32) ([]kafka.DescribeConfigurationRevisionOutput, error) {
	configurations, err := rd.mskService.GetConfigurations(ctx, maxResults)
	if err != nil {
		return nil, err
	}
	return configurations, nil
}

func (rd *RegionDiscoverer) discoverCosts(ctx context.Context, region string) (*types.CostInformation, error) {
	// todo - include tags in future?
	tags := []string{}
	tagsMap := rd.convertTagsToMap(tags)

	// time range of 12 months from now with monthly granularity
	startDate := time.Now().AddDate(0, -12, 0)
	endDate := time.Now()
	granularity := costexplorertypes.GranularityMonthly

	regionCosts, err := rd.costService.GetCostsForTimeRange(ctx, region, startDate, endDate, granularity, tagsMap)
	if err != nil {
		return nil, fmt.Errorf("❌ Failed to get AWS costs: %v", err)
	}

	costMetadata := types.CostMetadata{
		StartDate:   startDate,
		EndDate:     endDate,
		Granularity: string(granularity),
		Tags:        tagsMap,
		Services:    regionCosts.Services,
	}
	costInformation := types.CostInformation{
		CostData:     regionCosts.CostData.Costs,
		CostMetadata: costMetadata,
	}
	return &costInformation, nil
}

func (rd *RegionDiscoverer) discoverClusterArns(ctx context.Context, maxResults int32) ([]string, error) {
	slog.Info("🔍 listing clusters")

	clusters, err := rd.mskService.ListClusters(ctx, maxResults)
	if err != nil {
		return nil, err
	}

	clusterArns := []string{}
	for _, cluster := range clusters {
		clusterArns = append(clusterArns, aws.ToString(cluster.ClusterArn))
	}

	return clusterArns, nil
}

func (rd *RegionDiscoverer) convertTagsToMap(tags []string) map[string][]string {
	if len(tags) == 0 {
		return nil
	}

	tagMap := make(map[string][]string)
	for _, tag := range tags {
		parts := strings.Split(tag, "=")
		if len(parts) == 2 {
			key := parts[0]
			value := parts[1]
			tagMap[key] = append(tagMap[key], value)
		}
	}
	return tagMap
}
