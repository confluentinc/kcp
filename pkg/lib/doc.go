// Package lib is the stable JSON-in / JSON-out façade over kcp's
// state-processing and plan-generation pipelines. External Go modules
// (cc-growth-service, etc.) import this package; it wraps the
// internal/* implementations so they stay private.
//
// EXPERIMENTAL: signatures and JSON shapes may change while
// `plan_schema_version` is `"1-experimental"`. Pin to a specific kcp
// version in your go.mod and bump deliberately. The function names and
// `(stateJSON, planInputs []byte) → ([]byte, error)` shape are
// expected to remain stable; the JSON payload schema is the only
// surface intended to evolve.
package lib
