package kafka_acls

import "testing"

// TestRequiredFlagsExist guards MarkFlagRequired against naming a flag that was
// never defined: pflag returns an error in that case, but the constructor
// discards it (`_ = aclsCmd.MarkFlagRequired(...)`), so a typo (e.g.
// "target-cluster-rest-endpoint" for "target-rest-endpoint") would otherwise
// leave that flag unenforced with no build or test failure.
func TestRequiredFlagsExist(t *testing.T) {
	cmd := NewConvertKafkaAclsCmd()
	for _, name := range []string{"state-file", "cluster-id", "target-cluster-id", "target-rest-endpoint"} {
		if cmd.Flags().Lookup(name) == nil {
			t.Errorf("MarkFlagRequired(%q): no such flag defined", name)
		}
	}
}
