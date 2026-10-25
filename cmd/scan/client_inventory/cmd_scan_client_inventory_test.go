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
	opts, useCW, err := parseClientInventoryFlags(rawFlags{s3Uri: testS3Uri, stateFile: "kcp-state.json"})
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
	_, _, err := parseClientInventoryFlags(rawFlags{logGroupName: "kcp", clusterName: "c", stateFile: "s"})
	if err == nil {
		t.Fatal("want error when --region is missing in CloudWatch mode")
	}
}

func TestParse_CloudWatchMode_RequiresClusterName(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{logGroupName: "kcp", region: "us-east-1", stateFile: "s"})
	if err == nil {
		t.Fatal("want error when --cluster-name is missing in CloudWatch mode")
	}
}

func TestParse_CloudWatchMode_DefaultsWindowTo24h(t *testing.T) {
	opts, useCW, err := parseClientInventoryFlags(rawFlags{logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s"})
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
		logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s",
		startTime: "not-a-time",
	})
	if err == nil {
		t.Fatal("want error for unparseable --start-time")
	}
}

func TestParse_CloudWatchMode_RejectsInvertedWindow(t *testing.T) {
	_, _, err := parseClientInventoryFlags(rawFlags{
		logGroupName: "kcp", region: "us-east-1", clusterName: "c", stateFile: "s",
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
	cmd.SetArgs([]string{"--state-file", "s"})
	if err := cmd.Execute(); err == nil {
		t.Fatal("want error when neither --s3-uri nor --log-group-name is set")
	}
}

// The published IAM annotation must document both the S3 and the CloudWatch
// source footprints (it feeds the generated command-reference docs).
func TestClientInventoryIAMAnnotation_CoversBothSources(t *testing.T) {
	got := clientInventoryIAMAnnotation()
	for _, want := range []string{
		"s3:GetObject",
		"s3:ListBucket",
		"logs:FilterLogEvents",
		"BrokerLogsFromS3",
		"BrokerLogsFromCloudWatch",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("annotation missing %q:\n%s", want, got)
		}
	}
}
