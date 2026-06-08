package plan

import (
	"fmt"
	"strings"

	"github.com/confluentinc/kcp/internal/types"
)

// Plan-input enum tokens for schema migration. Stable customer-facing
// strings — surfaced in plan-inputs.yaml and the rendered Plan.
const (
	SchemaStrategyUnknown                       = "unknown"
	SchemaStrategyNoSchemas                     = "no_schemas"
	SchemaStrategyAdoptSchemasDuringMigration   = "adopt_schemas_during_migration"
	SchemaStrategyMigrateExistingSchemaRegistry = "migrate_existing_schema_registry"

	SchemaCPEditionEnterprise = "enterprise"
	SchemaCPEditionCommunity  = "community"
)

// knownSchemaStrategy reports whether `value` is a recognised
// schema_strategy token (empty value resolves to the configured
// default in the resolver, so empty is treated as known here).
func knownSchemaStrategy(value string) bool {
	return knownEnum(value, SchemaStrategyUnknown,
		SchemaStrategyNoSchemas,
		SchemaStrategyAdoptSchemasDuringMigration,
		SchemaStrategyMigrateExistingSchemaRegistry)
}

// knownSchemaCPEdition reports whether `value` is a recognised
// confluent_sr_cp_edition token. Empty (default) is known.
func knownSchemaCPEdition(value string) bool {
	return knownEnum(value, SchemaCPEditionEnterprise, SchemaCPEditionCommunity)
}

// decideSchema produces the fleet-wide schema migration recommendation.
// The branches:
//   - Glue detected                    → kcp_migrate_schemas_glue
//   - Confluent + eligible             → schema_linking
//   - Confluent + ineligible           → defer_to_account_team
//   - Confluent + eligibility unknown  → unknown (OQ asks customer)
//   - Confluent + Glue (rare)          → migrate Glue AND confluent verdict
//   - None + no_schemas                → schemaless (section omitted)
//   - SR scanned + no_schemas          → unknown + mismatch OQ
//   - Strategy unknown                 → unknown (OQ asks to declare)
//   - Strategy typo'd                  → unknown (OQ flags the invalid value)
//
// The result's `Paths` slice carries every verdict that applies —
// usually one entry, two for the dual-source case so JSON consumers
// branching on a single slot don't miss the second arm.
func decideSchema(state types.ProcessedState, cfg *PlanConfig, inputs types.PlanInputsResolved) *types.SchemaDecision {
	source, confluentURLs, glueNames := detectSchemaSource(state)
	strategy := inputs.SchemaStrategy
	if strategy == "" {
		strategy = SchemaStrategyUnknown
	}

	dec := &types.SchemaDecision{
		Source:          source,
		ConfluentSRURLs: confluentURLs,
		GlueRegistries:  glueNames,
	}

	// Strategy typo / unknown: surface the OQ before any other branch
	// (typo wins — emitting a verdict against an unrecognised strategy
	// would silently override the customer's intent).
	if !knownSchemaStrategy(strategy) || strategy == SchemaStrategyUnknown {
		dec.Paths = []types.SchemaPath{types.SchemaPathUnknown}
		return dec
	}

	// Customer declared no_schemas. Two outcomes:
	//   (a) source==None → schemaless (section will be omitted by Build).
	//   (b) source has an SR → contradiction. Don't populate eligibility
	//       flags (the table would render as "❔ unknown" across the
	//       board, which is misleading when the customer's intent is to
	//       skip schemas entirely). The 🟡 schema_state_strategy_mismatch
	//       OQ carries the message.
	if strategy == SchemaStrategyNoSchemas {
		if source == types.SchemaSourceNone {
			dec.Paths = []types.SchemaPath{types.SchemaPathSchemaless}
		} else {
			dec.Paths = []types.SchemaPath{types.SchemaPathUnknown}
		}
		return dec
	}

	switch source {
	case types.SchemaSourceGlue:
		dec.Paths = []types.SchemaPath{types.SchemaPathMigrateGlue}
		return dec
	case types.SchemaSourceConfluent:
		fillConfluentEligibility(dec, cfg, inputs)
		dec.Paths = []types.SchemaPath{confluentEligibilityPath(dec)}
		return dec
	case types.SchemaSourceConfluentAndGlue:
		// Two arms apply concurrently. Glue first (it's the
		// automatable path) so the renderer leads with the actionable
		// command; Confluent verdict second so the customer sees both
		// without re-running the Plan.
		//
		// We only append the Confluent arm when it RESOLVED to a
		// real verdict (Schema Linking or defer-to-account). If the
		// Confluent arm is still pending (eligibility flags
		// undeclared), don't put `unknown` in the Paths slice — a
		// JSON consumer can't distinguish "Confluent arm pending OQ"
		// from "verdict undecidable" by looking at the slice. The
		// `schema_linking_eligibility_unknown` OQ carries the gap.
		fillConfluentEligibility(dec, cfg, inputs)
		dec.Paths = []types.SchemaPath{types.SchemaPathMigrateGlue}
		if confluent := confluentEligibilityPath(dec); confluent != types.SchemaPathUnknown {
			dec.Paths = append(dec.Paths, confluent)
		}
		return dec
	}

	// Source = none AND strategy is one of the schemas-on-the-roadmap
	// values (`adopt_schemas_during_migration` or
	// `migrate_existing_schema_registry`). The customer has declared
	// intent but no source SR was scanned — the path is unknown until
	// they either rerun the scan or confirm "no existing SR".
	dec.Paths = []types.SchemaPath{types.SchemaPathUnknown}
	return dec
}

