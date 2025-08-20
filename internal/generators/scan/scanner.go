package scan

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

	return nil
}
