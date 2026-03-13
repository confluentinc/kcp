# MSK Testing Strategy Design

**Date:** 2026-03-13
**Branch:** `msk-testing-improvements` (off `main`)
**Status:** Approved — ready for implementation planning

---

## Problem Statement

KCP has a recurring class of bug: AWS API responses return null/optional fields that the code assumes are always populated, causing panics. The current pattern is reactive — a panic is reported on a live customer call, a nil guard is added, and the cycle repeats. The most recent example was a SASL/SCRAM bootstrap broker nil dereference in `create-asset migration-infra`.

Two things are needed together:
1. **Defensive coding** — guard against nil at every AWS API boundary, systematically
2. **Tests** — prevent regressions and catch the next case before it reaches a customer

---

## Confirmed Nil-Dereference Hotspots

Hotspots are identified by function name, not line number — line numbers drift as soon as the first fix is applied.

### `internal/services/metrics/metric_service.go` — `ProcessProvisionedCluster()`

| Location | Risk | Pattern |
|----------|------|---------|
| `BrokerNodeGroupInfo.BrokerAZDistribution` | `BrokerNodeGroupInfo` could be nil — same nil guards two accesses: `BrokerAZDistribution` and `InstanceType` | A |
| `CurrentBrokerSoftwareInfo.KafkaVersion` | `CurrentBrokerSoftwareInfo` could be nil | A |
| `*cluster.Provisioned.NumberOfBrokerNodes` | pointer deref without nil check | A |
| `BrokerNodeGroupInfo.StorageInfo.EbsStorageInfo.VolumeSize` | 4-level nil chain — express brokers and some configurations have no EBS | A |

Note: A single nil guard on `BrokerNodeGroupInfo` protects both the `BrokerAZDistribution` and `InstanceType` accesses. These are not independent fixes.

### `cmd/discover/cluster_discoverer.go`

| Location | Risk | Pattern |
|----------|------|---------|
| `discoverAWSClientInformation` — `*cluster.ClusterInfo` after `DescribeClusterV2` | `ClusterInfo` is not guaranteed non-nil on a successful AWS SDK response | B |
| `discoverMetrics` — `*cluster.ClusterInfo` after second `DescribeClusterV2` call | Same risk; also note this is a duplicate API call — see structural note below | B |
| `scanNetworkingInfo` — `cluster.ClusterInfo.Provisioned.BrokerNodeGroupInfo.ClientSubnets` | Multi-level chain | B |
| `getVpcIdFromSubnets` — `subnetIds[0]` | Slice index without length check | B |

**Important — Pattern B at `discoverAWSClientInformation` must be an early return:**
```go
if cluster.ClusterInfo == nil {
    return nil, nil, fmt.Errorf("DescribeClusterV2 returned nil ClusterInfo for %s", clusterArn)
}
```
This protects all downstream dereferences within `discoverAWSClientInformation` (including the call into `scanNetworkingInfo`). A log-and-continue approach here would not prevent the downstream panics.

**Structural note — duplicate `DescribeClusterV2` call:** `discoverMetrics` issues its own `DescribeClusterV2` call independently of the one in `discoverAWSClientInformation`. The defensive fix should add a nil guard, but this duplication is worth noting as a follow-up refactoring opportunity (pass the already-fetched cluster into `discoverMetrics` to avoid a redundant API call). Out of scope for this branch but should be captured as a TODO comment.

---

## Solution: Three Parallel Streams

### Stream 1: Defensive Coding Sweep

**Two fix patterns:**

**Pattern A — Safe default** (metric/metadata fields where partial data is acceptable):
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

**Pattern B — Early error return** (structural fields where nil makes the entire operation meaningless — must be an early return, not log-and-continue):
```go
if cluster.ClusterInfo == nil {
    return nil, nil, fmt.Errorf("DescribeClusterV2 returned nil ClusterInfo for %s", clusterArn)
}
```

