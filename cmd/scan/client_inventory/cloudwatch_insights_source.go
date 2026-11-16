package client_inventory

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
	cwltypes "github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs/types"
)

// insightsMaxRows is the most rows one Logs Insights query can return. It is also
// the Limit we request: the service default is only 1000.
const insightsMaxRows = 10000

// kafkaApiInsightsQuery lets CloudWatch do the filtering and de-duplication so KCP
// downloads one representative TRACE line per distinct client, not every event.
//
// Correctness rests on the grouping key never being COARSER than KCP's composite key
// (clientId|topic|role|auth|principal): if two distinct clients shared a group, only
// one would survive. So the key is built from the SAME regex captures the parser
// uses (see kafka_trace_line_parser.go) — role (apiKey), clientId, both topic forms,
// and every principal form. Finer is harmless (KCP de-duplicates again afterwards);
// coarser would silently lose clients. coalesce() turns missing captures into ""
// so lines without, say, a FetchTopic still group instead of being dropped.
//
// latest(@message) keeps the most recent line per group, matching KCP's own
// "store the most recent request" rule, and its text is fed to the unchanged parser.
const kafkaApiInsightsQuery = `filter @message like /TRACE \[KafkaApi-\d+\]/ and (@message like /apiKey=PRODUCE/ or @message like /apiKey=FETCH/)
| parse @message /apiKey=(?<api>[^,)]+)/
| parse @message /clientId=(?<cid>[^,)]+)/
| parse @message /FetchTopic\(topic='(?<ftopic>[^']+)'/
| parse @message /partitionSizes=\[(?<ptopic>.+)-\d+=/
| parse @message /principal:\[IAM\]:\[(?<iam>arn:aws:[^\]]+)\]:/
| parse @message /securityProtocol:SSL,principal:(?<tls>User:CN=[^(]+?)\s*\(/
| parse @message /principal:(?<scram>User:[^ ]+)/
| fields @message, coalesce(api, "") as k_api, coalesce(cid, "") as k_cid, coalesce(ftopic, "") as k_ftopic, coalesce(ptopic, "") as k_ptopic, coalesce(iam, "") as k_iam, coalesce(tls, "") as k_tls, coalesce(scram, "") as k_scram
| stats latest(@message) as line by k_api, k_cid, k_ftopic, k_ptopic, k_iam, k_tls, k_scram`

// insightsAPI is the slice of the CloudWatch Logs client the Insights source uses.
// *cloudwatchlogs.Client satisfies it.
type insightsAPI interface {
	StartQuery(ctx context.Context, in *cloudwatchlogs.StartQueryInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.StartQueryOutput, error)
	GetQueryResults(ctx context.Context, in *cloudwatchlogs.GetQueryResultsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.GetQueryResultsOutput, error)
}

// cloudWatchInsightsLogSource is an alternative CloudWatch source: instead of
// downloading every event it runs a Logs Insights query that returns one line per
// distinct client. Everything downstream (gate, parser, dedup, upsert) is unchanged.
type cloudWatchInsightsLogSource struct {
	api          insightsAPI
	logGroup     string
	start        time.Time
	end          time.Time
	pollInterval time.Duration
}

// NewCloudWatchInsightsLogSource constructs the Insights-backed source for a log
// group and [start,end] window.
func NewCloudWatchInsightsLogSource(api insightsAPI, logGroup string, start, end time.Time) *cloudWatchInsightsLogSource {
	return &cloudWatchInsightsLogSource{api: api, logGroup: logGroup, start: start, end: end, pollInterval: 2 * time.Second}
}

func (c *cloudWatchInsightsLogSource) Name() string { return c.logGroup }

// ListChunks treats the window as one chunk: the query is a single server-side job.
func (c *cloudWatchInsightsLogSource) ListChunks(ctx context.Context) ([]LogChunk, error) {
	return []LogChunk{{ID: c.logGroup}}, nil
}

// ReadChunk runs the query, polls it to completion and emits each row's `line`
// value, one per line.
func (c *cloudWatchInsightsLogSource) ReadChunk(ctx context.Context, _ LogChunk) ([]byte, error) {
	started, err := c.api.StartQuery(ctx, &cloudwatchlogs.StartQueryInput{
		LogGroupName: aws.String(c.logGroup),
		// Insights takes epoch SECONDS (FilterLogEvents takes milliseconds).
		StartTime:   aws.Int64(c.start.Unix()),
		EndTime:     aws.Int64(c.end.Unix()),
		QueryString: aws.String(kafkaApiInsightsQuery),
		Limit:       aws.Int32(insightsMaxRows),
	})
	if err != nil {
		return nil, fmt.Errorf("start insights query: %w", err)
	}

	for {
		out, err := c.api.GetQueryResults(ctx, &cloudwatchlogs.GetQueryResultsInput{QueryId: started.QueryId})
		if err != nil {
			return nil, fmt.Errorf("get insights query results: %w", err)
		}

		switch out.Status {
		case cwltypes.QueryStatusComplete:
			return c.linesFromResults(out)
		case cwltypes.QueryStatusFailed, cwltypes.QueryStatusCancelled, cwltypes.QueryStatusTimeout:
			return nil, fmt.Errorf("insights query ended with status %s", out.Status)
		}

		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("waiting for insights query: %w", ctx.Err())
		case <-time.After(c.pollInterval):
		}
	}
}

func (c *cloudWatchInsightsLogSource) linesFromResults(out *cloudwatchlogs.GetQueryResultsOutput) ([]byte, error) {
	// Insights silently caps a result at Limit. A capped result may be missing
	// clients, so refuse it rather than report a partial inventory as complete.
	if len(out.Results) >= insightsMaxRows {
		return nil, fmt.Errorf("insights returned the maximum %d rows, so the result may be incomplete; narrow the window with --start-time/--end-time", insightsMaxRows)
	}

	var buf bytes.Buffer
	truncated := 0
	for _, row := range out.Results {
		for _, f := range row {
			if aws.ToString(f.Field) != "line" {
				continue
			}
			line := aws.ToString(f.Value)
			// Every real TRACE line ends with the logger name. A line that does not was
			// cut short, and the gate would drop it without any other signal.
			if !KafkaApiTracePattern.MatchString(line) {
				truncated++
			}
			buf.WriteString(line)
			buf.WriteByte('\n')
		}
	}
	if truncated > 0 {
		slog.Warn(fmt.Sprintf("⚠️ %d Insights rows did not look like complete TRACE lines and will be ignored; re-run without --use-insights to compare", truncated))
	}
	return buf.Bytes(), nil
}

// Compile-time proof cloudWatchInsightsLogSource satisfies LogSource.
var _ LogSource = (*cloudWatchInsightsLogSource)(nil)
