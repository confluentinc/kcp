package discover_v2

import "fmt"

type DiscovererV2Opts struct {
	Regions []string
}

type DiscovererV2 struct {
	regions []string
}

func NewDiscovererV2(opts DiscovererV2Opts) *DiscovererV2 {
	return &DiscovererV2{
		regions: opts.Regions,
	}
}

func (d *DiscovererV2) Run() error {
	fmt.Println("Running Discover V2")
	return nil
}
