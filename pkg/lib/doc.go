// Package lib is the stable bytes-in / bytes-out façade over kcp's
// state-processing and plan-generation pipelines. External Go modules
// (cc-growth-service, etc.) import this package; it wraps the
// internal/* implementations so they stay private.
//
// Formats:
//   - state bytes must be JSON (kcp-state.json — what `kcp scan` writes).
//   - plan_inputs bytes must be YAML (plan-inputs.yaml shape).
//   - Plan output is JSON + Markdown. Resolved plan_inputs are echoed
//     back as YAML so callers preserve the plan-inputs.yaml shape their
//     users edit.
//
// API surface:
//
//	func ScanSummary(stateJSON []byte) ([]byte, error)
//	func GeneratePlan(stateJSON, planInputsYAML []byte) (*PlanResult, error)
//
//	type PlanResult struct { JSON, Markdown, PlanInputs []byte }
//
// EXPERIMENTAL: signatures and payload shapes may change while
// `plan_schema_version` is `"1-experimental"`. Pin to a specific kcp
// version in your go.mod and bump deliberately. Function names and
// argument shapes are expected to remain stable; the JSON / YAML
// payload schema is the surface intended to evolve.
package lib
