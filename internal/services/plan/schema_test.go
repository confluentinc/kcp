package plan

import (
	"testing"

	"github.com/confluentinc/kcp/internal/services/report"
	"github.com/confluentinc/kcp/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// schemaState builds a ProcessedState with optional Confluent / Glue
// registries. nil omits the SchemaRegistries block entirely (the
// `source = none` case); empty slices model a scanner that ran but
// found nothing (still resolves to `none`).
func schemaState(confluentURLs, glueNames []string) report.ProcessedState {
	if confluentURLs == nil && glueNames == nil {
		return report.ProcessedState{}
	}
	srs := &types.SchemaRegistriesState{}
	for _, u := range confluentURLs {
		srs.ConfluentSchemaRegistry = append(srs.ConfluentSchemaRegistry, types.SchemaRegistryInformation{URL: u})
	}
	for _, n := range glueNames {
		srs.AWSGlue = append(srs.AWSGlue, types.GlueSchemaRegistryInformation{RegistryName: n})
	}
	return report.ProcessedState{SchemaRegistries: srs}
}

func schemaInputs(strategy string) PlanInputsResolved {
	return PlanInputsResolved{SchemaStrategy: strategy}
}

func ptrBool(b bool) *bool { return &b }

// Glue detected → kcp_migrate_schemas_glue, regardless of strategy
// (the kcp-automated path doesn't need a strategy declaration).
func TestDecideSchema_GlueDetected(t *testing.T) {
	dec := decideSchema(schemaState(nil, []string{"my-glue"}), defaultCfg(t), schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry))
	require.NotNil(t, dec)
	assert.Equal(t, SchemaSourceGlue, dec.Source)
	assert.Equal(t, SchemaPathMigrateGlue, primaryPath(dec))
	assert.Equal(t, []string{"my-glue"}, dec.GlueRegistries)
}

// Confluent SR + all three eligibility flags positive → schema_linking.
func TestDecideSchema_ConfluentEligible(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "7.5.1"
	inputs.ConfluentSRCPEdition = SchemaCPEditionEnterprise
	inputs.SourceSROutboundReachableToCC = ptrBool(true)

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, nil), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	assert.Equal(t, SchemaSourceConfluent, dec.Source)
	assert.Equal(t, SchemaPathSchemaLinking, primaryPath(dec))
	require.NotNil(t, dec.MeetsCPVersionFloor)
	assert.True(t, *dec.MeetsCPVersionFloor)
	require.NotNil(t, dec.MeetsCPEditionRequirement)
	assert.True(t, *dec.MeetsCPEditionRequirement)
}

// Confluent SR + CP version below 7.0 → defer_to_account_team.
func TestDecideSchema_ConfluentBelowCPFloor(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "6.2.1"
	inputs.ConfluentSRCPEdition = SchemaCPEditionEnterprise
	inputs.SourceSROutboundReachableToCC = ptrBool(true)

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, nil), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	assert.Equal(t, SchemaPathDeferToAccount, primaryPath(dec))
	require.NotNil(t, dec.MeetsCPVersionFloor)
	assert.False(t, *dec.MeetsCPVersionFloor)
}

// Confluent SR + Community edition → defer_to_account_team.
func TestDecideSchema_ConfluentCommunityEdition(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "7.5.1"
	inputs.ConfluentSRCPEdition = SchemaCPEditionCommunity
	inputs.SourceSROutboundReachableToCC = ptrBool(true)

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, nil), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	assert.Equal(t, SchemaPathDeferToAccount, primaryPath(dec))
	require.NotNil(t, dec.MeetsCPEditionRequirement)
	assert.False(t, *dec.MeetsCPEditionRequirement)
}

// Confluent SR + reachability undeclared → unknown (OQ asks).
func TestDecideSchema_ConfluentReachabilityUnknown(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "7.5.1"
	inputs.ConfluentSRCPEdition = SchemaCPEditionEnterprise
	// SourceSROutboundReachableToCC left nil

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, nil), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	assert.Equal(t, SchemaPathUnknown, primaryPath(dec))
	assert.Nil(t, dec.SourceSROutboundReachable, "nil tri-state preserved when input is unset")
}

// State empty + strategy=no_schemas → schemaless (section omitted by Build).
func TestDecideSchema_NoneAndNoSchemas(t *testing.T) {
	dec := decideSchema(schemaState(nil, nil), defaultCfg(t), schemaInputs(SchemaStrategyNoSchemas))
	require.NotNil(t, dec)
	assert.Equal(t, SchemaSourceNone, dec.Source)
	assert.Equal(t, SchemaPathSchemaless, primaryPath(dec))
}

