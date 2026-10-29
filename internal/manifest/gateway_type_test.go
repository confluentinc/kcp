package manifest

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestMigrationType_DeclaredSection(t *testing.T) {
	cases := []struct {
		name  string
		block string
		want  MigrationType
		key   string
	}{
		{"static", staticMigrationBlock, MigrationTypeStatic, "staticMigration"},
		{"dynamic", dynamicMigrationBlock, MigrationTypeDynamic, "dynamicMigration"},
		{"conversion", conversionBlock, MigrationTypeDynamicToStatic, "dynamicToStaticConversion"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := parseGateway(t, gatewayDocBase+c.block)
			require.Empty(t, g.Validate())
			assert.Equal(t, c.want, g.Spec.MigrationType())
			assert.Equal(t, c.key, g.Spec.MigrationType().SectionKey())
			r := g.Spec.DeclaredRoute()
			assert.Equal(t, "migration-route", r.Name)
			assert.Equal(t, "confluent-cloud", r.TargetStreamingDomain)
		})
	}
}

func TestMigrationType_NoSectionRejected(t *testing.T) {
	g := parseGateway(t, gatewayDocBase)
	assert.Equal(t, MigrationType(""), g.Spec.MigrationType())
	requireErrContains(t, g.Validate(), "exactly one of staticMigration, dynamicMigration, dynamicToStaticConversion is required")
	requireErrContains(t, g.Validate(), "found: none")
}

// Review Focus 1: a copy-paste leftover must be named, not silently resolved.
func TestMigrationType_SeveralSectionsRejected(t *testing.T) {
	g := parseGateway(t, gatewayDocBase+staticMigrationBlock+dynamicMigrationBlock)
	assert.Equal(t, MigrationType(""), g.Spec.MigrationType(), "two sections must not resolve to either")
	requireErrContains(t, g.Validate(), "found: staticMigration, dynamicMigration")
}

func TestSection_WrongSectionFieldsAreParseErrors(t *testing.T) {
	cases := map[string]string{
		"topicGroup under the conversion":           gatewayDocBase + conversionBlock + topicGroupEntryBlock,
		"consumerOffsetSync under dynamicMigration": gatewayDocBase + dynamicMigrationBlock + "    consumerOffsetSync:\n      pause: true\n      baseline: enabled\n",
		"commits window under staticMigration":      gatewayDocBase + staticMigrationBlock + "    policies:\n      detectUnroutedCommitsDuration: 30s\n",
		"drain under dynamicMigration":              gatewayDocBase + dynamicMigrationBlock + "    policies:\n      consumerOffsetSyncDrainDuration: 15s\n",
		"promoteBatchSize under the conversion":     gatewayDocBase + conversionBlock + "    policies:\n      promoteBatchSize: 5\n",
	}
	for name, doc := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ParseGatewayMigration([]byte(doc))
			require.Error(t, err, "a field that does not belong to the declared section must fail the strict decode")
		})
	}
}

func TestSection_RouteRefMustNotBeBlank(t *testing.T) {
	for _, c := range []struct{ name, block, key string }{
		{"static", staticMigrationBlock, "staticMigration"},
		{"dynamic", dynamicMigrationBlock, "dynamicMigration"},
		{"conversion", conversionBlock, "dynamicToStaticConversion"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := parseGateway(t, gatewayDocBase+strings.Replace(c.block, "      name: migration-route\n", "      name: \"\"\n", 1))
			requireErrContains(t, g.Validate(), "spec."+c.key+".route.name")
			g = parseGateway(t, gatewayDocBase+strings.Replace(c.block, "      targetStreamingDomain: confluent-cloud\n", "      targetStreamingDomain: \"\"\n", 1))
			requireErrContains(t, g.Validate(), "spec."+c.key+".route.targetStreamingDomain")
		})
	}
}

func TestSection_MigrationsRequireATopicSelection(t *testing.T) {
	for _, c := range []struct{ name, block, key string }{
		{"static", staticMigrationBlock, "staticMigration"},
		{"dynamic", dynamicMigrationBlock, "dynamicMigration"},
	} {
		t.Run(c.name, func(t *testing.T) {
			g := parseGateway(t, gatewayDocBase+strings.Replace(c.block, topicGroupEntryBlock, "", 1))
			requireErrContains(t, g.Validate(), "spec."+c.key+".topicGroup")
			g = parseGateway(t, gatewayDocBase+strings.Replace(c.block, topicGroupEntryBlock, topicGroupEntryBlock+"      - topics:\n          - t2.orders\n", 1))
			requireErrContains(t, g.Validate(), "spec."+c.key+".topicGroup")
		})
	}
}

