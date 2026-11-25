package reconcile

// MigrationType is the migration the operator declared. It mirrors
// manifest.MigrationType's values: the reconcile core does not import manifest,
// so the migplan layer maps one onto the other. STATIC is the all-at-once
// strategy (reconcileStatic), DYNAMIC the topic-based one (reconcileDynamic),
// DYNAMIC_TO_STATIC the route conversion (ReconcileConvert).
type MigrationType string

const (
	TypeStatic          MigrationType = "STATIC"
	TypeDynamic         MigrationType = "DYNAMIC"
	TypeDynamicToStatic MigrationType = "DYNAMIC_TO_STATIC"
)

// sectionKey is the manifest section that declares this type, for refusals that
// must name the declaration the operator actually wrote ("" for an unknown type).
func (t MigrationType) sectionKey() string {
	switch t {
	case TypeStatic:
		return "staticMigration"
	case TypeDynamic:
		return "dynamicMigration"
	case TypeDynamicToStatic:
		return "dynamicToStaticConversion"
	}
	return ""
}

// PlanMode is the strategy a Plan was reconciled under, and what
// migplan.Result.Mode carries. It is distinct from RouteMode on purpose:
// "convert" is a plan shape with no route-mode equivalent.
type PlanMode string

const (
	ModeStatic  PlanMode = "static"
	ModeDynamic PlanMode = "dynamic"
	ModeConvert PlanMode = "convert"
)

// RouteMode is the Gateway route's own mode (the CR's `mode` field, or the
// mode inferred from its streaming-domain binding).
type RouteMode string

const (
	RouteModeStatic  RouteMode = "static"
	RouteModeDynamic RouteMode = "dynamic"
)