// Strategy=unknown (default) → unknown (OQ asks customer to declare).
func TestDecideSchema_StrategyUnknown(t *testing.T) {
	dec := decideSchema(schemaState(nil, nil), defaultCfg(t), schemaInputs(SchemaStrategyUnknown))
	require.NotNil(t, dec)
	assert.Equal(t, SchemaPathUnknown, primaryPath(dec))
}

// Strategy typo'd → unknown (OQ flags the typo before any path applies).
func TestDecideSchema_StrategyTypo(t *testing.T) {
	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, nil), defaultCfg(t), schemaInputs("no_schemaz"))
	require.NotNil(t, dec)
	assert.Equal(t, SchemaPathUnknown, primaryPath(dec), "typo'd strategy must not select a downstream path")
}

// Both Confluent + Glue with eligible flags → Paths carries BOTH
// arms ([migrate_glue, schema_linking]) so JSON consumers branching
// on a single slot don't miss the Confluent verdict.
func TestDecideSchema_ConfluentAndGlue(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "7.5.1"
	inputs.ConfluentSRCPEdition = SchemaCPEditionEnterprise
	inputs.SourceSROutboundReachableToCC = ptrBool(true)

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, []string{"my-glue"}), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	assert.Equal(t, SchemaSourceConfluentAndGlue, dec.Source)
	require.Len(t, dec.Paths, 2, "dual-source must carry both arms in Paths")
	assert.Equal(t, SchemaPathMigrateGlue, dec.Paths[0], "Glue path renders first")
	assert.Equal(t, SchemaPathSchemaLinking, dec.Paths[1], "Confluent verdict in second slot")
	assert.True(t, hasPath(dec, SchemaPathSchemaLinking))
	assert.True(t, hasPath(dec, SchemaPathMigrateGlue))
	require.NotNil(t, dec.MeetsCPVersionFloor, "Confluent eligibility populated even when Glue is the leading verdict")
}

// Dual-source with Confluent arm UNDECLARED — Paths must NOT include
// `unknown` (overloaded with the all-unknown fallback). The pending
// signal is carried by the schema_linking_eligibility_unknown OQ.
func TestDecideSchema_ConfluentAndGlue_PendingConfluentArmDropped(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	// All eligibility inputs left nil → Confluent arm unknown.

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, []string{"my-glue"}), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	require.Len(t, dec.Paths, 1, "pending Confluent arm must NOT serialise as Paths[1]==unknown")
	assert.Equal(t, SchemaPathMigrateGlue, dec.Paths[0])
	assert.False(t, hasPath(dec, SchemaPathUnknown))
}

// Dual-source with Confluent arm INELIGIBLE (e.g. CP below floor) —
// Paths carries [MigrateGlue, DeferToAccount].
func TestDecideSchema_ConfluentAndGlue_ConfluentArmIneligible(t *testing.T) {
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "6.2.1"
	inputs.ConfluentSRCPEdition = SchemaCPEditionEnterprise
	inputs.SourceSROutboundReachableToCC = ptrBool(true)

	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, []string{"my-glue"}), defaultCfg(t), inputs)
	require.NotNil(t, dec)
	require.Len(t, dec.Paths, 2)
	assert.Equal(t, SchemaPathMigrateGlue, dec.Paths[0])
	assert.Equal(t, SchemaPathDeferToAccount, dec.Paths[1])
}

// State has no SR + customer declared `adopt_schemas_during_migration`
// (the schemas-on-the-roadmap branch). Verdict resolves to `unknown` —
// the customer's intent is real but kcp can't recommend a concrete
// path until a source SR is scanned or "no existing SR" is confirmed.
func TestDecideSchema_NoneWithAdoptStrategy(t *testing.T) {
	dec := decideSchema(schemaState(nil, nil), defaultCfg(t), schemaInputs(SchemaStrategyAdoptSchemasDuringMigration))
	require.NotNil(t, dec)
	assert.Equal(t, SchemaSourceNone, dec.Source)
	assert.Equal(t, SchemaPathUnknown, primaryPath(dec))
	assert.Nil(t, dec.MeetsCPVersionFloor, "eligibility flags must not be populated when no Confluent SR was scanned")
}

