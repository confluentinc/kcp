package client_inventory

import (
	"strings"
	"testing"
	"time"
)

// A realistic MSK broker-logs S3 URI: AWSLogs/<acct>/KafkaBrokerLogs/<region>/<cluster-uuid>/<date>/.
// Region = segment[3] = us-east-1; cluster = segment[4] with the UUID suffix stripped = my-cluster.
const testS3Uri = "s3://my-logs/AWSLogs/000123456789/KafkaBrokerLogs/us-east-1/my-cluster-a1b2c3d4-e5f6-7890-abcd-ef1234567890-5/2025-08-13-14/"

func TestParse_S3Mode_DerivesRegionAndCluster(t *testing.T) {
	opts, useCW, err := parseClientInventoryFlags(rawFlags{logs: "s3", s3Uri: testS3Uri, stateFile: "kcp-state.json"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if useCW {
		t.Fatalf("want S3 mode, got CloudWatch")
	}
	if opts.Region != "us-east-1" {
		t.Errorf("Region = %q, want us-east-1", opts.Region)
	}
	if opts.ClusterName != "my-cluster" {
		t.Errorf("ClusterName = %q, want my-cluster", opts.ClusterName)
	}
	if opts.S3Uri != testS3Uri {
		t.Errorf("S3Uri = %q, want %q", opts.S3Uri, testS3Uri)
	}
}

func TestParse_CloudWatchMode_RequiresRegion(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{logs: "cloudwatch", logGroupName: "kcp", clusterName: "c", stateFile: "s"})
	if err == nil {
		t.Fatal("want error when --region is missing in CloudWatch mode")
	}
}

func TestParse_CloudWatchMode_RequiresClusterName(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{logs: "cloudwatch", logGroupName: "kcp", region: "us-east-1", stateFile: "s"})
	if err == nil {
		t.Fatal("want error when --cluster-name is missing in CloudWatch mode")
	}
}

func TestParse_CloudWatchMode_DefaultsWindowTo24h(t *testing.T) {
	opts, useCW, err := parseClientInventoryFlags(cwFlags())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !useCW {
		t.Fatal("want CloudWatch mode")
	}
	if opts.LogGroupName != "kcp" {
		t.Errorf("LogGroupName = %q, want kcp", opts.LogGroupName)
	}
	if window := opts.EndTime.Sub(opts.StartTime); window != 24*time.Hour {
		t.Errorf("default window = %v, want 24h", window)
	}
	if time.Since(opts.EndTime) > time.Minute {
		t.Errorf("EndTime should default to ~now, got %v", opts.EndTime)
	}
}

func TestParse_CloudWatchMode_RejectsBadTime(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{
		logs: "cloudwatch", logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s",
		startTime: "not-a-time",
	})
	if err == nil || !strings.Contains(err.Error(), "--start-time") {
		t.Fatalf("want an error naming --start-time, got %v", err)
	}
}

func TestParse_CloudWatchMode_RejectsInvertedWindow(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{
		logs: "cloudwatch", logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s",
		startTime: "2025-08-13T15:00:00Z", endTime: "2025-08-13T14:00:00Z",
	})
	if err == nil {
		t.Fatal("want error when --start-time is not before --end-time")
	}
}

// Review Focus #2: both sources set must fail fast (Cobra mutual exclusion).
func TestCmd_RejectsBothSources(t *testing.T) {
	cmd := NewScanClientInventoryCmd()
	cmd.SetArgs([]string{
		"--logs", "s3",
		"--s3-uri", testS3Uri,
		"--log-group-name", "kcp",
		"--region", "us-east-1", "--cluster-name", "c",
		"--state-file", "s",
	})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when both --s3-uri and --log-group-name are set")
	}
}

// Review Focus #2: neither source set must fail fast (Cobra one-required).
func TestCmd_RejectsNeitherSource(t *testing.T) {
	cmd := NewScanClientInventoryCmd()
	cmd.SetArgs([]string{"--logs", "s3", "--state-file", "s"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when neither --s3-uri nor --log-group-name is set")
	}
}

// --- --logs selection ---

func cwFlags() rawFlags {
	return rawFlags{logs: "cloudwatch", logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s"}
}

// --logs is required: flags alone no longer select a source.
func TestParse_SourceIsRequired(t *testing.T) {
	for name, in := range map[string]rawFlags{
		"only --s3-uri":         {s3Uri: testS3Uri, stateFile: "s"},
		"only --log-group-name": {logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s"},
		"nothing":               {stateFile: "s"},
	} {
		_, _, err := parseClientInventoryFlags(in)
		if err == nil || !strings.Contains(err.Error(), "--logs is required") {
			t.Errorf("%s without --logs: want \"--logs is required\", got %v", name, err)
		}
	}
}

func TestParse_ExplicitSourceMatchesFlags(t *testing.T) {
	if _, useCW, err := parseClientInventoryFlags(rawFlags{logs: "s3", s3Uri: testS3Uri, stateFile: "s"}); err != nil || useCW {
		t.Fatalf("--logs s3 with --s3-uri: useCW=%v err=%v", useCW, err)
	}
	in := cwFlags()
	in.logs = "cloudwatch"
	if _, useCW, err := parseClientInventoryFlags(in); err != nil || !useCW {
		t.Fatalf("--logs cloudwatch with --log-group-name: useCW=%v err=%v", useCW, err)
	}
	in.logs = "CloudWatch"
	if _, _, err := parseClientInventoryFlags(in); err != nil {
		t.Fatalf("--logs should be case-insensitive: %v", err)
	}
}

func TestParse_RejectsUnknownSource(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{logs: "kinesis", s3Uri: testS3Uri, stateFile: "s"})
	if err == nil || !strings.Contains(err.Error(), "--logs") {
		t.Fatalf("want an error naming --logs, got %v", err)
	}
}

func TestParse_SourceS3_RequiresS3Uri(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{logs: "s3", stateFile: "s"})
	if err == nil || !strings.Contains(err.Error(), "--s3-uri") {
		t.Fatalf("want an error naming --s3-uri, got %v", err)
	}
}

func TestParse_SourceCloudWatch_RequiresLogGroup(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{logs: "cloudwatch", region: "us-east-1", clusterName: "c", stateFile: "s"})
	if err == nil || !strings.Contains(err.Error(), "--log-group-name") {
		t.Fatalf("want an error naming --log-group-name, got %v", err)
	}
}

