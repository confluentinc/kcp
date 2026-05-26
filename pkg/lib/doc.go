// Package lib is the stable bytes-in / bytes-out façade over kcp's
// state-processing and plan-generation pipelines. External Go modules
// (cc-growth-service, etc.) import this package; it wraps the
// internal/* implementations so they stay private.
//
// Formats: state and plan_inputs go in as raw bytes — JSON or YAML
// (JSON is a YAML 1.2 subset, so goccy/go-yaml accepts either). Plan
// output is JSON + Markdown; resolved plan_inputs come back as YAML
// so callers preserve the plan-inputs.yaml format their users edit.
//
// EXPERIMENTAL: signatures and payload shapes may change while
// `plan_schema_version` is `"1-experimental"`. Pin to a specific kcp
// version in your go.mod and bump deliberately. The function names and
// `(stateJSON, planInputs []byte) → ([]byte, error)` shape are
// expected to remain stable; the JSON / YAML payload schema is the
// surface intended to evolve.
package lib
