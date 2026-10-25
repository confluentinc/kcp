package client_inventory

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/cloudwatchlogs"
)

// cwLogsAPI is the slice of the CloudWatch Logs client this source uses. An
// interface so tests inject a fake — the same seam the codebase uses for
// S3Service. *cloudwatchlogs.Client satisfies it.
type cwLogsAPI interface {
	FilterLogEvents(ctx context.Context, in *cloudwatchlogs.FilterLogEventsInput, optFns ...func(*cloudwatchlogs.Options)) (*cloudwatchlogs.FilterLogEventsOutput, error)
}

// cloudWatchLogSource reads MSK broker TRACE logs from a CloudWatch Logs log
// group over a time window. It is an additive peer of s3LogSource behind the
// LogSource interface; the gate/parser/dedup/upsert downstream are unchanged.
type cloudWatchLogSource struct {
	api      cwLogsAPI
	logGroup string
	start    time.Time
	end      time.Time
}

// NewCloudWatchLogSource constructs a CloudWatch Logs source for the given log
// group and [start,end] window.
func NewCloudWatchLogSource(api cwLogsAPI, logGroup string, start, end time.Time) *cloudWatchLogSource {
	return &cloudWatchLogSource{api: api, logGroup: logGroup, start: start, end: end}
}

func (c *cloudWatchLogSource) Name() string { return c.logGroup }

// ListChunks treats the whole time window as one chunk. (A future optimization
// could page by log stream — one per broker — for parallelism; ReadChunk is the
// seam for that.)
func (c *cloudWatchLogSource) ListChunks(ctx context.Context) ([]LogChunk, error) {
	return []LogChunk{{ID: c.logGroup}}, nil
}

// ReadChunk pages FilterLogEvents across the window (following NextToken to the
// end) and emits each event's RAW @message, one per line. THIS is the entire
// "de-envelope" step: CloudWatch's message body is the verbatim broker log line,
// so the existing gate + parser consume it with no changes.
func (c *cloudWatchLogSource) ReadChunk(ctx context.Context, _ LogChunk) ([]byte, error) {
	var buf bytes.Buffer
	var token *string
	for {
		out, err := c.api.FilterLogEvents(ctx, &cloudwatchlogs.FilterLogEventsInput{
			LogGroupName: aws.String(c.logGroup),
			StartTime:    aws.Int64(c.start.UnixMilli()),
			EndTime:      aws.Int64(c.end.UnixMilli()),
			NextToken:    token,
		})
		if err != nil {
			return nil, fmt.Errorf("filter log events: %w", err)
		}
		for _, e := range out.Events {
			if e.Message != nil {
				buf.WriteString(*e.Message)
				buf.WriteByte('\n')
			}
		}
		if out.NextToken == nil || *out.NextToken == "" {
			break
		}
		token = out.NextToken
	}
	return buf.Bytes(), nil
}

// Compile-time proof cloudWatchLogSource satisfies LogSource.
var _ LogSource = (*cloudWatchLogSource)(nil)
