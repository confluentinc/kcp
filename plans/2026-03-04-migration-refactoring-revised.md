# Migration Code Refactoring: Service-Oriented Architecture

**Date**: 2026-03-04
**Domain**: Backend/Services
**Type**: Refactoring
**Status**: Design Approved - Ready for Implementation

## Executive Summary

Refactor the monolithic `Migration` struct (~938 lines) to achieve clear separation of concerns by splitting it into three focused components: `MigrationConfig` (domain state), `MigrationOrchestrator` (FSM orchestration), and `MigrationWorkflowService` (business logic). This design prioritizes **clarity and understandability** while aligning with existing codebase patterns used in `ClusterDiscoverer` and `ClustersScanner`.

## Problem Statement

The current `Migration` struct violates Single Responsibility Principle by combining four distinct concerns:

1. **FSM State Machine Orchestration** - Callbacks, transitions, state management
2. **Domain State** - Cluster config, gateway config, topics, runtime data
3. **Service Dependencies** - `gatewayService`, `clusterLinkService` instantiated internally
4. **Business Logic** - All workflow methods (initialization, fencing, promotion, switching)

**Specific Issues Identified:**

- **Services instantiated inside Migration** (lines 293-294): Violates dependency injection
- **FSM callbacks tightly coupled to business logic**: Mixing orchestration with execution
- **State persistence mixed with orchestration**: Cannot test FSM without file I/O
- **No separation between "when" and "what"**: Orchestration and business logic tangled
- **Testing nightmare**: Cannot test components independently

## Design Principles

**Primary Goal**: Clarity - making the code easier to understand and reason about

**Key Separation Principles:**
1. **MigrationConfig** - Pure data, no behavior (like function parameters)
2. **MigrationOrchestrator** - FSM orchestration only, no business logic
3. **MigrationWorkflowService** - Business logic only, no FSM knowledge
4. **Services** - Infrastructure operations (K8s, Confluent API)

## Architecture Overview

```
┌─────────────────────────────────────────────────────────────┐
│ MigrationInitializer / MigrationExecutor (cmd layer)        │
│  - Loads/creates state via persistence                      │
│  - Creates service instances (gateway, clusterlink)         │
│  - Creates MigrationOrchestrator with injected services     │
│  - Calls orchestrator methods                               │
└─────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│ MigrationOrchestrator (orchestration layer)                 │
│  - Owns the FSM (looplab/fsm)                               │
│  - Holds MigrationConfig (domain state)                     │
│  - Has MigrationWorkflowService (injected interface)        │
│  - Has persistence.Service (injected interface)             │
│  - FSM callbacks delegate to workflow service               │
│  - Saves state after each transition                        │
└─────────────────────────────────────────────────────────────┘
                         │
                         ▼
┌─────────────────────────────────────────────────────────────┐
│ MigrationWorkflowService interface (business logic)         │
│  - Initialize(ctx, config, apiKey, apiSecret)               │
│  - CheckLags(ctx, config, threshold, maxWait, ...)          │
│  - FenceGateway(ctx, config)                                │
│  - PromoteTopics(ctx, config, apiKey, apiSecret)            │
│  - CheckPromotionCompletion(ctx, config)                    │
│  - SwitchGateway(ctx, config)                               │
│                                                              │
│ DefaultMigrationWorkflowService (implementation)            │
│  - gateway.Service (injected)                               │
│  - clusterlink.Service (injected)                           │
└─────────────────────────────────────────────────────────────┘
```

## Component Details

### 1. MigrationConfig (Pure Domain State)

**Location**: `internal/types/migration_config.go`

**Purpose**: Hold all domain configuration with no behavior

```go
type MigrationConfig struct {
    MigrationId  string
    CurrentState string

    // Gateway configuration
    GatewayNamespace     string
    GatewayCrdName       string
    SourceName           string
    DestinationName      string
    SourceRouteName      string
    DestinationRouteName string
    KubeConfigPath       string

    // Cluster link configuration
    ClusterId           string
    ClusterRestEndpoint string
    ClusterLinkName     string
    Topics              []string
    AuthMode            string

    // Migration runtime data
    ClusterLinkTopics   []string
    ClusterLinkConfigs  map[string]string
    GatewayOriginalYAML []byte

    CCBootstrapEndpoint  string
    LoadBalancerEndpoint string
}
```

**Key Characteristics:**
- No methods (except possibly JSON marshaling helpers)
- No FSM references
- No service dependencies
- Just data that gets passed around

### 2. MigrationWorkflowService (Business Logic)

**Location**: `internal/services/migration/workflow_service.go`

**Purpose**: Execute migration business logic using injected services

