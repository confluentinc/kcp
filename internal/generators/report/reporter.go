package report

import "log/slog"

type ReporterOpts struct {
}

type Reporter struct {
}

func NewReporter(opts ReporterOpts) *Reporter {
	return &Reporter{}
}

func (r *Reporter) Run() error {
	slog.Info("🔍 running reporter")
	return nil
}
