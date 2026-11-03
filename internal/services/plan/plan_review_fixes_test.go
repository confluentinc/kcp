package plan

import (
	"strings"
	"testing"

	"github.com/confluentinc/kcp/internal/services/plan/engine"
)

// Self-managed sinks on a Gateway plan: the offset-sync filter instruction applies to a
// static route only, since a dynamic route keeps offset sync disabled on the link.
func TestConnectorsStep_SelfManagedSinksOnGatewayAreRouteAware(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover = engine.SwitchoverResult{Value: "Gateway cutover (no downtime): Gateway license required", GatewayMediated: true}
	cp.Plan.Connectors = connectorsPlan("Run them yourself on your own Connect cluster").Connectors
	out := renderMigration(cp)
	for _, want := range []string{
		"Create sink connectors only after you promote their topics. On a static route, stop each old sink connector with its service, before you promote its topics, so its last offsets sync, and list each sink's consumer group (connect-<connector-name>) in consumer.offset.group.filters so it resumes from its synced offsets. On a dynamic route, consumer offset sync stays disabled on the link, so don't list the sink groups in consumer.offset.group.filters.",
		"On a static route, each resumes from the offsets synced for its consumer group",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("Gateway plan missing %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Create sink connectors only after you promote their topics, and list") || strings.Contains(out, "Each resumes from the offsets synced") {
		t.Errorf("Gateway plan must not give the unconditional sync instruction:\n%s", out)
	}
}

// The Gateway SCRAM command uses kafka-configs without .sh, and a plan with no known
// source auth makes no claim about the Gateway holding no source credentials.
func TestGatewaySCRAMCommandNameAndUnknownAuth(t *testing.T) {
	cp := enterpriseClusterLinkPlan()
	cp.Plan.Switchover.GatewayMediated = true
	cp.effectiveSourceAuths = []string{SourceAuthSCRAM}
	out := renderMigration(cp)
	if !strings.Contains(out, "`kafka-configs --bootstrap-server <gateway-host>:9599") || strings.Contains(out, "kafka-configs.sh") {
		t.Errorf("SCRAM registration must use kafka-configs without .sh:\n%s", out)
	}
	if !strings.Contains(out, "Route your clients through the Gateway: point each client at the Gateway instead of the source.") {
		t.Errorf("SCRAM plan lacks the separate routing step:\n%s", out)
	}
	cp.effectiveSourceAuths = nil
	out = renderMigration(cp)
	if strings.Contains(out, "holds none") || !strings.Contains(out, "Deploy the Confluent Gateway with Confluent for Kubernetes and route your clients through it: ") {
		t.Errorf("unknown-auth Gateway plan:\n%s", out)
	}
}

// Connector wording on Replicator and cluster-link plans: the utility's version
// prerequisite, no "source offsets don't carry" next to the utility, the reset time
// and dry-run group, and an offsets block for generated sink definitions.
func TestConnectorsStep_ReplicatorAndUtilityWording(t *testing.T) {
	const version = "This mode needs Kafka Connect on Apache Kafka 3.6 or Confluent Platform 7.6 or later, which has the offsets REST API."
	self := stepsSection(t, stepsMarkdown(t, map[string]string{"kafka_version": "older", "self_managed_connectors": "true"}))
	for _, want := range []string{
		version,
		"note the time in UTC, a few seconds before it stopped, to allow for producer clock skew.",
		"--group <any-unused-group-name> --topic <topic> --reset-offsets",
		"Add an offsets block to each sink connector's generated definition before you apply it.",
	} {
		if !strings.Contains(self, want) {
			t.Errorf("self-managed Replicator plan missing %q:\n%s", want, self)
		}
	}
	if strings.Contains(self, "Source connectors don't carry offsets over") {
		t.Errorf("the utility carries source offsets, so the plan must not say they don't:\n%s", self)
	}

	msk := stepsSection(t, stepsMarkdown(t, map[string]string{"kafka_version": "older", "msk_connect_present": "true"}))
	if n := strings.Count(msk, "before you apply it"); n != 1 || !strings.Contains(msk, "Source connectors don't carry offsets over, so check where each one should start.") {
		t.Errorf("MSK Connect Replicator plan repeats \"before you apply it\" (%d):\n%s", n, msk)
	}

	link := stepsSection(t, stepsMarkdown(t, map[string]string{"self_managed_connectors": "true"}))
	if !strings.Contains(link, version) {
		t.Errorf("link plan lacks the utility's version prerequisite:\n%s", link)
	}

	kept := stepsSection(t, stepsMarkdown(t, map[string]string{"kafka_version": "older", "self_managed_connectors": "true", "connector_destination": "self-managed"}))
	if strings.Contains(kept, "Add an offsets block") || strings.Contains(kept, version) || !strings.Contains(kept, "a few records around that time may be delivered twice") {
		t.Errorf("kept self-managed Replicator plan:\n%s", kept)
	}
}
