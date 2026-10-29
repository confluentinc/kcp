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