// The S3 source must refuse every CloudWatch-only flag instead of silently ignoring it.
func TestParse_S3Source_RejectsCloudWatchOnlyFlags(t *testing.T) {
	cases := map[string]rawFlags{
		"--log-group-name": {logGroupName: "kcp"},
		"--region":         {region: "us-east-1"},
		"--cluster-name":   {clusterName: "c"},
		"--start-time":     {startTime: "2025-08-13T14:00:00Z"},
		"--end-time":       {endTime: "2025-08-13T15:00:00Z"},
	}
	for flag, in := range cases {
		in.logs, in.s3Uri, in.stateFile = "s3", testS3Uri, "s"
		_, _, err := parseClientInventoryFlags(in)
		if err == nil {
			t.Errorf("%s with --logs s3: want error, got none", flag)
			continue
		}
		if !strings.Contains(err.Error(), flag) {
			t.Errorf("%s: error should name the flag, got %q", flag, err)
		}
	}
}

// The CloudWatch source must refuse --s3-uri.
func TestParse_CloudWatchSource_RejectsS3Uri(t *testing.T) {
	in := cwFlags()
	in.s3Uri = testS3Uri
	_, _, err := parseClientInventoryFlags(in)
	if err == nil || !strings.Contains(err.Error(), "--s3-uri") {
		t.Errorf("want error naming --s3-uri, got %v", err)
	}
}

func TestParse_ExplicitSourceContradictsFlags(t *testing.T) {
	if _, _, err := parseClientInventoryFlags(rawFlags{logs: "cloudwatch", s3Uri: testS3Uri, stateFile: "s"}); err == nil {
		t.Error("--logs cloudwatch with only --s3-uri must fail")
	}
	in := cwFlags()
	in.logs = "s3"
	if _, _, err := parseClientInventoryFlags(in); err == nil {
		t.Error("--logs s3 with only CloudWatch flags must fail")
	}
}

func TestParse_CloudWatchMode_RejectsStartEqualEnd(t *testing.T) {
	in := cwFlags()
	in.startTime, in.endTime = "2025-08-13T14:00:00Z", "2025-08-13T14:00:00Z"
	if _, _, err := parseClientInventoryFlags(in); err == nil {
		t.Fatal("want error when --start-time equals --end-time")
	}
}

// Same rules through the real Cobra command, so the wiring is covered too.
func TestCmd_S3Source_RejectsCloudWatchFlags(t *testing.T) {
	for _, extra := range [][]string{
		{"--region", "us-east-1"},
		{"--cluster-name", "c"},
		{"--start-time", "2025-08-13T14:00:00Z"},
		{"--end-time", "2025-08-13T15:00:00Z"},
	} {
		cmd := NewScanClientInventoryCmd()
		cmd.SetArgs(append([]string{"--logs", "s3", "--s3-uri", testS3Uri, "--state-file", "s"}, extra...))
		if err := cmd.Execute(); err == nil {
			t.Errorf("--s3-uri %v: want error, got none", extra)
		}
	}
}

func TestCmd_RequiresSourceFlag(t *testing.T) {
	cmd := NewScanClientInventoryCmd()
	cmd.SetArgs([]string{"--s3-uri", testS3Uri, "--state-file", "s"})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), `required flag(s) "logs" not set`) {
		t.Fatalf("want Cobra's required-flag error for --logs, got %v", err)
	}
}

func TestCmd_HasSourceFlag(t *testing.T) {
	if NewScanClientInventoryCmd().Flags().Lookup("logs") == nil {
		t.Fatal("--logs flag missing")
	}
}

// The published IAM annotation must document both the S3 and the CloudWatch
// source footprints (it feeds the generated command-reference docs).
func TestClientInventoryIAMAnnotation_CoversBothSources(t *testing.T) {
	got := clientInventoryIAMAnnotation()
	for _, want := range []string{
		"s3:GetObject",
		"s3:ListBucket",
		"BrokerLogsFromS3",
		"BrokerLogsFromCloudWatch",
		"logs:StartQuery",
		"logs:GetQueryResults",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("annotation missing %q:\n%s", want, got)
		}
	}
}

func TestClientInventoryIAMAnnotation_DoesNotRequireFilterLogEvents(t *testing.T) {
	if got := clientInventoryIAMAnnotation(); strings.Contains(got, "FilterLogEvents") {
		t.Errorf("annotation still mentions the removed FilterLogEvents permission:\n%s", got)
	}
}

// --use-insights was folded into the CloudWatch source; the flag must not linger.
func TestCmd_UseInsightsFlagIsGone(t *testing.T) {
	if NewScanClientInventoryCmd().Flags().Lookup("use-insights") != nil {
		t.Error("--use-insights should no longer exist; CloudWatch always uses Insights")
	}
}
