package txndiscovery

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Opts is the resolved configuration of one discovery run: everything the
// runner needs, with no dependency on the flag variables it came from.
type Opts struct {
	// Brokers are the source cluster's bootstrap addresses and Region the AWS
	// region IAM signing needs.
	Brokers []string
	Region  string
	Auth    authResolution

	// Duration is the observation window; Interval the cadence at which
	// enrichment refreshes and buffered producer-id sightings are resolved.
	Duration time.Duration
	Interval time.Duration

	TxnStateTopic         string
	EnrichConsumerGroups  bool
	TailConsumerOffsets   bool
	IncludeInternalTopics bool

	// OutPath is the groups YAML. StatsOutPath and AuditLogPath are empty when
	// their artifact is not wanted — AuditLogPath is empty for --no-audit-log,
	// which the runner turns into a nil AuditWriter whose methods are all no-ops.
	OutPath      string
	StatsOutPath string
	AuditLogPath string

	// DryRun suppresses every artifact write. It does not suppress kcp.log.
	DryRun bool

	// Verbose mirrors the root --verbose flag and gates the detailed keep-up block.
	Verbose bool

	// Stdout is where the terminal narrative goes.
	Stdout io.Writer
}

// parseOpts snapshots the flag variables into an Opts.
func parseOpts() Opts {
	return Opts{
		Brokers:               splitCSV(sourceBootstrap),
		Region:                awsRegion,
		Auth:                  resolveAuth(),
		Duration:              duration,
		Interval:              interval,
		TxnStateTopic:         txnStateTopic,
		EnrichConsumerGroups:  enrichConsumerGroups,
		TailConsumerOffsets:   tailConsumerOffsets,
		IncludeInternalTopics: includeInternalTopics,
		OutPath:               outPath,
		StatsOutPath:          statsOutPath,
		AuditLogPath:          resolveAuditLogPath(outPath, auditLogPath, noAuditLog),
		DryRun:                dryRun,
		Stdout:                os.Stdout,
	}
}

// resolveAuditLogPath decides where the audit trail is written, or that it is
// not. An explicit path wins; otherwise the trail lands beside --out, because
// an operator reading txn-discovery.yaml is the one who needs the trail that
// explains it.
func resolveAuditLogPath(out, explicit string, disabled bool) string {
	if disabled {
		return ""
	}
	if explicit != "" {
		return explicit
	}
	return filepath.Join(filepath.Dir(out), DefaultAuditBasename)
}

// splitCSV parses a comma-separated bootstrap list, dropping blanks so a
// trailing comma does not become an empty broker address.
func splitCSV(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		if p = strings.TrimSpace(p); p != "" {
			out = append(out, p)
		}
	}
	return out
}
