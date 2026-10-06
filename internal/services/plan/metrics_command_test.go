package plan

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

func TestMetricsScanCommand_Sources(t *testing.T) {
	// Scanless with Amazon MSK named: only the MSK command.
	got := engine.MetricsScanCommand(engine.Profile{Scanless: true, SourcePlatformDeclared: true})
	if !strings.Contains(got, "kcp discover") || strings.Contains(got, "apache-kafka") {
		t.Errorf("declared MSK: got %q", got)
	}
	// Scanless with no source named: both.
	got = engine.MetricsScanCommand(engine.Profile{Scanless: true})
	if !strings.Contains(got, "kcp discover") || !strings.Contains(got, "apache-kafka") {
		t.Errorf("unnamed source: got %q", got)
	}
	// A mixed fleet with a scan carries the state file on the Apache Kafka command.
	got = engine.MetricsScanCommand(engine.Profile{Scanless: true, StateFile: "kcp-state.json"})
	if !strings.Contains(got, "--state-file kcp-state.json") {
		t.Errorf("mixed fleet: missing --state-file in %q", got)
	}
}

func TestFleetMetricsCommand_ScanlessMSK(t *testing.T) {
	declared := DeclaredInputs{Defaults: IntakeInputs{SourcePlatform: enginePlatformMSK}}
	ep := BuildEnginePlan(ScanlessState(), declared, "", fixedExampleClock)
	if got := fleetMetricsCommand(ep); strings.Contains(got, "apache-kafka") {
		t.Errorf("scanless MSK plan names the Apache Kafka command: %q", got)
	}
	unnamed := BuildEnginePlan(ScanlessState(), DeclaredInputs{}, "", fixedExampleClock)
	if got := fleetMetricsCommand(unnamed); !strings.Contains(got, "apache-kafka") {
		t.Errorf("scanless plan with no source should name both commands: %q", got)
	}
}

func TestBeforeYouStart_ScanlessMSKConnectUsesARNPlaceholder(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.SourcePlatform, cp.Arn, cp.ClusterID = "", "", ScanlessClusterName
	cp.topicsScanned = true
	cp.Plan.Connectors = engine.ConnectorsResult{Kind: engine.ConnectorsKindManaged}
	cp.ConnectorSource = &ConnectorSource{MSKConnect: true}
	out := renderBeforeYouStart(cp, "")
	if strings.Contains(out, "--cluster-arn "+ScanlessClusterName) || !strings.Contains(out, "--cluster-arn <your-msk-cluster-arn>") {
		t.Errorf("want an ARN placeholder, got:\n%s", out)
	}
}
