package manifest

import (
	"fmt"
	"strings"
	"time"
)

// MigrationType is the migration an operator declared, by which of the three
// mutually exclusive spec sections the manifest carries. The values match the
// section names, and the static/dynamic vocabulary of the Gateway CRD's route
// modes. Mapping to the state-machine packages: STATIC is AAO
// (internal/services/migration), DYNAMIC is TBM
// (internal/services/migration/tbm), DYNAMIC_TO_STATIC is d2s
// (internal/services/migration/d2s).
type MigrationType string

const (
	MigrationTypeStatic          MigrationType = "STATIC"
	MigrationTypeDynamic         MigrationType = "DYNAMIC"
	MigrationTypeDynamicToStatic MigrationType = "DYNAMIC_TO_STATIC"
)

// The spec section keys, one per migration type.
const (
	SectionStaticMigration           = "staticMigration"
	SectionDynamicMigration          = "dynamicMigration"
	SectionDynamicToStaticConversion = "dynamicToStaticConversion"
)

var sectionKeys = []string{SectionStaticMigration, SectionDynamicMigration, SectionDynamicToStaticConversion}

// SectionKey returns the spec key that declares t, or "" for an unknown type.
func (t MigrationType) SectionKey() string {
	switch t {
	case MigrationTypeStatic:
		return SectionStaticMigration
	case MigrationTypeDynamic:
		return SectionDynamicMigration
	case MigrationTypeDynamicToStatic:
		return SectionDynamicToStaticConversion
	}
	return ""
}

// RouteRef names the route on the live Gateway CR to act on and the streaming
// domain it ends up bound to. There is no bootstrapServerId and no mode field:
// the bootstrap server id is derived from the live CR on every run, and the
// route's live mode is checked against the declared section by the reconcile
// preconditions.
type RouteRef struct {
	// Name is the spec.routes[].name of the route to fence and switch over.
	// Must exist in the initial CR. On a static route it must carry no fence
	// other than kcp's own.
	Name string `yaml:"name" json:"name"`
	// TargetStreamingDomain names a streaming domain already declared in the
	// initial CR's spec.streamingDomains. kcp derives the bootstrap server id
	// to bind the route to from the live CR — it is not written here. Safe with
	// no secret or auth change at cutover only because the route's
	// security.cluster already carries auth for this domain.
	TargetStreamingDomain string `yaml:"targetStreamingDomain" json:"targetStreamingDomain"`
}

// OffsetSyncPause is the static migration's opt-in to pausing the cluster
// link's consumer offset sync around the cutover.
type OffsetSyncPause struct {
	// Pause disables the link's consumer.offset.sync.enable right after the
	// fence and restores it after the switch.
	Pause bool `yaml:"pause,omitempty" json:"pause,omitempty"`
	// Baseline is the state consumer.offset.sync.enable was in BEFORE this
	// migration started ("enabled" or "disabled"). Required when Pause is set:
	// a resumed run cannot observe the pre-migration value (a fenced link with
	// sync disabled is ambiguous), so the operator declares it and kcp restores
	// sync to it at the end.
	Baseline string `yaml:"baseline,omitempty" json:"baseline,omitempty"`
}

// CommonPolicies are the policy fields every section carries.
type CommonPolicies struct {
	// HotReloadTimeout bounds the per-pod configId verification used when the
	// gateway supports hot-reload. 0 uses gateway.DefaultHotReloadTimeout; unlike
	// RolloutTimeout this is never unbounded, since a hot-reload moves no
	// Kubernetes signal to wait on.
	HotReloadTimeout time.Duration `yaml:"hotReloadTimeout,omitempty" json:"hotReloadTimeout,omitempty"`
	// GatewayConfigPort is the port serving the gateway's /config endpoint,
	// polled per pod to confirm a config revision was applied. 0 uses the
	// gateway default (9180).
	GatewayConfigPort int `yaml:"gatewayConfigPort,omitempty" json:"gatewayConfigPort,omitempty"`
}