// confluentEligibilityPath maps the three-flag eligibility verdict
// onto the corresponding SchemaPath. Extracted from decideSchema so
// both the pure-Confluent and the dual ConfluentAndGlue branches share
// one rule.
func confluentEligibilityPath(dec *types.SchemaDecision) types.SchemaPath {
	switch schemaLinkingEligibilityVerdict(dec) {
	case eligibilityVerdictEligible:
		return types.SchemaPathSchemaLinking
	case eligibilityVerdictIneligible:
		return types.SchemaPathDeferToAccount
	default:
		return types.SchemaPathUnknown
	}
}

// primaryPath returns the first Path on a SchemaDecision, or
// SchemaPathUnknown if Paths is empty. Package-private — only test
// assertions care about the leading verdict; production code uses
// hasPath or reads `dec.Paths` directly.
func primaryPath(dec *types.SchemaDecision) types.SchemaPath {
	if dec == nil || len(dec.Paths) == 0 {
		return types.SchemaPathUnknown
	}
	return dec.Paths[0]
}

// sourceTouchesConfluent reports whether the detected source includes
// a Confluent Schema Registry (solo or alongside Glue). Used by the
// eligibility-OQ detectors and the renderer's preamble gate so the
// predicate doesn't drift across callsites.
func sourceTouchesConfluent(s types.SchemaSource) bool {
	return s == types.SchemaSourceConfluent || s == types.SchemaSourceConfluentAndGlue
}

// hasPath reports whether a SchemaDecision's Paths slice contains `p`.
// Used by Build to decide whether to suppress the §Schema section
// (schemaless) and by the renderer for path-specific rendering.
func hasPath(dec *types.SchemaDecision, p types.SchemaPath) bool {
	if dec == nil {
		return false
	}
	for _, dp := range dec.Paths {
		if dp == p {
			return true
		}
	}
	return false
}

