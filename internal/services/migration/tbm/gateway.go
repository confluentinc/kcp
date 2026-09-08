package tbm

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/confluentinc/kcp/internal/services/gateway"
	"github.com/goccy/go-yaml"
)

// gatewayApplyResult carries what an apply produced that a later verification
// needs to interpret the cluster's response. Mirrors
// migration.gatewayApplyResult.
type gatewayApplyResult struct {
	// ConfigID is the config revision the API server stored, or "" when the
	// cluster cannot report one — the signal to verify by pod rollout instead.
	ConfigID string
	// BaselineDeploymentGeneration is the backing Deployment's
	// metadata.generation read immediately BEFORE the apply. 0 means it could
	// not be read, which the rollout waits treat as "any generation counts as
	// a bump".
	BaselineDeploymentGeneration int64
}

// cleanGatewayYAML parses config.GatewayYAML and strips the server-managed
// metadata (managedFields, resourceVersion, uid, creationTimestamp,
// generation) and top-level status that a live-read CR carries and that
// server-side apply rejects. Mirrors migration.cleanInitialCR — duplicated,
// not shared (see the tbm package doc comment in state.go).
func cleanGatewayYAML(gatewayYAML string) (map[string]interface{}, error) {
	var obj map[string]interface{}
	if err := yaml.Unmarshal([]byte(gatewayYAML), &obj); err != nil {
		return nil, fmt.Errorf("failed to parse gateway CR YAML: %w", err)
	}
	if metadata, ok := obj["metadata"].(map[string]interface{}); ok {
		delete(metadata, "managedFields")
		delete(metadata, "resourceVersion")
		delete(metadata, "uid")
		delete(metadata, "creationTimestamp")
		delete(metadata, "generation")
	}
	delete(obj, "status")
	return obj, nil
}

// deriveFencedCRYAML builds the fenced CR bytes from the captured gateway CR
// snapshot by replacing config.Route's rules subtree with config.FenceYAML.
// There is no separately-snapshotted fenced CR: this and
// resolveGatewayCapability's detection both derive from the same source, so
// they can never drift from each other.
func deriveFencedCRYAML(config *TBMConfig) ([]byte, error) {
	base, err := cleanGatewayYAML(config.GatewayYAML)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteRulesObj(base, config.Route, []byte(config.FenceYAML))
}

// deriveSwitchedCRYAML builds the switched CR bytes the same way, from
// config.SwitchoverYAML. Switch itself is not yet real; this exists because
// resolveGatewayCapability's detection needs to see both the fenced and
// switched CR to correctly infer verification mode — a detector shown only
// the live gateway could pick rollout verification for a migration that will
// hot-reload, and then observe nothing (see
// migration.ResolveGatewayCapability's comment).
func deriveSwitchedCRYAML(config *TBMConfig) ([]byte, error) {
	base, err := cleanGatewayYAML(config.GatewayYAML)
	if err != nil {
		return nil, err
	}
	return gateway.ReplaceRouteRulesObj(base, config.Route, []byte(config.SwitchoverYAML))
}

// resolveGatewayCapability determines how gateway state transitions will be
// verified on the live cluster, and adopts it for this run. Called once,
// authoritatively, from Fence itself — unlike migration.ResolveGatewayCapability
// there is no separate advisory call at initialize: TBM has one command, not
// migration's separate init/execute split with a review step in between.
func (a *TBMActions) resolveGatewayCapability(ctx context.Context, config *TBMConfig) error {
	fencedCrYAML, err := deriveFencedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive fenced gateway CR: %w", err)
	}
	switchedCrYAML, err := deriveSwitchedCRYAML(config)
	if err != nil {
		return fmt.Errorf("failed to derive switched gateway CR: %w", err)
	}

	capability, err := a.gatewayService.DetectCapability(ctx, config.K8sNamespace, config.InitialCrName,
		gateway.DefaultGatewayConfigPort, fencedCrYAML, switchedCrYAML)
	if err != nil {
		return fmt.Errorf("failed to determine how gateway transitions can be verified: %w", err)
	}

	a.gatewayCapability = capability
	if capability.Advisory != "" {
		a.reporter.detail("%s", capability.Advisory)
	}
	if capability.Mode == gateway.VerifyPerPodConfigID {
		a.reporter.success("Gateway transitions will be verified per pod via %s", gateway.GatewayConfigEndpointPath)
	}
	slog.Debug("resolved gateway verification capability",
		"mode", capability.Mode, "crdSupportsConfigId", capability.CRDSupportsConfigID,
		"hotReloadEnabled", capability.HotReloadEnabled)
	return nil
}

