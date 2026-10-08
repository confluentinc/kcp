package plan

import (
	"strings"
	"testing"
	"time"
)

// Several sign-in methods no longer reach the Gateway: the plan falls back and says why,
// with no mixed-authentication route text.
func TestPlan_MixedAuthFallsBackFromGateway(t *testing.T) {
	md := stepsMarkdown(t, map[string]string{"downtime_tolerance": "zero", "source_auth": "[mtls, scram]"})
	if !strings.Contains(md, "Your clients use mTLS and SASL/SCRAM, so this plan uses a Cluster Linking cutover without the Gateway instead.") {
		t.Errorf("missing fallback sentence")
	}
	for _, bad := range []string{"Give each authentication type its own Gateway route", "kcp migration execute"} {
		if strings.Contains(md, bad) {
			t.Errorf("mixed-auth plan must not contain %q", bad)
		}
	}
}

// Replicator plans with connectors that stay self-managed still get the offset guidance.
func TestPlan_ReplicatorSelfManagedConnectorOffsets(t *testing.T) {
	md := stepsMarkdown(t, map[string]string{"kafka_version": "older", "self_managed_connectors": "true", "connector_destination": "self-managed"})
	for _, want := range []string{"At cutover, let each old sink connector reach zero lag, stop it, and note the time in UTC, a few seconds before it stopped, to allow for producer clock skew. Once Replicator has caught up, set the new sink's consumer group to that time, then create the sink connector: `kafka-consumer-groups --bootstrap-server <confluent-cloud-bootstrap> --command-config <client.properties> --group connect-<connector-name> --topic <topic> --reset-offsets --to-datetime <yyyy-MM-ddTHH:mm:ss.SSS, in UTC> --execute`.", "Source connectors don't carry offsets over", "recreate each source connector there. Create each sink connector only after you reset its offsets at cutover (below)."} {
		if !strings.Contains(md, want) {
			t.Errorf("missing %q", want)
		}
	}
	if strings.Contains(md, docConnectorOffsets) {
		t.Errorf("self-managed connectors must not link the managed offsets page")
	}
}

// A start-fresh plan does not ask tiered_storage at all.
func TestPlanInputs_TieredStorageHiddenOnStartFresh(t *testing.T) {
	declared, _, err := ParseDeclaredInputs([]byte("clusters:\n  your-cluster:\n    source_platform: msk\n    move_existing_data: false\n"))
	if err != nil {
		t.Fatal(err)
	}
	fixed := func() time.Time { return time.Date(2026, 9, 3, 0, 0, 0, 0, time.UTC) }
	yaml := RenderPlanInputsYAML(BuildEnginePlan(ScanlessState(), declared, "", fixed))
	if strings.Contains(yaml, "tiered_storage:   # not set") {
		t.Errorf("tiered_storage must not be required on start fresh:\n%s", yaml)
	}
	if strings.Contains(yaml, "tiered_storage") {
		t.Errorf("tiered_storage must not be asked on start fresh:\n%s", yaml)
	}
}

// The self-managed sink offset reset sits in the cutover step, after Replicator has caught up.
func TestPlan_ReplicatorSelfManagedSinkOffsetsInCutover(t *testing.T) {
	md := stepsMarkdown(t, map[string]string{"kafka_version": "older", "self_managed_connectors": "true", "connector_destination": "self-managed"})
	start := strings.Index(md, "Migration steps")
	if start < 0 {
		t.Fatalf("no Migration steps section:\n%s", md)
	}
	steps := md[start:]
	i := strings.Index(steps, ": cut over.**")
	j := strings.Index(steps, "At cutover, let each old sink connector reach zero lag")
	k := strings.Index(steps, ": port your connectors to a self-managed Connect cluster")
	if i < 0 || j < i || k < 0 || k > i || strings.Count(steps, "At cutover, let each old sink connector reach zero lag") != 1 {
		t.Errorf("sink offset reset must follow the cutover step (cut over %d, reset %d, port %d)", i, j, k)
	}
}
