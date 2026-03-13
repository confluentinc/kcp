# KCP Testing Strategy Design

**Status:** WIP — brainstorming in progress. Section 1 (defensive coding) approved. Sections 2 (unit tests) and 3 (soak tests) still to be designed.

**Date:** 2026-03-13
**Context:** Panics observed on live customer calls due to nil AWS API response fields. Need systematic fix, not just reactive hotfixes.

---

## Problem Statement

KCP has a class of bug where AWS API responses return null/optional fields that the code assumes are always populated. This causes panics. The current pattern is reactive — a panic is reported, a nil guard is added, and the cycle repeats. The most recent example was the SASL/SCRAM bootstrap broker panic fix (commit 9e7c45e).

The fix for this requires two things working together:
1. **Defensive coding** — guard against nil at every AWS API boundary
2. **Tests** — prevent regressions and catch the next case before it hits a customer

---

## Approach: Three Parallel Streams

### Stream 1: Defensive Coding Sweep ✅ APPROVED

**Principle:** AWS API responses treat many fields as optional. Rather than panicking, degrade gracefully — log a warning, substitute a safe default, and continue. A partial result is better than a crash on a customer call.

**Two fix patterns:**

**Pattern A — Safe defaults** (metric metadata fields where partial data is acceptable):
```go
// Before (panics if nil)
numberOfBrokerNodes := int(*cluster.Provisioned.NumberOfBrokerNodes)

// After
numberOfBrokerNodes := 0
if cluster.Provisioned.NumberOfBrokerNodes != nil {
    numberOfBrokerNodes = int(*cluster.Provisioned.NumberOfBrokerNodes)
} else {
    slog.Warn("NumberOfBrokerNodes is nil, defaulting to 0", "cluster", clusterName)
}
```

**Pattern B — Early return with error** (structural fields where nil makes the operation meaningless):
```go
if cluster.ClusterInfo.Provisioned == nil || cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo == nil {
    return types.ClusterNetworking{}, fmt.Errorf("cluster %s has no broker node group info", clusterArn)
}
```

**Confirmed hotspots to fix:**

`internal/services/metrics/metric_service.go` — `ProcessProvisionedCluster()`:
- Line 34: `cluster.Provisioned.BrokerNodeGroupInfo.BrokerAZDistribution` — `BrokerNodeGroupInfo` could be nil → **Pattern A**
- Line 35: `cluster.Provisioned.CurrentBrokerSoftwareInfo.KafkaVersion` — `CurrentBrokerSoftwareInfo` could be nil → **Pattern A**
- Line 37: `*cluster.Provisioned.NumberOfBrokerNodes` — pointer deref without nil check → **Pattern A**
- Line 93: `*cluster.Provisioned.BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize` — 4-level nil chain → **Pattern A** (storage info may not exist for all cluster types)

`cmd/discover/cluster_discoverer.go`:
- Line 99: `*cluster.ClusterInfo` — nil check missing after `DescribeClusterV2` → **Pattern B** (fatal — can't discover without cluster info)
- Line 284: `cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo.ClientSubnets` — multi-level chain in `scanNetworkingInfo` → **Pattern B**
- Line 309: `subnetIds[0]` — slice index without length check → **Pattern B**
- Line 373: `*cluster.ClusterInfo` — same nil risk in `discoverMetrics` → **Pattern B**

**Going forward — static analysis:**
Add `nilaway` (Uber's nil safety linter) to the CI pipeline to catch new nil-dereference patterns at PR time. This closes the loop so the sweep isn't a one-off.

---

### Stream 2: Mock-based Unit Tests — TO BE DESIGNED

**High-level intent:**
- Use the existing service interfaces (`ClusterDiscovererMSKService`, `ClusterDiscovererMetricService`, `RegionDiscovererMSKService`, etc.) which are already perfectly structured for mock injection
- Write hand-rolled stub implementations (function-field pattern, no mock library) in `_test.go` files
- Write table-driven tests for `ClusterDiscoverer.Discover()` and `ProcessProvisionedCluster()` covering nil/sparse API response shapes
- Tests run in CI on every PR — fast, no AWS credentials needed

**Specific test coverage targets (not yet fully designed):**
- `ClusterDiscoverer.Discover()` — happy path, all-nil response fields, partial provisioned data, serverless cluster
- `ProcessProvisionedCluster()` — nil `BrokerNodeGroupInfo`, nil `VolumeSize`, express broker type
- `RegionDiscoverer.Discover()` — empty cluster list, cost skip, configurations pagination

---

### Stream 3: Soak Test Harness — TO BE DESIGNED

**High-level intent:**
- Scheduled test (not in CI) that runs against a real MSK test cluster
- Similar pattern to OSK Docker integration tests (`make test-all-envs`, `make test-env-up-*`)
- Target: `make test-msk-soak` — skips automatically if AWS credentials/cluster ARN env vars are not set
- Validates the full `kcp discover` and `kcp scan clusters` flows end-to-end with no panics
- Run on a schedule (e.g. nightly or weekly), not on every PR

---

## Current Test Coverage Baseline

| Area | Coverage | Notes |
|------|----------|-------|
| Auth option selection | Good | Table-driven, thorough |
| Metric query building | Good | Tests query structure, CLI generation |
| Cost query building / URL encoding | Good | Helper function coverage |
| OSK source, kafka client, credentials | Good | Unit tests exist |
| `ClusterDiscoverer.Discover()` | **None** | No tests |
| `discoverAWSClientInformation()` | **None** | No tests |
| `discoverMetrics()` | **None** | No tests |
| `ProcessProvisionedCluster` with nil fields | **None** | Query building tested only |
| MSK API null/partial response handling | **None** | Never tested |

---

## How to Resume This Session

This design doc was created mid-brainstorm. To resume:

1. Start a new Claude session in a worktree for isolation
2. Reference this file: `docs/plans/2026-03-13-testing-strategy-design.md`
3. Tell Claude: "Continue the brainstorming in this file — Section 1 is approved, we need to design Sections 2 and 3 in detail, then write the implementation plan"

Sections still to design:
- [ ] Stream 2: Mock/stub architecture, test file locations, specific test cases
- [ ] Stream 3: Soak test harness structure, Makefile targets, env var conventions
- [ ] Final: Write implementation plan (invoke `superpowers:writing-plans` skill)