// detectSchemaSource collapses the scanner's two-bucket state shape
// (state.SchemaRegistries.ConfluentSchemaRegistry +
// state.SchemaRegistries.AWSGlue) into a single enum + the lookups the
// renderer needs (URLs + registry names).
func detectSchemaSource(state types.ProcessedState) (types.SchemaSource, []string, []string) {
	srs := state.SchemaRegistries
	if srs == nil {
		return types.SchemaSourceNone, nil, nil
	}
	confluentURLs := make([]string, 0, len(srs.ConfluentSchemaRegistry))
	for _, sr := range srs.ConfluentSchemaRegistry {
		confluentURLs = append(confluentURLs, sr.URL)
	}
	glueNames := make([]string, 0, len(srs.AWSGlue))
	for _, gr := range srs.AWSGlue {
		glueNames = append(glueNames, gr.RegistryName)
	}
	switch {
	case len(confluentURLs) > 0 && len(glueNames) > 0:
		return types.SchemaSourceConfluentAndGlue, confluentURLs, glueNames
	case len(confluentURLs) > 0:
		return types.SchemaSourceConfluent, confluentURLs, nil
	case len(glueNames) > 0:
		return types.SchemaSourceGlue, nil, glueNames
	default:
		return types.SchemaSourceNone, nil, nil
	}
}

// fillConfluentEligibility populates the three Schema-Linking flags
// on `dec` from the customer-declared CP version + edition +
// reachability inputs. Tri-state (nil = unknown) so the verdict can
// distinguish "verified false" from "not declared yet" downstream.
func fillConfluentEligibility(dec *types.SchemaDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) {
	if inputs.ConfluentSRCPVersion != "" {
		v := versionAtLeast(inputs.ConfluentSRCPVersion, cfg.SchemaLinking.MinCPVersion)
		dec.MeetsCPVersionFloor = &v
	}
	if inputs.ConfluentSRCPEdition != "" && knownSchemaCPEdition(inputs.ConfluentSRCPEdition) {
		v := inputs.ConfluentSRCPEdition == cfg.SchemaLinking.RequiresCPEdition
		dec.MeetsCPEditionRequirement = &v
	}
	if inputs.SourceSROutboundReachableToCC != nil {
		v := *inputs.SourceSROutboundReachableToCC
		dec.SourceSROutboundReachable = &v
	}
}

type eligibilityVerdict int

const (
	eligibilityVerdictUnknown eligibilityVerdict = iota
	eligibilityVerdictEligible
	eligibilityVerdictIneligible
)

// schemaLinkingEligibilityVerdict folds the three tri-state flags into
// one verdict: any "verified false" → ineligible, any nil → unknown,
// all "verified true" → eligible.
func schemaLinkingEligibilityVerdict(dec *types.SchemaDecision) eligibilityVerdict {
	flags := []*bool{dec.MeetsCPVersionFloor, dec.MeetsCPEditionRequirement, dec.SourceSROutboundReachable}
	anyUnknown := false
	for _, f := range flags {
		if f == nil {
			anyUnknown = true
			continue
		}
		if !*f {
			return eligibilityVerdictIneligible
		}
	}
	if anyUnknown {
		return eligibilityVerdictUnknown
	}
	return eligibilityVerdictEligible
}