// Customer declared `no_schemas` but the scan found a Confluent SR.
// Verdict short-circuits to `unknown` WITHOUT populating eligibility
// flags — the renderer would otherwise show a misleading "❔ unknown"
// 3-row table to someone who said they're not migrating schemas.
// The mismatch OQ carries the contradiction.
func TestDecideSchema_NoSchemasButConfluentDetected(t *testing.T) {
	dec := decideSchema(schemaState([]string{"https://csr.example.com"}, nil), defaultCfg(t), schemaInputs(SchemaStrategyNoSchemas))
	require.NotNil(t, dec)
	assert.Equal(t, SchemaSourceConfluent, dec.Source)
	assert.Equal(t, SchemaPathUnknown, primaryPath(dec), "verdict short-circuits to unknown; OQ carries the message")
	assert.Nil(t, dec.MeetsCPVersionFloor, "eligibility flags must remain nil on the no_schemas+scanned-SR contradiction")
	assert.Nil(t, dec.MeetsCPEditionRequirement)
	assert.Nil(t, dec.SourceSROutboundReachable)
}

// detectSchemaOpenQuestions emits schema_linking_ineligible when any
// declared flag is false. The body names the failing constraint AND
// the declared value the customer can edit.
func TestDetectSchemaOpenQuestions_IneligibleSurfacesReason(t *testing.T) {
	dec := &SchemaDecision{
		Source:                    SchemaSourceConfluent,
		Paths:                     []SchemaPath{SchemaPathDeferToAccount},
		MeetsCPVersionFloor:       ptrBool(false),
		MeetsCPEditionRequirement: ptrBool(true),
		SourceSROutboundReachable: ptrBool(true),
	}
	inputs := schemaInputs(SchemaStrategyMigrateExistingSchemaRegistry)
	inputs.ConfluentSRCPVersion = "6.2.1"
	oqs := detectSchemaOpenQuestions(dec, defaultCfg(t), inputs)
	require.Len(t, oqs, 1)
	assert.Equal(t, "schema_linking_ineligible", oqs[0].ID)
	assert.Contains(t, oqs[0].Body, "6.2.1", "ineligibility body must surface the declared CP version")
	assert.Contains(t, oqs[0].Body, "`7.0`", "ineligibility body must surface the configured floor from plan-config")
}

// schema_state_strategy_mismatch fires when no_schemas is declared
// but the scan found a registry — 🟡, not 🔴 (could be deliberate).
func TestDetectSchemaOpenQuestions_NoSchemasMismatch(t *testing.T) {
	dec := &SchemaDecision{Source: SchemaSourceConfluent, Paths: []SchemaPath{SchemaPathUnknown}}
	oqs := detectSchemaOpenQuestions(dec, defaultCfg(t), schemaInputs(SchemaStrategyNoSchemas))
	require.NotEmpty(t, oqs)
	found := false
	for _, oq := range oqs {
		if oq.ID == "schema_state_strategy_mismatch" {
			found = true
		}
	}
	assert.True(t, found, "expected schema_state_strategy_mismatch OQ when strategy=no_schemas conflicts with scanned SR")
}

// versionAtLeast handles dot-separated segment comparison + tolerates
// missing trailing segments ("7.0" vs "7.0.0" → equal).
func TestCPVersionAtLeast(t *testing.T) {
	cases := []struct {
		have, floor string
		want        bool
	}{
		{"7.0", "7.0", true},
		{"7.0.0", "7.0", true},
		{"7.5.1", "7.0", true},
		{"6.2.1", "7.0", false},
		{"8.0", "7.0", true},
		{"latest", "7.0", true},        // "latest" always clears any floor
		{"current", "7.0", true},       // case-insensitive alias
		{"LATEST", "7.0", true},        // case-insensitive
		{"7.0.0-rc1", "7.0", true},     // pre-release suffix stripped before compare
		{"7.0+build5", "7.0", true},    // build-metadata suffix stripped
		{"6.2.1-rc3", "7.0", false},    // pre-release stripped, base compares below floor
		{"garbage", "7.0", false},      // truly unparseable → safe direction (false)
		{"7.-1.0", "7.0", false},       // negative segment rejected → unparseable
		{"3.8.x", "2.4.0", true},       // AWS MSK trailing 'x' suffix → 3.8.0 clears 2.4.0
		{"4.0.x.kraft", "2.4.0", true}, // AWS MSK KRaft suffix → 4.0.0 clears 2.4.0
		{"3.9.x.kraft", "3.9.0", true}, // trailing alphanumeric segments treated as 0
		{"3.8.x", "3.8.1", false},      // 'x' is a placeholder; conservatively treated as 0, so 3.8.0 < 3.8.1
		{"x.8.0", "2.4.0", false},      // first segment non-numeric → unparseable, safe direction
	}
	for _, c := range cases {
		t.Run(c.have+"_vs_"+c.floor, func(t *testing.T) {
			assert.Equal(t, c.want, versionAtLeast(c.have, c.floor))
		})
	}
}