**Going forward — static analysis:**
Add `nilaway` (Uber's nil safety linter) as a `make lint` step in the Makefile, running as part of the existing lint job in Semaphore CI (not a standalone pipeline). Initial run will produce noise from existing code — suppress known false positives with `//nolint:nilaway` comments at call sites that are already guarded, rather than disabling the tool globally. Treat the first PR as a baseline suppression pass, then enforce clean nilaway on all new code going forward.

---

### Stream 2: Mock-based Unit Tests

**Architecture: hand-rolled function-field stubs — no mock library.**

The codebase has well-defined service interfaces perfect for stub injection. `ClusterDiscovererMSKService` has 10 methods; all must be implemented on the stub or it will not compile. The full method list:

1. `DescribeClusterV2`
2. `GetBootstrapBrokers`
3. `ListClientVpcConnections`
4. `ListClusterOperationsV2`
5. `ListNodes`
6. `ListScramSecrets`
7. `GetClusterPolicy`
8. `GetCompatibleKafkaVersions`
9. `IsFetchFromFollowerEnabled`
10. `GetTopicsWithConfigs`

**Stub pattern:**
```go
// testhelpers_test.go in cmd/discover/
type stubMSKService struct {
    describeClusterV2Fn        func(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error)
    getBootstrapBrokersFn      func(ctx context.Context, clusterArn string) (*kafka.GetBootstrapBrokersOutput, error)
    listClientVpcConnectionsFn func(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClientVpcConnection, error)
    listClusterOperationsV2Fn  func(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.ClusterOperationV2Summary, error)
    listNodesFn                func(ctx context.Context, clusterArn string, maxResults int32) ([]kafkatypes.NodeInfo, error)
    listScramSecretsFn         func(ctx context.Context, clusterArn string, maxResults int32) ([]string, error)
    getClusterPolicyFn         func(ctx context.Context, clusterArn string) (*kafka.GetClusterPolicyOutput, error)
    getCompatibleKafkaVersionsFn func(ctx context.Context, clusterArn string) (*kafka.GetCompatibleKafkaVersionsOutput, error)
    isFetchFromFollowerEnabledFn func(ctx context.Context, cluster kafkatypes.Cluster) (bool, error)
    getTopicsWithConfigsFn     func(ctx context.Context, clusterArn string) ([]types.TopicDetails, error)
}

// Each method delegates to the function field if set, otherwise returns a safe empty default:
func (s *stubMSKService) DescribeClusterV2(ctx context.Context, clusterArn string) (*kafka.DescribeClusterV2Output, error) {
    if s.describeClusterV2Fn != nil {
        return s.describeClusterV2Fn(ctx, clusterArn)
    }
    return &kafka.DescribeClusterV2Output{}, nil
}
// ... same pattern for all 10 methods
```

Each test only wires the function fields it cares about. Everything else returns a safe empty default.

**File locations:**
```
cmd/discover/
  cluster_discoverer_test.go     ← new: ClusterDiscoverer tests
  region_discoverer_test.go      ← new: RegionDiscoverer tests
  testhelpers_test.go            ← new: stub implementations shared across this package

cmd/discover/testdata/
  provisioned_cluster.json       ← real API response captured by soak tests (see Stream 3)
  serverless_cluster.json        ← real API response captured by soak tests (see Stream 3)

internal/services/metrics/
  metric_service_test.go         ← extend existing: add ProcessProvisionedCluster tests
```

**Test cases — `ClusterDiscoverer.Discover()`:**

| Scenario | What it proves |
|----------|---------------|
| Happy path — fully populated provisioned cluster (seeded from `testdata/provisioned_cluster.json`) | Baseline works end-to-end |
| `ClusterInfo` is nil from `DescribeClusterV2` | Returns error, does not panic |
| `Provisioned.BrokerNodeGroupInfo` is nil | Early return, no downstream panics |
| `Provisioned.NumberOfBrokerNodes` is nil | Defaults to 0, no panic |
| Empty subnet list from EC2 | Returns error, no panic on `subnetIds[0]` |
| Serverless cluster | Correct code path, no provisioned-only fields accessed |
| `SkipMetrics: true` | Metrics path skipped, empty `ClusterMetrics` returned |

**Test cases — `ProcessProvisionedCluster()`:**

| Scenario | What it proves |
|----------|---------------|
| `BrokerNodeGroupInfo` is nil | Single nil guard protects both `BrokerAZDistribution` and `InstanceType`, no panic |
| `CurrentBrokerSoftwareInfo` is nil | Kafka version defaults to empty string, no panic |
| `NumberOfBrokerNodes` is nil | Defaults to 0, no panic |
| `StorageInfo.EbsStorageInfo.VolumeSize` nil chain | No panic — express brokers and clusters without EBS |
| Express broker (`instance_type` prefix `"express."`) | Takes express-broker code path, skips storage queries |

**Test cases — `RegionDiscoverer.Discover()`:**

Note: `RegionDiscoverer` does not contain the same nil-dereference risk as `ClusterDiscoverer` — it stack-allocates `DiscoveredRegion` and does not dereference AWS pointer fields directly. These tests are regression/coverage tests, not nil-panic prevention.

| Scenario | What it proves |
|----------|---------------|
| Happy path — region with clusters | Full flow completes, cluster ARNs returned |
| Empty cluster list | Returns valid state with no clusters, no panic |
| `skipCosts: true` | Cost service never called |
| Cost API returns error | Error propagated from `Discover()` |

All of these run in CI on every PR. No AWS credentials required.

---

### Stream 3: MSK Soak Test Harness

**Purpose:** Validate the full `kcp discover` flow against a real MSK cluster on a schedule. Catches issues that mocks cannot — actual AWS API response shapes, rate limiting, partial data from specific cluster configurations.

**File locations:**
```
test/integration/msk/
  soak_test.go          ← test suite
  fixtures/             ← real API response snapshots (copied to cmd/discover/testdata/ — see below)
```

**Fixture handoff between soak tests and unit tests:**
The soak test's `TestCaptureFixtures` writes JSON files to `test/integration/msk/fixtures/`. A `make` target copies them into `cmd/discover/testdata/` for use as happy-path seeds in Stream 2 unit tests:
```makefile
update-test-fixtures:
    cp test/integration/msk/fixtures/provisioned_cluster.json cmd/discover/testdata/
    cp test/integration/msk/fixtures/serverless_cluster.json cmd/discover/testdata/
```
Run this manually after a successful fixture capture run. The `testdata/` files are committed to the repo; `fixtures/` files are regenerated on demand.

**Build tag — critical for CI safety:**
```go
//go:build integration
```
Physically excluded from `go test ./...`. Only runs when `-tags integration` is explicitly passed.

**Environment variables:**
```bash
KCP_TEST_MSK_CLUSTER_ARN=arn:aws:kafka:us-east-1:123456789012:cluster/test-cluster/...
KCP_TEST_MSK_REGION=us-east-1
# AWS credentials via standard chain (AWS_PROFILE, AWS_ACCESS_KEY_ID, etc.)
```

**Skip behaviour:**
```go
func TestMain(m *testing.M) {
    if os.Getenv("KCP_TEST_MSK_CLUSTER_ARN") == "" {
        fmt.Println("KCP_TEST_MSK_CLUSTER_ARN not set — skipping MSK soak tests")
        os.Exit(0)
    }
    os.Exit(m.Run())
}
```

**Make target:**
```makefile
test-msk-soak:
    @echo "Running MSK soak tests (cluster: $(KCP_TEST_MSK_CLUSTER_ARN))..."
    go test ./test/integration/msk/... -v -timeout 30m -tags integration
```

**What the tests validate — three levels:**

1. **No panics** — runs `Discoverer.Run()` and asserts it returns without error. `Discoverer` and `DiscovererOpts` are defined in `cmd/discover/discoverer.go`:
```go
func TestDiscoverDoesNotPanic(t *testing.T) {
    stateFile := filepath.Join(t.TempDir(), "kcp-state.json")
    discoverer := discover.NewDiscoverer(discover.DiscovererOpts{
        Regions:     []string{os.Getenv("KCP_TEST_MSK_REGION")},
        SkipCosts:   false,
        SkipMetrics: false,
        SkipTopics:  true, // topics require Kafka network access; skip in soak
        State:       types.NewState(),
    })
    err := discoverer.Run()
    assert.NoError(t, err)
}
```

2. **Output is well-formed** — shares the temp state file from step 1 (tests run sequentially in a `TestSuite` wrapper or via shared package-level state set up in `TestMain`):
```go
func TestDiscoverOutputIsWellFormed(t *testing.T) {
    require.NotEmpty(t, sharedState.MSKSources.Regions)
    cluster := sharedState.MSKSources.Regions[0].Clusters[0]
    assert.NotEmpty(t, cluster.Name)
    assert.NotEmpty(t, cluster.Arn)
    assert.Greater(t, cluster.ClusterMetrics.MetricMetadata.NumberOfBrokerNodes, 0)
    assert.NotEmpty(t, cluster.AWSClientInformation.BootstrapBrokers)
}
```
`sharedState` is populated by `TestMain` after running discover once, so the API is called only once per soak run.

3. **Fixture capture** — opt-in, run separately when refreshing unit test seed data:
```go
func TestCaptureFixtures(t *testing.T) {
    if os.Getenv("KCP_CAPTURE_FIXTURES") == "" {
        t.Skip("set KCP_CAPTURE_FIXTURES=1 to regenerate fixtures")
    }
    // Writes sharedState clusters[0] API response to test/integration/msk/fixtures/provisioned_cluster.json
    // Run `make update-test-fixtures` after to copy into cmd/discover/testdata/
}
```

**Scheduling:** A separate Semaphore pipeline (not the PR pipeline), triggered nightly. MSK cluster credentials stored as Semaphore secrets (`KCP_TEST_MSK_CLUSTER_ARN`, `KCP_TEST_MSK_REGION`, plus AWS credentials).

---

## Current Test Coverage Baseline (on `main`)

| Area | Coverage | Notes |
|------|----------|-------|
| Auth option selection | Good | Table-driven, thorough |
| Metric query building | Good | Tests query structure, CLI generation |
| Cost query building / URL encoding | Good | Helper function coverage |
| Kafka admin client | Good | Unit tests exist |
| `ClusterDiscoverer.Discover()` | **None** | No tests at all |
| `discoverAWSClientInformation()` | **None** | No tests |
| `discoverMetrics()` | **None** | No tests |
| `ProcessProvisionedCluster` with nil fields | **None** | Query building tested only |
| MSK API null/partial response handling | **None** | Never tested |
| `RegionDiscoverer.Discover()` | **None** | No tests |

---

## What This Does Not Include

- Tests for `discoverTopics` (Kafka Admin API surface — separate concern)
- Tests for connector discovery (lower nil-panic risk, no pointer-heavy field access)
- Full contract testing framework (overkill — fixtures + soak tests cover the same ground more simply)
- Mock library (`mockery`, `gomock`) — hand-rolled stubs are sufficient for stable interfaces this size
- Refactoring the duplicate `DescribeClusterV2` call in `discoverMetrics` — out of scope, captured as a TODO comment in the code
