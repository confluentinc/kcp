package scan

import (
	"context"
	"log/slog"

	"github.com/confluentinc/kcp/internal/client"
	"github.com/confluentinc/kcp/internal/generators/scan/region"
)

type ScanOpts struct {
	Regions   []string
	SkipKafka bool
}

type Scanner struct {
	regions   []string
	skipKafka bool
}

func NewScanner(opts ScanOpts) *Scanner {
	return &Scanner{
		regions:   opts.Regions,
		skipKafka: opts.SkipKafka,
	}
}

func (rs *Scanner) Run() error {

	for _, r := range rs.regions {

		// scan the region
		opts := region.ScanRegionOpts{
			Region: r,
		}

		mskClient, err := client.NewMSKClient(r)
		if err != nil {
			slog.Error("failed to create msk client", "region", r, "error", err)
			continue
		}

		regionScanner := region.NewRegionScanner(mskClient, opts)
		scanResult, err := regionScanner.ScanRegion(context.Background())
		if err != nil {
			slog.Error("failed to scan region", "region", r, "error", err)
			continue
		}

		if err := scanResult.WriteAsJson(); err != nil {
			slog.Error("failed to write region scan result", "region", r, "error", err)
			continue
		}

		if err := scanResult.WriteAsMarkdown(); err != nil {
			slog.Error("failed to write region scan result", "region", r, "error", err)
			continue
		}

		// get region costs
		
	}

	return nil
}