func TestStaticMigration_OffsetSyncBaseline(t *testing.T) {
	cases := []struct {
		name, baseline, wantErrHas string
		pause                      bool
	}{
		{"pause+enabled ok", OffsetSyncBaselineEnabled, "", true},
		{"pause+disabled ok", OffsetSyncBaselineDisabled, "", true},
		{"pause+missing baseline errors", "", "consumerOffsetSync.baseline", true},
		{"pause+invalid baseline errors", "on", "consumerOffsetSync.baseline", true},
		{"no pause, baseline omitted ok", "", "", false},
		{"no pause, invalid baseline still errors", "yes", "consumerOffsetSync.baseline", false},
		{"no pause, valid baseline ok", OffsetSyncBaselineEnabled, "", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			g := parseGateway(t, gatewayDocBase+staticMigrationBlock)
			g.Spec.StaticMigration.ConsumerOffsetSync = &OffsetSyncPause{Pause: c.pause, Baseline: c.baseline}
			errs := g.Validate()
			if c.wantErrHas == "" {
				for _, e := range errs {
					assert.NotContains(t, e.Error(), "baseline")
				}
				return
			}
			requireErrContains(t, errs, c.wantErrHas)
		})
	}
}

func TestStaticMigration_DeclaredOffsetSync(t *testing.T) {
	g := parseGateway(t, gatewayDocBase+staticMigrationBlock+"    consumerOffsetSync:\n      pause: true\n      baseline: disabled\n")
	pause, baseline := g.Spec.DeclaredOffsetSync()
	assert.True(t, pause)
	assert.Equal(t, "disabled", baseline)

	pause, baseline = parseGateway(t, validGatewayDoc).Spec.DeclaredOffsetSync()
	assert.False(t, pause, "a dynamic migration has no offset-sync pause")
	assert.Empty(t, baseline)
}

func TestPolicies_DecodeFromYAMLPerSection(t *testing.T) {
	t.Run("static", func(t *testing.T) {
		g := parseGateway(t, gatewayDocBase+staticMigrationBlock+`    policies:
      lagThreshold: 3
      promoteBatchSize: 100
      rolloutTimeout: 10m
      detectUnroutedProducersDuration: 30s
      consumerOffsetSyncDrainDuration: 15s
      hotReloadTimeout: 45s
      gatewayConfigPort: 9181
`)
		require.Empty(t, g.Validate())
		p := g.Spec.StaticMigration.Policies
		assert.Equal(t, 3, p.LagThreshold)
		assert.Equal(t, 100, p.PromoteBatchSize)
		assert.Equal(t, 10*time.Minute, p.RolloutTimeout)
		assert.Equal(t, 30*time.Second, p.DetectUnroutedProducersDuration)
		assert.Equal(t, 15*time.Second, p.ConsumerOffsetSyncDrainDuration)
		assert.Equal(t, 45*time.Second, p.HotReloadTimeout, "common fields inline into the section's policies")
		assert.Equal(t, 9181, p.GatewayConfigPort)
	})
	t.Run("dynamic", func(t *testing.T) {
		g := parseGateway(t, validGatewayDoc+"    policies:\n      promoteBatchSize: 7\n      detectUnroutedProducersDuration: 10s\n")
		require.Empty(t, g.Validate())
		assert.Equal(t, 7, g.Spec.DynamicMigration.Policies.PromoteBatchSize)
		assert.Equal(t, 10*time.Second, g.Spec.DynamicMigration.Policies.DetectUnroutedProducersDuration)
	})
	t.Run("conversion", func(t *testing.T) {
		g := parseGateway(t, gatewayDocBase+conversionBlock+"    policies:\n      rolloutTimeout: 5m\n      detectUnroutedCommitsDuration: 45s\n      offsetSyncConcurrency: 3\n")
		require.Empty(t, g.Validate())
		p := g.Spec.DynamicToStaticConversion.Policies
		assert.Equal(t, 5*time.Minute, p.RolloutTimeout)
		assert.Equal(t, 45*time.Second, p.DetectUnroutedCommitsDuration)
		assert.Equal(t, 3, p.EffectiveOffsetSyncConcurrency())
	})
}

func TestPolicies_OmittedIsZeroValued(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	require.Empty(t, g.Validate())
	assert.Equal(t, DynamicPolicies{}, g.Spec.DynamicMigration.Policies)
}

func TestDynamicToStaticPolicies_Defaults(t *testing.T) {
	var p DynamicToStaticPolicies
	assert.Zero(t, p.DetectUnroutedCommitsDuration, "an omitted commits window skips the check")
	assert.Equal(t, DefaultOffsetSyncConcurrency, p.EffectiveOffsetSyncConcurrency())
}

