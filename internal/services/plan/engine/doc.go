// Package engine is the decision core behind `kcp report plan`: it turns a
// source cluster's migration-relevant facts into the target recommendations
// (size band, cluster type, networking, auth, switchover, and the human-assist
// handoffs).
//
// Contract: every exported decision takes a plain Profile value (never kcp
// state, never global state) and returns a plain verdict value. No I/O, no
// mutation of the input — Profile in, verdicts out. That purity is what lets the
// outer plan package feed the engine from scanned state, plan-inputs.yaml, and
// defaults interchangeably, and what makes the decision logic exhaustively
// testable in isolation.
//
// Sizing is capacity-based: every band edge is a published Confluent capacity
// ceiling (read off the tier tables and the eCKU caps), never a price. Cost does
// not reach the engine in any form.
package engine
