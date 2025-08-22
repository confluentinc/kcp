package costs

import (
	"fmt"
	"log/slog"
	"strings"
	"time"

	costexplorertypes "github.com/aws/aws-sdk-go-v2/service/costexplorer/types"
	"github.com/confluentinc/kcp/internal/types"
)

type RegionCosterOpts struct {
	Region      string
	StartDate   time.Time
	EndDate     time.Time
	Granularity costexplorertypes.Granularity
	Tag         []string
}

type RegionCoster struct {
	region      string
	costService CostService
	startDate   time.Time
	endDate     time.Time
	granularity costexplorertypes.Granularity
	tags        []string
}

type CostService interface {
	GetCostsForTimeRange(region string, startDate time.Time, endDate time.Time, granularity costexplorertypes.Granularity, tags map[string][]string) (types.RegionCosts, error)
}

func NewRegionCoster(costService CostService, opts RegionCosterOpts) *RegionCoster {
	return &RegionCoster{
		region:      opts.Region,
		costService: costService,
		startDate:   opts.StartDate,
		endDate:     opts.EndDate,
		granularity: opts.Granularity,
		tags:        opts.Tag,
	}
}

func (rc *RegionCoster) convertTagsToMap() map[string][]string {
	if len(rc.tags) == 0 {
		return nil
	}

	tagMap := make(map[string][]string)
	for _, tag := range rc.tags {
		parts := strings.Split(tag, "=")
		if len(parts) == 2 {
			key := parts[0]
			value := parts[1]
			tagMap[key] = append(tagMap[key], value)
		}
	}
	return tagMap
}

func (rc *RegionCoster) Run() error {
	slog.Info("🚀 starting region costs report", "region", rc.region)

	tags := rc.convertTagsToMap()
	regionCosts, err := rc.costService.GetCostsForTimeRange(rc.region, rc.startDate, rc.endDate, rc.granularity, tags)
	if err != nil {
		return fmt.Errorf("❌ Failed to get AWS costs: %v", err)
	}

	if err := regionCosts.WriteAsJson(); err != nil {
		return fmt.Errorf("❌ Failed to write JSON output: %v", err)
	}

	if err := regionCosts.WriteAsMarkdown(false); err != nil {
		return fmt.Errorf("❌ Failed to write markdown output: %v", err)
	}

	if err := regionCosts.WriteAsCSV(); err != nil {
		return fmt.Errorf("❌ Failed to write CSV output: %v", err)
	}

	slog.Info("✅ region costs report complete",
		"region", rc.region,
		"filePath", regionCosts.GetJsonPath(),
		"markdownPath", regionCosts.GetMarkdownPath(),
		"csvPath", regionCosts.GetCSVPath(),
	)
	return nil
}