// detectSchemaOpenQuestions surfaces what the customer must close
// before the schema-migration verdict above is fully reliable. Called
// after decideSchema so the detector can read the verdict directly
// rather than recomputing eligibility flags. Takes `cfg` so the OQ
// body can quote the configured CP-version floor + edition the same
// way the rendered eligibility table does — keeps the two surfaces
// in lockstep if an admin tunes `plan-config.yaml`.
func detectSchemaOpenQuestions(dec *types.SchemaDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) []types.OpenQuestion {
	if dec == nil {
		return nil
	}
	var oqs []types.OpenQuestion

	strategy := inputs.SchemaStrategy
	if strategy == "" {
		strategy = SchemaStrategyUnknown
	}

	// Strategy typo (recognised values + the explicit `unknown` token
	// keep this silent; anything else fires).
	if !knownSchemaStrategy(strategy) {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "schema_strategy_invalid",
			Title:      "`schema_strategy` is not a recognised value — set to one of the four enum tokens",
			Body:       "Recognised values: `unknown` | `no_schemas` | `adopt_schemas_during_migration` | `migrate_existing_schema_registry`. The current value falls outside the enum, so the Plan treats it as `unknown` and emits this OQ.",
			HowToClose: "In `plan-inputs.yaml`:\n```yaml\nschema_strategy: migrate_existing_schema_registry   # unknown | no_schemas | adopt_schemas_during_migration | migrate_existing_schema_registry\n```",
		})
		return oqs
	}

	// Strategy explicitly unknown — the customer hasn't declared
	// intent. Suppress on the pure-Glue branch: the kcp-automated
	// migration command decides itself, no strategy declaration
	// needed. The dual `ConfluentAndGlue` case still fires the OQ
	// because the Confluent arm requires the strategy.
	if strategy == SchemaStrategyUnknown && dec.Source != types.SchemaSourceGlue {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "schema_strategy_unknown",
			Title:      "Schema migration strategy not declared — set `schema_strategy` in `plan-inputs.yaml`",
			Body:       "First-run Plans default `schema_strategy: unknown` so we don't silently emit a schemaless verdict (which would suppress the Red Flag for shops that genuinely have a Schema Registry). Pick the strategy that matches your migration: `no_schemas` (workloads carry no schemas), `adopt_schemas_during_migration` (you want CC Schema Registry but don't have one on-prem), or `migrate_existing_schema_registry` (you have an SR and want it mirrored).",
			HowToClose: "Set `schema_strategy` in `plan-inputs.yaml` to one of `no_schemas | adopt_schemas_during_migration | migrate_existing_schema_registry`, then re-run `kcp report plan`.",
		})
	}

	// State / strategy mismatch: customer declared no_schemas but the
	// scan found a Schema Registry on the source. Yellow rather than
	// red — the customer's intent could be deliberate (we'll discard
	// the SR) or accidental (they forgot it was there). When this
	// fires we SHORT-CIRCUIT the eligibility OQs below: until the
	// mismatch is reconciled, asking about CP version / edition /
	// reachability is premature.
	//
	// CONTRACT for future contributors: any NEW OQ detector unrelated
	// to the mismatch must be appended ABOVE this block, not below.
	// The `return oqs` here intentionally drops the eligibility OQs
	// only; OQs that are independent of the no_schemas contradiction
	// belong above the mismatch gate so they still fire.
	mismatch := strategy == SchemaStrategyNoSchemas && dec.Source != types.SchemaSourceNone
	if mismatch {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "schema_state_strategy_mismatch",
			Title:      "`schema_strategy: no_schemas` but the scan found a Schema Registry on the source",
			Body:       "If you intend to retire the source SR during the migration, this is fine — leave the setting and acknowledge this OQ. If the SR was scanned in error (e.g. it belongs to a different team), narrow the scan scope. Otherwise switch `schema_strategy` to `migrate_existing_schema_registry` so the Plan applies the SR-detected path.",
			HowToClose: "Either keep `schema_strategy: no_schemas` (acknowledge the gap), narrow `kcp scan schema-registry` / `kcp scan glue-schema-registry`, or switch `schema_strategy` to `migrate_existing_schema_registry` and re-run.",
		})
		return oqs
	}

	// Confluent SR detected but Schema-Linking eligibility unknown
	// (one or more of CP version, edition, outbound reachability
	// hasn't been declared). The Plan can't pick between the
	// schema_linking and defer_to_account_team paths until the
	// customer fills these in.
	if sourceTouchesConfluent(dec.Source) &&
		schemaLinkingEligibilityVerdict(dec) == eligibilityVerdictUnknown {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "schema_linking_eligibility_unknown",
			Title:      "Declare source SR CP version, edition, and outbound reachability in `plan-inputs.yaml`",
			Body:       fmt.Sprintf("Schema Linking requires the source SR to (1) be on CP %s or later, (2) run the `%s` edition (other editions do not ship Schema Linking), and (3) be able to make outbound TCP connections to your CC Schema Registry endpoint. Until all three are declared, the Plan can't pick between the Schema Linking path and the account-team-handoff path.", cfg.SchemaLinking.MinCPVersion, cfg.SchemaLinking.RequiresCPEdition),
			HowToClose: "Set `confluent_sr_cp_version`, `confluent_sr_cp_edition`, and `source_sr_outbound_reachable_to_cc` (true|false) in `plan-inputs.yaml`, then re-run `kcp report plan`. If any constraint fails after declaring, the path becomes an account-team conversation — REST API export/import is not a kcp-automated fallback.",
		})
	}

	// Confluent SR detected and verifiably ineligible (at least one
	// declared flag is false). Defer to account team — explain which
	// constraint failed so the customer can decide whether to fix
	// (upgrade CP, change edition, open network reachability) or
	// accept the manual path.
	if sourceTouchesConfluent(dec.Source) &&
		schemaLinkingEligibilityVerdict(dec) == eligibilityVerdictIneligible {
		oqs = append(oqs, types.OpenQuestion{
			ID:         "schema_linking_ineligible",
			Title:      "Schema Linking blocked — fix the failing constraint or defer to your Confluent account team",
			Body:       ineligibilityBody(dec, cfg, inputs),
			HowToClose: fmt.Sprintf("Resolve the failing constraint (upgrade source CP, switch to `%s` edition, open outbound TCP from source SR to CC SR) and re-declare in `plan-inputs.yaml`:\n```yaml\nconfluent_sr_cp_version: \"%s\"           # or later\nconfluent_sr_cp_edition: %s\nsource_sr_outbound_reachable_to_cc: true\n```\nOR confirm with your account team that the manual REST API path is acceptable.", cfg.SchemaLinking.RequiresCPEdition, cfg.SchemaLinking.MinCPVersion, cfg.SchemaLinking.RequiresCPEdition),
		})
	}

	return oqs
}

