package healthcheck

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/confluentinc/kcp/internal/services/markdown"
	"github.com/confluentinc/kcp/internal/sources"
	"github.com/confluentinc/kcp/internal/sources/osk"
	"github.com/spf13/cobra"
)

// filenameSafe matches any character that should be stripped from a cluster
// identifier when building a default output filename.
var filenameSafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// runHealthcheck is the entry point invoked by Cobra. It loads credentials
// via the OSK source, scans each cluster via the Kafka Admin API, renders
// a markdown report per cluster, and writes it to disk. A per-cluster
// summary is logged via slog (which fans out to both kcp.log and the
// console).
func runHealthcheck(cmd *cobra.Command, args []string) error {
	ctx := context.Background()

	source := osk.NewOSKSource()

	if err := source.LoadCredentials(credentialsFile); err != nil {
		return fmt.Errorf("failed to load credentials: %w", err)
	}

	clusters := source.GetClusters()
	slog.Info("clusters to healthcheck", "count", len(clusters))
	for _, cluster := range clusters {
		slog.Info("cluster", "name", cluster.Name, "id", cluster.UniqueID)
	}

	// Reject --output with multiple clusters — there is no sensible way
	// to write N reports to a single explicit path. The default
	// per-cluster naming handles multi-cluster automatically.
	if outputFile != "" && len(clusters) > 1 {
		return fmt.Errorf("--output cannot be used when the credentials file contains multiple clusters (%d found); omit --output to use per-cluster default filenames", len(clusters))
	}

	scanOpts := sources.ScanOptions{
		SkipTopics: false,
		SkipACLs:   false,
	}

	slog.Info("starting healthcheck scan")
	scanResult, err := source.Scan(ctx, scanOpts)
	if err != nil {
		return fmt.Errorf("healthcheck scan failed: %w", err)
	}

	timestamp := time.Now()
	for _, c := range scanResult.Clusters {
		path := resolveOutputPath(c, timestamp)

		md := RenderClusterHealthcheck(c, timestamp)
		if err := md.Print(markdown.PrintOptions{ToTerminal: false, ToFile: path}); err != nil {
			return fmt.Errorf("failed to write healthcheck report for cluster %s: %w", c.Identifier.Name, err)
		}

		userTopics := 0
		internalTopics := 0
		if c.KafkaAdminInfo.Topics != nil {
			userTopics = c.KafkaAdminInfo.Topics.Summary.Topics
			internalTopics = c.KafkaAdminInfo.Topics.Summary.InternalTopics
		}
		slog.Info("healthcheck complete",
			"cluster", c.Identifier.Name,
			"cluster_id", c.KafkaAdminInfo.ClusterID,
			"brokers", len(c.KafkaAdminInfo.DiscoveredBrokers),
			"user_topics", userTopics,
			"internal_topics", internalTopics,
			"acls", len(c.KafkaAdminInfo.Acls),
			"report", path,
		)
	}

	return nil
}

// resolveOutputPath returns the markdown output path for a cluster. When
// --output is set we honour it verbatim (multi-cluster case is rejected
// earlier in the runner). Otherwise we generate a default file in the
// working directory using the cluster name + timestamp.
func resolveOutputPath(c sources.ClusterScanResult, timestamp time.Time) string {
	if outputFile != "" {
		return outputFile
	}
	safeID := filenameSafe.ReplaceAllString(c.Identifier.Name, "-")
	return fmt.Sprintf("./healthcheck-%s-%s.md", safeID, timestamp.UTC().Format("20060102T150405Z"))
}