```go
type MigrationWorkflowService interface {
    Initialize(ctx context.Context, config *MigrationConfig, clusterApiKey, clusterApiSecret string) error
    CheckLags(ctx context.Context, config *MigrationConfig, threshold, maxWaitTime int64, clusterApiKey, clusterApiSecret string) error
    FenceGateway(ctx context.Context, config *MigrationConfig) error
    PromoteTopics(ctx context.Context, config *MigrationConfig, clusterApiKey, clusterApiSecret string) error
    CheckPromotionCompletion(ctx context.Context, config *MigrationConfig) error
    SwitchGateway(ctx context.Context, config *MigrationConfig) error
}

type DefaultMigrationWorkflowService struct {
    gatewayService     gateway.Service
    clusterLinkService clusterlink.Service
}

func NewDefaultMigrationWorkflowService(
    gatewayService gateway.Service,
    clusterLinkService clusterlink.Service,
) *DefaultMigrationWorkflowService {
    return &DefaultMigrationWorkflowService{
        gatewayService:     gatewayService,
        clusterLinkService: clusterLinkService,
    }
}
```

**Key Characteristics:**
- All business logic from current `migration.go` lines 374-937
- Uses injected services (no direct instantiation)
- No FSM knowledge
- Methods can modify config fields (pointer parameter)
- Pure business logic - no orchestration concerns

### 3. MigrationOrchestrator (FSM Orchestration)

**Location**: `internal/services/migration/orchestrator.go`

**Purpose**: Manage FSM lifecycle and coordinate workflow execution

```go
type MigrationOrchestrator struct {
    config             *MigrationConfig
    fsm                *fsm.FSM
    workflowService    MigrationWorkflowService
    persistenceService persistence.Service
    stateFilePath      string
}

func NewMigrationOrchestrator(
    config *MigrationConfig,
    workflowService MigrationWorkflowService,
    persistenceService persistence.Service,
    stateFilePath string,
) *MigrationOrchestrator {
    o := &MigrationOrchestrator{
        config:             config,
        workflowService:    workflowService,
        persistenceService: persistenceService,
        stateFilePath:      stateFilePath,
    }
    o.initializeFSM()
    return o
}

func (o *MigrationOrchestrator) Initialize(ctx context.Context, clusterApiKey, clusterApiSecret string) error {
    return o.fsm.Event(ctx, EventInitialize, clusterApiKey, clusterApiSecret)
}

func (o *MigrationOrchestrator) Execute(ctx context.Context, threshold, maxWaitTime int64, clusterApiKey, clusterApiSecret string) error {
    // Execute remaining steps based on current state
    steps := []struct {
        event       string
        description string
        args        []any
    }{
        {EventWaitForLags, "checking lags", []any{threshold, maxWaitTime, clusterApiKey, clusterApiSecret}},
        {EventFence, "fencing gateway", []any{}},
        {EventPromote, "promoting topics", []any{clusterApiKey, clusterApiSecret}},
        {EventWaitForPromotionCompletion, "waiting for promotion completion", []any{}},
        {EventSwitch, "switching gateway config", []any{}},
    }

    for _, step := range steps {
        if !o.canTransition(step.event) {
            continue
        }

        if err := o.fsm.Event(ctx, step.event, step.args...); err != nil {
            return fmt.Errorf("failed during %s: %w", step.description, err)
        }
    }

    return nil
}
```

**FSM Callback Pattern:**
```go
func (o *MigrationOrchestrator) leaveUninitializedCallback(ctx context.Context, e *fsm.Event) {
    slog.Info("FSM: LEAVING STATE", "state", StateUninitialized)

    // Extract args
    var clusterApiKey, clusterApiSecret string
    if len(e.Args) > 0 {
        if key, ok := e.Args[0].(string); ok {
            clusterApiKey = key
        }
    }
    if len(e.Args) > 1 {
        if secret, ok := e.Args[1].(string); ok {
            clusterApiSecret = secret
        }
    }

    // DELEGATE TO WORKFLOW SERVICE - key separation
    if err := o.workflowService.Initialize(ctx, o.config, clusterApiKey, clusterApiSecret); err != nil {
        e.Cancel(err)
        return
    }
}
```

**Key Characteristics:**
- Owns FSM lifecycle
- Callbacks extract args and delegate to workflow service
- No business logic - only arg extraction and delegation
- Handles state persistence after transitions
- Uses persistence service for state file operations

### 4. Persistence Service

**Location**: `internal/services/persistence/migration_persistence.go`

**Purpose**: Handle migration state file operations

```go
type Service interface {
    LoadMigrationState(filePath string) (*types.MigrationState, error)
    SaveMigrationState(filePath string, state *types.MigrationState) error
}

type FileSystemService struct{}

func (s *FileSystemService) LoadMigrationState(filePath string) (*types.MigrationState, error) {
    return types.NewMigrationStateFromFile(filePath)
}

func (s *FileSystemService) SaveMigrationState(filePath string, state *types.MigrationState) error {
    return state.WriteToFile(filePath)
}
```

## Command Layer Updates

### MigrationInitializer

**Before:**
```go
migration := types.NewMigration(migrationId, migrationOpts)
migration.SetSaveStateFunc(func() error { ... })
migration.Initialize(context.Background())
```

