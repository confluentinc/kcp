package discover

type DiscovererOpts struct {
	Regions []string
}

type Discoverer struct {
	Regions []string
}

func NewDiscoverer(opts DiscovererOpts) *Discoverer {
	return &Discoverer{
		Regions: opts.Regions,
	}
}

func (d *Discoverer) Run() error {
	return nil
}
