package plan

import (
	"strings"
	"testing"
)

const (
	sinkOffsetsWording = "Confluent-managed sink connectors read with their own consumer group, so create each one with the offsets of its MSK Connect consumer group (`connect-<connector-name>`); see [Create connectors with offsets](https://docs.confluent.io/cloud/current/connectors/offsets.html)."
	afterPromoteWord   = "Run the utility only after you promote the mirror topics, since it creates the Confluent-managed connectors right away."
)

// The promote, sink-offset and utility-timing wording applies only where a cluster link moves
// the data; Replicator and start-fresh plans keep "generate definitions" and drop the rest.
func TestConnectorsStep_WordingFollowsDataMovement(t *testing.T) {
	conn := map[string]string{"msk_connect_present": "true", "self_managed_connectors": "true"}
	link := stepsSection(t, stepsMarkdown(t, conn))
	for _, want := range []string{sinkOffsetsWording, afterPromoteWord, "only after you promote the mirror topics", "kcp create-asset migrate-connectors msk"} {
		if !strings.Contains(link, want) {
			t.Errorf("link plan missing %q:\n%s", want, link)
		}
	}
	if strings.Contains(link, "Sink connectors resume from") {
		t.Errorf("link plan still has the offset-filter sentence:\n%s", link)
	}

	fresh := map[string]string{"msk_connect_present": "true", "self_managed_connectors": "true", "move_existing_data": "false"}
	got := stepsSection(t, stepsMarkdown(t, fresh))
	for _, bad := range []string{sinkOffsetsWording, afterPromoteWord, "promote the mirror topics", "Sink connectors resume from"} {
		if strings.Contains(got, bad) {
			t.Errorf("start-fresh plan contains %q:\n%s", bad, got)
		}
	}
	for _, want := range []string{"stop your source connectors first, then stop each sink connector only after its consumer lag on the old cluster reaches zero", "Recreate your connectors as"} {
		if !strings.Contains(got, want) {
			t.Errorf("start-fresh plan missing %q:\n%s", want, got)
		}
	}
	for _, bad := range []string{"read-only mirror topic", "Recreate the source connectors"} {
		if strings.Contains(got, bad) {
			t.Errorf("start-fresh plan contains %q:\n%s", bad, got)
		}
	}
	if !strings.Contains(link, "write into a read-only mirror topic") {
		t.Errorf("link plan lost the mirror-topic wording:\n%s", link)
	}
	if !strings.Contains(got, "kcp create-asset migrate-connectors msk") {
		t.Errorf("start-fresh plan dropped generate-definitions:\n%s", got)
	}
}

// Connectors that stay self-managed get neither migrate-connectors nor the Connect Migration Utility.
func TestConnectorsStep_KeepSelfManagedSkipsGenerationAndUtility(t *testing.T) {
	md := stepsMarkdown(t, map[string]string{"msk_connect_present": "true", "self_managed_connectors": "true", "connector_destination": "self-managed"})
	steps := stepsSection(t, md)
	if !strings.Contains(steps, "port your connectors to a self-managed Connect cluster") {
		t.Fatalf("no self-managed connectors step:\n%s", steps)
	}
	for _, bad := range []string{"migrate-connectors msk", "migrate-connectors self-managed", "Connect Migration Utility copies", docConnectMigrationUtility, afterPromoteWord} {
		if strings.Contains(steps, bad) {
			t.Errorf("keep-self-managed steps contain %q:\n%s", bad, steps)
		}
	}
}

// Kerberos sources are told to create a SCRAM user for the link.
func TestCredentialsStep_KerberosAsksForSCRAMUser(t *testing.T) {
	steps := stepsSection(t, stepsMarkdown(t, map[string]string{"source_platform": "apache-kafka", "source_cloud": "aws", "source_auth": "[kerberos]"}))
	if !strings.Contains(steps, "that only the link uses, and create a SCRAM user for the link, in place before you apply Step 3, the migration link.") {
		t.Errorf("Kerberos credentials note lacks the SCRAM user:\n%s", steps)
	}
}

// A specialist-wired link needs an explicit step creating it before migrate-topics names it.
func TestMigrationSteps_SpecialistLinkStepBeforeTopics(t *testing.T) {
	for _, auth := range []string{"[mtls]", "[sasl-plain]"} {
		steps := stepsSection(t, stepsMarkdown(t, map[string]string{
			"source_platform": "apache-kafka", "source_cloud": "azure", "target_cloud": "azure", "source_auth": auth,
		}))
		link := strings.Index(steps, "**Step 2: set up the cluster link with a specialist.** A specialist sets up the cluster link")
		topics := strings.Index(steps, "**Step 3: create your topics on the target.**")
		if link < 0 || topics < link {
			t.Fatalf("%s: want the link step (Step 2) before the topics step (Step 3):\n%s", auth, steps)
		}
		if !strings.Contains(steps, "Use the link's name for `<your-link-name>`") {
			t.Errorf("%s: missing link-name wording:\n%s", auth, steps)
		}
	}
}