// verifyHotReloadCapability proves the gateway really does apply config
// revisions, before fencing touches any traffic. Ports
// migration.MigrationActions.VerifyHotReloadCapability verbatim — see that
// method's doc comment for why a dedicated, disjoint field manager (configId
// only) is what makes this safe to call at any point, including a resume.
func (a *TBMActions) verifyHotReloadCapability(ctx context.Context, config *TBMConfig) error {
	if !a.gatewayCapability.InjectsConfigID() {
		return nil
	}

	a.reporter.detail("Checking the gateway applies config revisions in place...")

	applied, err := a.applyGatewayConfigIDOnly(ctx, config, "hot-reload check")
	if err != nil {
		return fmt.Errorf("failed to apply the gateway hot-reload check: %w", err)
	}
	if applied.ConfigID == "" {
		return fmt.Errorf("the gateway hot-reload check applied no config revision")
	}

	if err := a.waitForGatewayAccepted(ctx, config, "hot-reload check"); err != nil {
		return err
	}

	if err := a.waitForGatewayConfigApplied(ctx, config, applied, "hot-reload check"); err != nil {
		a.reporter.remediation("A configId-only change must hot-reload without restarting pods. When it never reaches the pods, the\n"+
			"   gateway's config watcher is not running — most often because the gateway holds a trial rather than an\n"+
			"   Enterprise licence. CFK reports success regardless, so check the gateway itself:\n"+
			"   kubectl -n %s logs -l app=%s | grep -i hot-reload", config.K8sNamespace, config.InitialCrName)
		return err
	}

	return nil
}

// applyGatewayCR applies a gateway CR, attaching a fresh config revision id
// when the cluster supports one. Mirrors migration.MigrationActions.applyGatewayCR.
func (a *TBMActions) applyGatewayCR(ctx context.Context, config *TBMConfig, yamlData []byte, step string) (gatewayApplyResult, error) {
	var configID string
	if a.gatewayCapability.InjectsConfigID() {
		var err error
		configID, err = gateway.NewConfigID()
		if err != nil {
			return gatewayApplyResult{}, err
		}
	}

	baseline := a.gatewayDeploymentBaseline(ctx, config, step)

	slog.Debug("applying gateway CR", "step", step, "gateway", config.InitialCrName,
		"configId", configID, "baselineDeploymentGeneration", baseline)

	storedConfigID, err := a.gatewayService.ApplyGatewayYAML(ctx, config.K8sNamespace, config.InitialCrName, yamlData, configID)
	if err != nil {
		return gatewayApplyResult{}, err
	}
	return gatewayApplyResult{ConfigID: storedConfigID, BaselineDeploymentGeneration: baseline}, nil
}

// applyGatewayConfigIDOnly stamps a fresh configId on the gateway without
// applying — or owning — anything else. Used only by verifyHotReloadCapability.
// Mirrors migration.MigrationActions.applyGatewayConfigIDOnly.
func (a *TBMActions) applyGatewayConfigIDOnly(ctx context.Context, config *TBMConfig, step string) (gatewayApplyResult, error) {
	configID, err := gateway.NewConfigID()
	if err != nil {
		return gatewayApplyResult{}, err
	}

	baseline := a.gatewayDeploymentBaseline(ctx, config, step)

	slog.Debug("applying gateway configId only", "step", step, "gateway", config.InitialCrName,
		"configId", configID, "baselineDeploymentGeneration", baseline)

	storedConfigID, err := a.gatewayService.ApplyGatewayConfigID(ctx, config.K8sNamespace, config.InitialCrName, configID)
	if err != nil {
		return gatewayApplyResult{}, err
	}
	return gatewayApplyResult{ConfigID: storedConfigID, BaselineDeploymentGeneration: baseline}, nil
}

// gatewayDeploymentBaseline reads the backing Deployment's generation
// immediately before an apply. A read failure is not fatal — 0 makes the
// rollout path conservative rather than wrong. Mirrors
// migration.MigrationActions.gatewayDeploymentBaseline.
func (a *TBMActions) gatewayDeploymentBaseline(ctx context.Context, config *TBMConfig, step string) int64 {
	baseline, err := a.gatewayService.GetGatewayDeploymentGeneration(ctx, config.K8sNamespace, config.InitialCrName)
	if err != nil {
		slog.Debug("could not read the gateway deployment generation before applying; "+
			"any generation will count as a rollout", "step", step, "error", err)
		return 0
	}
	return baseline
}

// gatewayHotReloadTimeout returns the configId verification deadline, never
// unbounded. Mirrors migration.MigrationActions.gatewayHotReloadTimeout.
func (a *TBMActions) gatewayHotReloadTimeout() time.Duration {
	if a.hotReloadTimeout <= 0 {
		return gateway.DefaultHotReloadTimeout
	}
	return a.hotReloadTimeout
}

