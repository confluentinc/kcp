package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
)

func TestDecideClusterType_DefaultEnterprise(t *testing.T) {
	cfg := defaultCfg(t)
	sizing := types.ClusterSizing{ClusterID: "x", FinalECKU: 5}
	d := DecideClusterType(types.ProcessedCluster{Name: "x"}, sizing, cfg, defaultInputs())
	assert.Equal(t, types.ClusterTypeEnterprise, d.Verdict)
	assert.Empty(t, d.Triggers)
}

func TestDecideClusterType_DedicatedWhenSizedOverPNICap(t *testing.T) {
	cfg := defaultCfg(t)
	// pni_max_eCKU = 32; size at 33 to fire the rule.
	sizing := types.ClusterSizing{ClusterID: "huge", FinalECKU: 33}
	d := DecideClusterType(types.ProcessedCluster{Name: "huge"}, sizing, cfg, defaultInputs())
	assert.Equal(t, types.ClusterTypeDedicated, d.Verdict)
	assert.Len(t, d.Triggers, 1)
	assert.Equal(t, "eCKU_exceeds_pni_cap", d.Triggers[0].RowID)
	assert.Contains(t, d.Triggers[0].Evidence, "33 eCKU")
	assert.Contains(t, d.Triggers[0].Evidence, "32 eCKU")
}

func TestHardLimitCatalogIsWellFormed(t *testing.T) {
	seen := map[string]bool{}
	for _, hl := range HardLimitCatalog {
		assert.NotEmpty(t, hl.ID)
		assert.NotEmpty(t, hl.Description)
		assert.NotNil(t, hl.Check, "all listed rules must be wired (no nil Check until the input is real)")
		assert.False(t, seen[hl.ID], "duplicate ID %q", hl.ID)
		seen[hl.ID] = true
	}
}