// StaticPolicies are the execute-time knobs of a static (all-at-once)
// migration. Every value is optional and zero carries meaning: 0 promotes all
// at once / imposes no deadline / skips the check. Each is a default that
// `kcp migration cutover execute` can override with a per-policy flag for one run.
type StaticPolicies struct {
	LagThreshold     int `yaml:"lagThreshold,omitempty" json:"lagThreshold,omitempty"`
	PromoteBatchSize int `yaml:"promoteBatchSize,omitempty" json:"promoteBatchSize,omitempty"`
	// RolloutTimeout bounds the gateway rollout. 0 means no deadline.
	RolloutTimeout time.Duration `yaml:"rolloutTimeout,omitempty" json:"rolloutTimeout,omitempty"`
	// DetectUnroutedProducersDuration opts in to the pre-switchover unrouted
	// producer check; a detected increase aborts before switchover. 0 SKIPS the
	// check entirely; when set the minimum is 10s.
	DetectUnroutedProducersDuration time.Duration `yaml:"detectUnroutedProducersDuration,omitempty" json:"detectUnroutedProducersDuration,omitempty"`
	// ConsumerOffsetSyncDrainDuration waits after fencing, before disabling
	// consumer offset sync. 0 means no wait; it has no effect unless
	// consumerOffsetSync.pause is set.
	ConsumerOffsetSyncDrainDuration time.Duration `yaml:"consumerOffsetSyncDrainDuration,omitempty" json:"consumerOffsetSyncDrainDuration,omitempty"`
	CommonPolicies                  `yaml:",inline"`
}

// DynamicPolicies are the execute-time knobs of a dynamic (topic-based)
// migration.
type DynamicPolicies struct {
	LagThreshold     int `yaml:"lagThreshold,omitempty" json:"lagThreshold,omitempty"`
	PromoteBatchSize int `yaml:"promoteBatchSize,omitempty" json:"promoteBatchSize,omitempty"`
	// RolloutTimeout bounds the gateway rollout. 0 means no deadline.
	RolloutTimeout time.Duration `yaml:"rolloutTimeout,omitempty" json:"rolloutTimeout,omitempty"`
	// DetectUnroutedProducersDuration is the verify_fence monitoring window.
	// 0 SKIPS the check; when set the minimum is 10s.
	DetectUnroutedProducersDuration time.Duration `yaml:"detectUnroutedProducersDuration,omitempty" json:"detectUnroutedProducersDuration,omitempty"`
	CommonPolicies                  `yaml:",inline"`
}

// DynamicToStaticPolicies are the execute-time knobs of a dynamic-to-static
// route conversion.
type DynamicToStaticPolicies struct {
	// RolloutTimeout bounds the gateway rollout. 0 means no deadline.
	RolloutTimeout time.Duration `yaml:"rolloutTimeout,omitempty" json:"rolloutTimeout,omitempty"`
	// DetectUnroutedCommitsDuration is the wait between the two committed-offset
	// snapshots taken after fencing, to catch a consumer that bypasses the
	// gateway and commits to the source directly. Its own field, not a reuse of
	// detectUnroutedProducersDuration, because that field's 0 means "skip the
	// check" and this check can never be skipped. 0 uses the built-in 30s; when
	// set the minimum is 10s (two default auto-commit intervals).
	DetectUnroutedCommitsDuration time.Duration `yaml:"detectUnroutedCommitsDuration,omitempty" json:"detectUnroutedCommitsDuration,omitempty"`
	// OffsetSyncConcurrency is how many workers, each with its own broker
	// connection, read and write consumer-group offsets. 0 uses the built-in 8.
	OffsetSyncConcurrency int `yaml:"offsetSyncConcurrency,omitempty" json:"offsetSyncConcurrency,omitempty"`
	CommonPolicies        `yaml:",inline"`
}

// EffectiveDetectUnroutedCommitsDuration resolves 0 to the built-in default.
func (p DynamicToStaticPolicies) EffectiveDetectUnroutedCommitsDuration() time.Duration {
	if p.DetectUnroutedCommitsDuration == 0 {
		return DefaultDetectUnroutedCommitsDuration
	}
	return p.DetectUnroutedCommitsDuration
}

// EffectiveOffsetSyncConcurrency resolves 0 to the built-in default.
func (p DynamicToStaticPolicies) EffectiveOffsetSyncConcurrency() int {
	if p.OffsetSyncConcurrency == 0 {
		return DefaultOffsetSyncConcurrency
	}
	return p.OffsetSyncConcurrency
}

