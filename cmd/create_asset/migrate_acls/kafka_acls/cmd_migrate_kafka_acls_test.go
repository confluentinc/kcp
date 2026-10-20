package kafka_acls

import (
	"testing"

	"github.com/spf13/cobra"
)

// TestRequiredFlagsAreRequired guards MarkFlagRequired against naming a flag that was
// never defined (or being dropped): pflag returns an error in that case, but the constructor
// discards it (`_ = aclsCmd.MarkFlagRequired(...)`), so a typo (e.g.
// "target-cluster-rest-endpoint" for "target-rest-endpoint") would otherwise
// leave that flag unenforced with no build or test failure.
func TestRequiredFlagsAreRequired(t *testing.T) {
	cmd := NewConvertKafkaAclsCmd()
	for _, name := range []string{"state-file", "cluster-id", "target-cluster-id", "target-rest-endpoint"} {
		flag := cmd.Flags().Lookup(name)
		if flag == nil {
			t.Errorf("MarkFlagRequired(%q): no such flag defined", name)
			continue
		}
		if len(flag.Annotations[cobra.BashCompOneRequiredFlag]) == 0 {
			t.Errorf("flag %q is not marked required", name)
		}
	}
}