func TestPolicies_Validate(t *testing.T) {
	cases := []struct {
		name    string
		errs    []error
		wantErr string // "" = valid
	}{
		{"static unset", StaticPolicies{}.Validate("p"), ""},
		{"static lag negative", StaticPolicies{LagThreshold: -1}.Validate("p"), "p.lagThreshold"},
		{"static promote negative", StaticPolicies{PromoteBatchSize: -1}.Validate("p"), "p.promoteBatchSize"},
		{"static detect below minimum", StaticPolicies{DetectUnroutedProducersDuration: 5 * time.Second}.Validate("p"), "p.detectUnroutedProducersDuration"},
		{"static detect zero skips", StaticPolicies{}.Validate("p"), ""},
		{"static drain negative", StaticPolicies{ConsumerOffsetSyncDrainDuration: -time.Second}.Validate("p"), "p.consumerOffsetSyncDrainDuration"},
		{"static hot reload negative", StaticPolicies{CommonPolicies: CommonPolicies{HotReloadTimeout: -time.Second}}.Validate("p"), "p.hotReloadTimeout"},
		{"dynamic rollout negative", DynamicPolicies{RolloutTimeout: -time.Second}.Validate("p"), "p.rolloutTimeout"},
		{"dynamic port negative", DynamicPolicies{CommonPolicies: CommonPolicies{GatewayConfigPort: -1}}.Validate("p"), "p.gatewayConfigPort"},
		{"d2s commits at minimum", DynamicToStaticPolicies{DetectUnroutedCommitsDuration: 10 * time.Second}.Validate("p"), ""},
		{"d2s commits below minimum", DynamicToStaticPolicies{DetectUnroutedCommitsDuration: 9 * time.Second}.Validate("p"), "p.detectUnroutedCommitsDuration"},
		{"d2s commits negative", DynamicToStaticPolicies{DetectUnroutedCommitsDuration: -time.Second}.Validate("p"), "p.detectUnroutedCommitsDuration"},
		{"d2s concurrency positive", DynamicToStaticPolicies{OffsetSyncConcurrency: 16}.Validate("p"), ""},
		{"d2s concurrency negative", DynamicToStaticPolicies{OffsetSyncConcurrency: -1}.Validate("p"), "p.offsetSyncConcurrency"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if c.wantErr == "" {
				assert.Empty(t, c.errs)
				return
			}
			requireErrContains(t, c.errs, c.wantErr)
		})
	}
}

func TestGatewaySpec_ValidatePoliciesUsesTheDeclaredSection(t *testing.T) {
	g := parseGateway(t, validGatewayDoc)
	g.Spec.DynamicMigration.Policies.LagThreshold = -1
	requireErrContains(t, g.Spec.ValidatePolicies(), "spec.dynamicMigration.policies.lagThreshold")
	assert.Empty(t, GatewaySpec{}.ValidatePolicies(), "no declared section: nothing to validate here (Validate reports the missing section)")
}

// A new policy field must not silently drop out of the audit log line: every
// field of every policies struct (including the inlined common fields) must
// appear as a snake_case key.
func TestPolicyLogArgs_CoverEveryField(t *testing.T) {
	for name, v := range map[string]any{
		"static":  StaticPolicies{},
		"dynamic": DynamicPolicies{},
		"d2s":     DynamicToStaticPolicies{},
	} {
		t.Run(name, func(t *testing.T) {
			var args []any
			switch p := v.(type) {
			case StaticPolicies:
				args = p.LogArgs()
			case DynamicPolicies:
				args = p.LogArgs()
			case DynamicToStaticPolicies:
				args = p.LogArgs()
			}
			keys := map[string]bool{}
			for i := 0; i+1 < len(args); i += 2 {
				keys[args[i].(string)] = true
			}
			var want []string
			var walk func(rt reflect.Type)
			walk = func(rt reflect.Type) {
				for i := 0; i < rt.NumField(); i++ {
					f := rt.Field(i)
					if f.Anonymous {
						walk(f.Type)
						continue
					}
					want = append(want, snakeCase(strings.Split(f.Tag.Get("json"), ",")[0]))
				}
			}
			walk(reflect.TypeOf(v))
			require.NotEmpty(t, want)
			for _, k := range want {
				assert.True(t, keys[k], "LogArgs is missing %q", k)
			}
		})
	}
}

func snakeCase(camel string) string {
	var b strings.Builder
	for i, r := range camel {
		if r >= 'A' && r <= 'Z' {
			if i > 0 {
				b.WriteByte('_')
			}
			b.WriteRune(r + ('a' - 'A'))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

// Review Focus 4: an old-shape manifest gets a targeted hint — and no inference.
func TestParse_LegacyShapeGetsATargetedHint(t *testing.T) {
	legacyRoute := gatewayDocBase + "  route:\n    name: migration-route\n    targetStreamingDomain: confluent-cloud\n" + topicGroupEntryBlock
	legacyPolicies := validGatewayDoc + "  defaultPolicies:\n    lagThreshold: 0\n"
	legacyPause := strings.Replace(validGatewayDoc, "    name: msk-to-cc\n", "    name: msk-to-cc\n    pauseConsumerOffsetSync: true\n    consumerOffsetSyncBaseline: enabled\n", 1)

	for name, doc := range map[string]string{
		"spec.route":                          legacyRoute,
		"spec.defaultPolicies":                legacyPolicies,
		"clusterLink.pauseConsumerOffsetSync": legacyPause,
	} {
		t.Run(name, func(t *testing.T) {
			g, err := ParseGatewayMigration([]byte(doc))
			require.Error(t, err)
			require.Nil(t, g, "an old-shape manifest must not be inferred into a new one")
			assert.Contains(t, err.Error(), "the manifest shape changed")
			assert.Contains(t, err.Error(), "staticMigration, dynamicMigration, dynamicToStaticConversion")
		})
	}

	_, err := ParseGatewayMigration([]byte(validGatewayDoc + "  typoSection: x\n"))
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "the manifest shape changed", "an ordinary typo keeps the plain unknown-field error")
}