// StaticMigration migrates topics across a static route (AAO): every selected
// topic cuts over together.
type StaticMigration struct {
	Route RouteRef `yaml:"route" json:"route"`
	// TopicGroup pairs the topic selection that migrates. Exactly one entry
	// today.
	TopicGroup         []TopicGroupEntry `yaml:"topicGroup" json:"topicGroup"`
	ConsumerOffsetSync *OffsetSyncPause  `yaml:"consumerOffsetSync,omitempty" json:"consumerOffsetSync,omitempty"`
	Policies           StaticPolicies    `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// DynamicMigration migrates topics across a dynamic route (TBM): topics cut
// over incrementally, promoting mirror topics at zero lag.
type DynamicMigration struct {
	Route      RouteRef          `yaml:"route" json:"route"`
	TopicGroup []TopicGroupEntry `yaml:"topicGroup" json:"topicGroup"`
	Policies   DynamicPolicies   `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// DynamicToStaticConversion converts a fully-migrated dynamic route to a static
// route bound to Route.TargetStreamingDomain, carrying consumer-group offsets
// across. It selects no topics: the cluster link is its scope.
type DynamicToStaticConversion struct {
	Route    RouteRef                `yaml:"route" json:"route"`
	Policies DynamicToStaticPolicies `yaml:"policies,omitempty" json:"policies,omitempty"`
}

// setSections returns the keys of the sections present, in canonical order.
func (s GatewaySpec) setSections() []string {
	var set []string
	if s.StaticMigration != nil {
		set = append(set, SectionStaticMigration)
	}
	if s.DynamicMigration != nil {
		set = append(set, SectionDynamicMigration)
	}
	if s.DynamicToStaticConversion != nil {
		set = append(set, SectionDynamicToStaticConversion)
	}
	return set
}

// MigrationType returns the declared type, or "" unless exactly one section is
// set. It is the only code that inspects which section is present: all dispatch
// goes through it.
func (s GatewaySpec) MigrationType() MigrationType {
	if len(s.setSections()) != 1 {
		return ""
	}
	switch {
	case s.StaticMigration != nil:
		return MigrationTypeStatic
	case s.DynamicMigration != nil:
		return MigrationTypeDynamic
	default:
		return MigrationTypeDynamicToStatic
	}
}

// DeclaredRoute returns the declared section's route, or the zero RouteRef.
func (s GatewaySpec) DeclaredRoute() RouteRef {
	switch s.MigrationType() {
	case MigrationTypeStatic:
		return s.StaticMigration.Route
	case MigrationTypeDynamic:
		return s.DynamicMigration.Route
	case MigrationTypeDynamicToStatic:
		return s.DynamicToStaticConversion.Route
	}
	return RouteRef{}
}

// DeclaredTopicGroup returns the declared migration's topic selection; nil for
// a conversion (which selects no topics) or an undeclared type.
func (s GatewaySpec) DeclaredTopicGroup() []TopicGroupEntry {
	switch s.MigrationType() {
	case MigrationTypeStatic:
		return s.StaticMigration.TopicGroup
	case MigrationTypeDynamic:
		return s.DynamicMigration.TopicGroup
	}
	return nil
}

// DeclaredOffsetSync returns the static migration's offset-sync pause and its
// declared baseline; (false, "") for every other type.
func (s GatewaySpec) DeclaredOffsetSync() (pause bool, baseline string) {
	if s.MigrationType() == MigrationTypeStatic && s.StaticMigration.ConsumerOffsetSync != nil {
		c := s.StaticMigration.ConsumerOffsetSync
		return c.Pause, c.Baseline
	}
	return false, ""
}

// ValidatePolicies validates the declared section's policies. It is exported
// because `kcp migration cutover execute` re-validates after applying command-line
// overrides, which can introduce a value the manifest itself never carried.
// With no (or several) sections declared it returns nil: Validate reports that.
func (s GatewaySpec) ValidatePolicies() []error {
	switch s.MigrationType() {
	case MigrationTypeStatic:
		return s.StaticMigration.Policies.Validate("spec." + SectionStaticMigration + ".policies")
	case MigrationTypeDynamic:
		return s.DynamicMigration.Policies.Validate("spec." + SectionDynamicMigration + ".policies")
	case MigrationTypeDynamicToStatic:
		return s.DynamicToStaticConversion.Policies.Validate("spec." + SectionDynamicToStaticConversion + ".policies")
	}
	return nil
}

// PolicyLogArgs renders the declared section's policy as slog key/value pairs
// for the audit log line.
func (s GatewaySpec) PolicyLogArgs() []any {
	switch s.MigrationType() {
	case MigrationTypeStatic:
		return s.StaticMigration.Policies.LogArgs()
	case MigrationTypeDynamic:
		return s.DynamicMigration.Policies.LogArgs()
	case MigrationTypeDynamicToStatic:
		return s.DynamicToStaticConversion.Policies.LogArgs()
	}
	return nil
}

// logArgs spells out the common policy fields for the audit log; each policies
// type's exported LogArgs appends its own fields to it, so each type's fields
// are spelled out in exactly one place and a new field cannot silently drop out.
func (p CommonPolicies) logArgs() []any {
	return []any{"hot_reload_timeout", p.HotReloadTimeout, "gateway_config_port", p.GatewayConfigPort}
}

func (p StaticPolicies) LogArgs() []any {
	return append([]any{
		"lag_threshold", p.LagThreshold,
		"promote_batch_size", p.PromoteBatchSize,
		"rollout_timeout", p.RolloutTimeout,
		"detect_unrouted_producers_duration", p.DetectUnroutedProducersDuration,
		"consumer_offset_sync_drain_duration", p.ConsumerOffsetSyncDrainDuration,
	}, p.logArgs()...)
}

func (p DynamicPolicies) LogArgs() []any {
	return append([]any{
		"lag_threshold", p.LagThreshold,
		"promote_batch_size", p.PromoteBatchSize,
		"rollout_timeout", p.RolloutTimeout,
		"detect_unrouted_producers_duration", p.DetectUnroutedProducersDuration,
	}, p.logArgs()...)
}

func (p DynamicToStaticPolicies) LogArgs() []any {
	return append([]any{
		"rollout_timeout", p.RolloutTimeout,
		"detect_unrouted_commits_duration", p.EffectiveDetectUnroutedCommitsDuration(),
		"offset_sync_concurrency", p.EffectiveOffsetSyncConcurrency(),
	}, p.logArgs()...)
}

// --- validation ---

func (p CommonPolicies) validate(path string) []error {
	var errs []error
	if p.HotReloadTimeout < 0 {
		errs = append(errs, fmt.Errorf("%s.hotReloadTimeout: must not be negative (0 uses the built-in default)", path))
	}
	if p.GatewayConfigPort < 0 {
		errs = append(errs, fmt.Errorf("%s.gatewayConfigPort: must not be negative (0 uses the default port)", path))
	}
	return errs
}

func validateLagAndBatch(path string, lag, batch int) []error {
	var errs []error
	if lag < 0 {
		errs = append(errs, fmt.Errorf("%s.lagThreshold: must not be negative", path))
	}
	if batch < 0 {
		errs = append(errs, fmt.Errorf("%s.promoteBatchSize: must not be negative (0 promotes all at once)", path))
	}
	return errs
}

func validateRollout(path string, d time.Duration) []error {
	if d < 0 {
		return []error{fmt.Errorf("%s.rolloutTimeout: must not be negative (0 means no deadline)", path)}
	}
	return nil
}

func validateDetectProducers(path string, d time.Duration) []error {
	switch {
	case d < 0:
		return []error{fmt.Errorf("%s.detectUnroutedProducersDuration: must not be negative (0 skips the check)", path)}
	case d > 0 && d < minDetectUnroutedProducersDuration:
		return []error{fmt.Errorf(
			"%s.detectUnroutedProducersDuration: must be at least %s when set (0 skips the check) — a shorter window cannot span a producer's metadata refresh",
			path, minDetectUnroutedProducersDuration)}
	}
	return nil
}

// Validate checks the static policies. path is the field path of the policies
// block, e.g. "spec.staticMigration.policies".
func (p StaticPolicies) Validate(path string) []error {
	errs := validateLagAndBatch(path, p.LagThreshold, p.PromoteBatchSize)
	errs = append(errs, validateRollout(path, p.RolloutTimeout)...)
	errs = append(errs, validateDetectProducers(path, p.DetectUnroutedProducersDuration)...)
	if p.ConsumerOffsetSyncDrainDuration < 0 {
		errs = append(errs, fmt.Errorf("%s.consumerOffsetSyncDrainDuration: must not be negative (0 means no wait)", path))
	}
	return append(errs, p.validate(path)...)
}

// Validate checks the dynamic policies.
func (p DynamicPolicies) Validate(path string) []error {
	errs := validateLagAndBatch(path, p.LagThreshold, p.PromoteBatchSize)
	errs = append(errs, validateRollout(path, p.RolloutTimeout)...)
	errs = append(errs, validateDetectProducers(path, p.DetectUnroutedProducersDuration)...)
	return append(errs, p.validate(path)...)
}

// Validate checks the conversion policies.
func (p DynamicToStaticPolicies) Validate(path string) []error {
	errs := validateRollout(path, p.RolloutTimeout)
	switch d := p.DetectUnroutedCommitsDuration; {
	case d < 0:
		errs = append(errs, fmt.Errorf("%s.detectUnroutedCommitsDuration: must not be negative (0 uses the built-in %s)", path, DefaultDetectUnroutedCommitsDuration))
	case d > 0 && d < minDetectUnroutedProducersDuration:
		errs = append(errs, fmt.Errorf(
			"%s.detectUnroutedCommitsDuration: must be at least %s when set (0 uses the built-in %s) — a shorter window can miss a consumer on default auto-commit settings",
			path, minDetectUnroutedProducersDuration, DefaultDetectUnroutedCommitsDuration))
	}
	if p.OffsetSyncConcurrency < 0 {
		errs = append(errs, fmt.Errorf("%s.offsetSyncConcurrency: must not be negative (0 uses the built-in %d)", path, DefaultOffsetSyncConcurrency))
	}
	return append(errs, p.validate(path)...)
}

func validateRouteRef(path string, r RouteRef) []error {
	var errs []error
	if blank(r.Name) {
		errs = append(errs, fmt.Errorf("%s.name: must not be blank", path))
	}
	if blank(r.TargetStreamingDomain) {
		errs = append(errs, fmt.Errorf("%s.targetStreamingDomain: must not be blank", path))
	}
	return errs
}

// validateTopicGroup applies the structural rules for a migration's topic
// selection: exactly one entry with at least one of topics/topicPatterns on it,
// each pattern compiling as an anchored RE2 full-match.
func validateTopicGroup(path string, entries []TopicGroupEntry) []error {
	var errs []error
	add := func(format string, args ...any) { errs = append(errs, fmt.Errorf(format, args...)) }

	if len(entries) != 1 {
		add("%s: must have exactly one entry (got %d)", path, len(entries))
		if len(entries) == 0 {
			return errs
		}
	}
	e := entries[0]
	entry := path + "[0]"
	if e.Topics == nil && e.TopicPatterns == nil {
		add("%s: at least one of topics or topicPatterns is required", entry)
	}
	if e.Topics != nil {
		errs = append(errs, validateSelection(entry+".topics", *e.Topics)...)
	}
	if e.TopicPatterns != nil {
		errs = append(errs, validateSelection(entry+".topicPatterns", *e.TopicPatterns)...)
		for i, pat := range *e.TopicPatterns {
			if blank(pat) {
				continue
			}
			if _, err := anchoredPattern(pat); err != nil {
				add("%s.topicPatterns[%d]: not a valid regular expression: %v", entry, i, err)
			}
		}
	}
	return errs
}

func validateOffsetSyncPause(path string, c *OffsetSyncPause) []error {
	if c == nil {
		return nil
	}
	switch b := c.Baseline; {
	case b == "":
		if c.Pause {
			return []error{fmt.Errorf("%s.baseline: required when pause is set; must be %q or %q",
				path, OffsetSyncBaselineEnabled, OffsetSyncBaselineDisabled)}
		}
	case b != OffsetSyncBaselineEnabled && b != OffsetSyncBaselineDisabled:
		return []error{fmt.Errorf("%s.baseline: must be %q or %q (got %q)",
			path, OffsetSyncBaselineEnabled, OffsetSyncBaselineDisabled, b)}
	}
	return nil
}

// validateSection requires exactly one of the three sections and applies that
// section's rules.
func (s GatewaySpec) validateSection() []error {
	set := s.setSections()
	if len(set) != 1 {
		found := "none"
		if len(set) > 0 {
			found = strings.Join(set, ", ")
		}
		return []error{fmt.Errorf("spec: exactly one of %s is required (found: %s)", strings.Join(sectionKeys, ", "), found)}
	}

	var errs []error
	switch s.MigrationType() {
	case MigrationTypeStatic:
		p := "spec." + SectionStaticMigration
		m := s.StaticMigration
		errs = append(errs, validateRouteRef(p+".route", m.Route)...)
		errs = append(errs, validateTopicGroup(p+".topicGroup", m.TopicGroup)...)
		errs = append(errs, validateOffsetSyncPause(p+".consumerOffsetSync", m.ConsumerOffsetSync)...)
		errs = append(errs, m.Policies.Validate(p+".policies")...)
	case MigrationTypeDynamic:
		p := "spec." + SectionDynamicMigration
		m := s.DynamicMigration
		errs = append(errs, validateRouteRef(p+".route", m.Route)...)
		errs = append(errs, validateTopicGroup(p+".topicGroup", m.TopicGroup)...)
		errs = append(errs, m.Policies.Validate(p+".policies")...)
	case MigrationTypeDynamicToStatic:
		p := "spec." + SectionDynamicToStaticConversion
		m := s.DynamicToStaticConversion
		errs = append(errs, validateRouteRef(p+".route", m.Route)...)
		errs = append(errs, m.Policies.Validate(p+".policies")...)
	}
	return errs
}