// ineligibilityBody renders the schema_linking_ineligible OQ body
// with the *declared* values surfaced (e.g. "declared as `6.2.1`
// is below the `7.0` floor") so the customer sees exactly which input
// to change. The configured floor + required edition come from `cfg`
// so the OQ stays in lockstep with the eligibility table whenever an
// admin tunes plan-config.yaml. Joins failure reasons with "; " so
// the list reads as one sentence.
func ineligibilityBody(dec *types.SchemaDecision, cfg *PlanConfig, inputs types.PlanInputsResolved) string {
	var reasons []string
	if dec.MeetsCPVersionFloor != nil && !*dec.MeetsCPVersionFloor {
		reasons = append(reasons, fmt.Sprintf("CP version declared as `%s` is below the `%s` floor", inputs.ConfluentSRCPVersion, cfg.SchemaLinking.MinCPVersion))
	}
	if dec.MeetsCPEditionRequirement != nil && !*dec.MeetsCPEditionRequirement {
		reasons = append(reasons, fmt.Sprintf("CP edition declared as `%s` (Schema Linking requires `%s`)", inputs.ConfluentSRCPEdition, cfg.SchemaLinking.RequiresCPEdition))
	}
	if dec.SourceSROutboundReachable != nil && !*dec.SourceSROutboundReachable {
		reasons = append(reasons, "source SR cannot reach CC SR outbound (Schema Linking's Schema Exporter is source → CC, one-directional)")
	}
	if len(reasons) == 0 {
		return "(constraint failed but reason was not captured)"
	}
	return "Failing constraint" + pluralize("", len(reasons)) + ": " + strings.Join(reasons, "; ") + ". REST API export/import is technically possible but carries complexity that kcp does not automate today (consumer-by-consumer subject coordination, ID remapping when destinations already have schemas)."
}