**After:**
```go
// Create config
config := types.NewMigrationConfig(migrationId, migrationOpts)

// Create services
gatewayService := gateway.NewK8sService(opts.kubeConfigPath)
clusterLinkService := clusterlink.NewConfluentCloudService(http.DefaultClient)

// Create workflow service
workflowService := migration.NewDefaultMigrationWorkflowService(
    gatewayService,
    clusterLinkService,
)

// Create persistence service
persistenceService := persistence.NewFileSystemService()

// Create orchestrator
orchestrator := migration.NewMigrationOrchestrator(
    config,
    workflowService,
    persistenceService,
    stateFilePath,
)

// Execute initialization
orchestrator.Initialize(ctx, clusterApiKey, clusterApiSecret)
```

### MigrationExecutor

**Before:**
```go
migration, err := types.LoadMigration(migrationState, migrationId)
migration.SetSaveStateFunc(func() error { ... })
migration.Execute(ctx, threshold, maxWaitTime, clusterApiKey, clusterApiSecret)
```

**After:**
```go
// Load config from state
config, err := migrationState.GetMigrationConfigById(migrationId)

// Create services (same as init)
gatewayService := gateway.NewK8sService(config.KubeConfigPath)
clusterLinkService := clusterlink.NewConfluentCloudService(http.DefaultClient)

// Create workflow service
workflowService := migration.NewDefaultMigrationWorkflowService(
    gatewayService,
    clusterLinkService,
)

// Create persistence service
persistenceService := persistence.NewFileSystemService()

// Create orchestrator
orchestrator := migration.NewMigrationOrchestrator(
    config,
    workflowService,
    persistenceService,
    stateFilePath,
)

// Execute migration
orchestrator.Execute(ctx, threshold, maxWaitTime, clusterApiKey, clusterApiSecret)
```

## State File Structure Changes

**Breaking Change**: State file format will change to store `MigrationConfig` instead of full `Migration`

**Before** (`types.MigrationState`):
```go
type MigrationState struct {
    Migrations   []Migration  `json:"migrations"`  // Full Migration struct
    KcpBuildInfo KcpBuildInfo `json:"kcp_build_info"`
    Timestamp    time.Time    `json:"timestamp"`
}
```

**After**:
```go
type MigrationState struct {
    Migrations   []MigrationConfig `json:"migrations"`  // Just config
    KcpBuildInfo KcpBuildInfo      `json:"kcp_build_info"`
    Timestamp    time.Time         `json:"timestamp"`
}
```

**Migration Strategy**: Accept breaking change. Any in-progress migrations must be restarted.

## File Structure (After Refactoring)

```
internal/types/
  ├── migration_config.go          (domain state only, ~100 lines)
  ├── migration_state.go            (updated to store MigrationConfig)
  ├── migration_constants.go        (FSM states and events)
  └── gateway_resource.go           (existing, unchanged)

internal/services/migration/
  ├── workflow_service.go           (interface + default impl, ~500 lines)
  ├── workflow_service_test.go      (unit tests with mocked services)
  ├── orchestrator.go               (FSM orchestrator, ~300 lines)
  └── orchestrator_test.go          (unit tests with mocked workflow)

internal/services/persistence/
  └── migration_persistence.go      (state file operations, ~50 lines)

cmd/migration/
  ├── init/
  │   ├── cmd_migration_init.go     (cobra command, unchanged)
  │   └── migration_initializer.go  (refactored to use orchestrator, ~100 lines)
  └── execute/
      ├── cmd_migration_execute.go  (cobra command, unchanged)
      └── migration_executor.go     (refactored to use orchestrator, ~100 lines)
```

## Implementation Plan Summary

1. **Phase 1**: Create new types (MigrationConfig, constants)
2. **Phase 2**: Create workflow service (interface + implementation)
3. **Phase 3**: Create orchestrator (FSM management)
4. **Phase 4**: Create persistence service
5. **Phase 5**: Update command layer (init, execute)
6. **Phase 6**: Update state file structure
7. **Phase 7**: Remove old Migration struct
8. **Phase 8**: Testing

## Success Criteria

1. **Clear Separation**: No business logic in orchestrator; no FSM in workflow service
2. **Dependency Injection**: All services injected via constructors
3. **Testability**: Can unit test workflow methods with mocked services
4. **Readability**: Each file has single, obvious purpose
5. **Line Count**: No single file exceeds ~500 lines
6. **Pattern Consistency**: Matches `ClusterDiscoverer` and `ClustersScanner` patterns

## Risks & Mitigations

- **Risk**: Breaking existing migrations in progress
  **Mitigation**: Accepted - no production migrations running, can start fresh

- **Risk**: Complex refactoring could introduce bugs
  **Mitigation**: Comprehensive testing, careful migration of existing logic

- **Risk**: FSM library quirks
  **Mitigation**: FSM isolated in orchestrator, easier to replace if needed

## References

- Existing pattern: `cmd/discover/cluster_discoverer.go` (service injection)
- Existing pattern: `cmd/scan/clusters/clusters_scanner.go` (opts struct, Run method)
- Existing services: `internal/services/gateway/gateway.go` (interface-based)
- Existing services: `internal/services/clusterlink/clusterlink.go` (interface-based)
