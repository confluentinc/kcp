package engine

import (
	"strings"
	"testing"
)

func ibpReason(t *testing.T, mut func(*Profile)) string {
	t.Helper()
	sw := swOf(func(p *Profile) {
		p.KafkaVersion = "2.4–2.9"
		p.InterBrokerProtocol = "No"
		p.DowntimeTolerance = "Minutes per service"
		mut(p)
	})
	return sw.Reason
}

func TestIBPAttribution(t *testing.T) {
	const scan = "Based on your scan (inter-broker protocol below 2.8)"
	const answer = "Based on your answer (inter-broker protocol below 2.8)"
	cases := []struct {
		name string
		mut  func(*Profile)
		want string
	}{
		{"version scanned, IBP answered", func(p *Profile) { p.IBPAnswered = true }, answer},
		{"version answered, IBP scanned", func(p *Profile) { p.KafkaVersionAnswered = true }, scan},
		{"both scanned", func(p *Profile) {}, scan},
		{"both answered", func(p *Profile) { p.IBPAnswered, p.KafkaVersionAnswered = true, true }, answer},
	}
	for _, c := range cases {
		if got := ibpReason(t, c.mut); !strings.Contains(got, c.want) {
			t.Errorf("%s: reason %q missing %q", c.name, got, c.want)
		}
	}
}

func TestSchemaAttribution(t *testing.T) {
	schemaless := func(mut func(*Profile)) string {
		p := Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategySchemaless}
		mut(&p)
		return schemaDecision(p).Reason
	}
	// Scan-detected registry, no declared edition: presence is a scan finding.
	got := schemaless(func(p *Profile) { p.SchemaDetectedByScan = true })
	if !strings.Contains(got, "your scan (Schema Registry detected)") || strings.Contains(got, "your answer (Schema Registry detected") {
		t.Errorf("scan-detected: %q", got)
	}
	// Presence supplied by the customer.
	if got := schemaless(func(p *Profile) {}); !strings.Contains(got, "your answer (Schema Registry detected") {
		t.Errorf("answered: %q", got)
	}
	migrate := func(answered bool) string {
		return schemaDecision(Profile{SourceSRType: sourceSRCPEnterprise7, SchemaStrategy: schemaStrategyMigrate,
			SourceSROutboundReachableToCC: "Yes", SchemaDetectedByScan: true, SchemaAnswered: answered}).Reason
	}
	if got := migrate(true); !strings.Contains(got, "your answer (Confluent Platform Enterprise 7.1+, migrate your schemas)") {
		t.Errorf("declared edition: %q", got)
	}
	if got := migrate(false); !strings.Contains(got, "your scan (Confluent Platform Enterprise 7.1+)") {
		t.Errorf("undeclared edition: %q", got)
	}
}

func TestSchemaDecision_ScanDetectedBlankEdition(t *testing.T) {
	r := schemaDecision(Profile{SchemaDetectedByScan: true, SchemaStrategy: schemaStrategySchemaless})
	if strings.Contains(r.Reason, "no Schema Registry") || !strings.Contains(r.Reason, "your scan (Schema Registry detected)") || r.Value == "Schemaless" {
		t.Errorf("schemaless: %q %q", r.Value, r.Reason)
	}
	if r := schemaDecision(Profile{SchemaDetectedByScan: true, SchemaStrategy: schemaStrategyMigrate}); r.Kind != SchemaKindPending {
		t.Errorf("migrate: %+v", r)
	}
}
