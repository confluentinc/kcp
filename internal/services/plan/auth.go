package plan

import (
	"github.com/confluentinc/kcp/internal/types"
)

// Target auth method tokens. Customer-facing values written into
// `plan-inputs.yaml` and rendered back in the AuthDecision JSON.
const (
	TargetAuthAPIKeys = "confluent_cloud_api_keys"
	TargetAuthMTLS    = "mtls"
	TargetAuthOAuth   = "oauth"
)

// knownTargetAuthMethod reports whether `t` is one of the recognised
// enum values. Empty string counts as known (resolver leaves the
// per-source default in place). Used by the OQ detector to surface
// typos in `plan-inputs.yaml`.
func knownTargetAuthMethod(t string) bool {
	return knownEnum(t, TargetAuthAPIKeys, TargetAuthMTLS, TargetAuthOAuth)
}

// decideAuth produces the per-cluster source→target auth mapping.
// Sources come from the MSK ClientAuthentication detection in
// cluster_signals.go; per-source target defaults come from
// `auth_mapping` in plan-config.yaml.
//
// The customer's `target_auth_method` override (global or per-cluster
// via `clusters[name].target_auth_method`) replaces the default
// target across all source rows for that cluster — the renderer
// surfaces the source/target pair and a `Note` so the customer can
// see the trade-off.
func decideAuth(c types.ProcessedCluster, cfg *PlanConfig, inputs PlanInputsResolved) AuthDecision {
	sources := sourceAuthsDetected(c)
	out := AuthDecision{
		ClusterID:   c.Name,
		SourceAuths: sources,
	}
	if len(sources) == 0 {
		return out
	}
	override := inputs.TargetAuthMethod
	if override != "" && !knownTargetAuthMethod(override) {
		out.OverrideRejected = true
		out.RejectedOverrideValue = override
	}
	for _, src := range sources {
		mapping, ok := cfg.AuthMapping[src]
		if !ok {
			// Unknown source token (shouldn't happen given the closed enum
			// in cluster_signals.go); emit a row with empty target rather
			// than dropping it silently.
			out.TargetMappings = append(out.TargetMappings, AuthMappingRow{SourceAuth: src})
			continue
		}
		row := AuthMappingRow{
			SourceAuth:        src,
			EffectiveTarget:   effectiveTarget(mapping, override),
			GatewayCompatible: mapping.GatewayCompatible,
			TransparentSwap:   mapping.TransparentSwap,
			Note:              mapping.Note,
			Source:            mapping.Source,
			LastVerified:      mapping.LastVerified,
		}
		out.TargetMappings = append(out.TargetMappings, row)
	}
	return out
}

// effectiveTarget returns the customer-overridden target when set
// AND recognised. Empty string means the customer didn't explicitly
// set `target_auth_method` — the per-source default applies. An
// unknown enum value falls back to the per-source default too (the
// target_auth_method_unknown OQ surfaces the typo separately); without
// this guard the §4 Auth table would render the typo as the
// "effective target" while the OQ says "per-source defaults applied"
// — two surfaces disagreeing.
func effectiveTarget(mapping AuthMapping, override string) string {
	if !knownTargetAuthMethod(override) {
		return mapping.Target
	}
	if override == "" {
		return mapping.Target
	}
	return override
}