// waitForGatewayAccepted blocks until the Confluent operator has accepted the
// gateway CR just applied. Mirrors migration.MigrationActions.waitForGatewayAccepted.
func (a *TBMActions) waitForGatewayAccepted(ctx context.Context, config *TBMConfig, step string) error {
	a.reporter.detail("Waiting for gateway reconcile...")
	slog.Debug("waiting for gateway acceptance", "step", step, "gateway", config.InitialCrName, "rolloutTimeout", a.rolloutTimeout)

	err := a.gatewayService.WaitForGatewayAccepted(ctx, config.K8sNamespace, config.InitialCrName, 2*time.Second, a.rolloutTimeout)
	if err == nil {
		return nil
	}

	var rejected *gateway.GatewayRejectedError
	if errors.As(err, &rejected) {
		a.reporter.remediation("Confluent operator rejected the %s gateway spec. Inspect its view of the gateway:\n"+
			"   kubectl -n %s get gateway %s -o jsonpath='{.status.conditions}'", step, config.K8sNamespace, config.InitialCrName)
		return err
	}
	return fmt.Errorf("failed waiting for gateway reconcile during %s: %w", step, err)
}

// waitForGatewayConfigApplied blocks until every ready gateway pod reports the
// applied configId. Mirrors migration.MigrationActions.waitForGatewayConfigApplied.
func (a *TBMActions) waitForGatewayConfigApplied(ctx context.Context, config *TBMConfig, applied gatewayApplyResult, step string) error {
	a.reporter.detail("Waiting for every gateway pod to apply the new config...")
	slog.Debug("waiting for per-pod configId", "step", step, "configId", applied.ConfigID,
		"port", gateway.DefaultGatewayConfigPort, "hotReloadTimeout", a.gatewayHotReloadTimeout(),
		"rollTimeout", a.rolloutTimeout, "baselineDeploymentGeneration", applied.BaselineDeploymentGeneration)

	err := a.gatewayService.WaitForGatewayConfigID(ctx, config.K8sNamespace, config.InitialCrName, gateway.ConfigWaitOptions{
		ConfigID:                     applied.ConfigID,
		Port:                         gateway.DefaultGatewayConfigPort,
		BaselineDeploymentGeneration: applied.BaselineDeploymentGeneration,
		PollInterval:                 2 * time.Second,
		HotReloadTimeout:             a.gatewayHotReloadTimeout(),
		RollTimeout:                  a.rolloutTimeout,
		OnProgress:                   a.printConfigWaitProgress,
	})
	if err != nil {
		return fmt.Errorf("failed waiting for the gateway to apply the %s config on every pod: %w", step, err)
	}

	a.reporter.success("All gateway pods have applied the new config")
	return nil
}

// verifyGatewayTransition confirms a transition landed, by whichever means the
// cluster supports. Mirrors migration.MigrationActions.verifyGatewayTransition.
func (a *TBMActions) verifyGatewayTransition(ctx context.Context, config *TBMConfig, applied gatewayApplyResult, step string) error {
	if applied.ConfigID != "" {
		return a.waitForGatewayConfigApplied(ctx, config, applied, step)
	}

	a.reporter.detail("Waiting for gateway readiness...")
	slog.Debug("waiting for gateway readiness", "step", step, "rolloutTimeout", a.rolloutTimeout,
		"baselineDeploymentGeneration", applied.BaselineDeploymentGeneration)

	if err := a.gatewayService.WaitForGatewayReady(ctx, config.K8sNamespace, config.InitialCrName,
		applied.BaselineDeploymentGeneration, 5*time.Second, a.rolloutTimeout, a.printGatewayReadinessProgress); err != nil {
		return fmt.Errorf("failed waiting for gateway readiness during %s: %w", step, err)
	}
	return nil
}

// printConfigWaitProgress renders one line per poll tick of the per-pod
// configId wait. Mirrors migration.MigrationActions.printConfigWaitProgress.
func (a *TBMActions) printConfigWaitProgress(p gateway.ConfigWaitProgress) {
	if p.Converged {
		return
	}
	if p.Mechanism == gateway.MechanismPodRoll {
		a.reporter.detail("Gateway pods are rolling — %d/%d have applied the new config (elapsed %s)",
			p.PodsAtWant, p.PodsReady, formatElapsed(p.Elapsed))
		return
	}
	a.reporter.detail("Applying in place, no pod restart — %d/%d gateway pods have applied the new config (elapsed %s)",
		p.PodsAtWant, p.PodsReady, formatElapsed(p.Elapsed))
}

// printGatewayReadinessProgress renders WaitForGatewayReady progress. Mirrors
// migration.MigrationActions.printGatewayReadinessProgress.
func (a *TBMActions) printGatewayReadinessProgress(p gateway.GatewayReadinessProgress) {
	if !p.RolloutDetected {
		a.reporter.success("No pod restart required")
		return
	}
	if p.InitialPodCount > 0 {
		a.reporter.detail("%d/%d pods ready (elapsed %s)", p.PodsReady, p.InitialPodCount, formatElapsed(p.Elapsed))
	} else {
		a.reporter.detail("gateway reconciling (elapsed %s)", formatElapsed(p.Elapsed))
	}
}

// formatElapsed formats a duration to whole seconds. Mirrors migration.formatElapsed.
func formatElapsed(d time.Duration) string {
	return d.Round(time.Second).String()
}
