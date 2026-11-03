package gateway

// The three roles a gateway CR plays in a migration. Used to name the offending
// file in findings, so an operator knows which flag to go and fix.
const (
	roleFenced     = "fenced"
	roleSwitchover = "switchover"
)

// The CR tree is untyped YAML, so these read one level with a type assertion
// instead of unstructured.Nested*: those deep-copy the subtree through
// runtime.DeepCopyJSONValue, which panics on the uint64 goccy/go-yaml produces
// for a positive integer like nodeIdRanges.start (it tolerates int64 only).

func mapField(m map[string]any, key string) (map[string]any, bool) {
	v, ok := m[key].(map[string]any)
	return v, ok
}

func sliceField(m map[string]any, key string) ([]any, bool) {
	v, ok := m[key].([]any)
	return v, ok
}

func stringField(m map[string]any, key string) (string, bool) {
	v, ok := m[key].(string)
	return v, ok
}
