---
name: testing
description: Use when writing or running Go tests, adding Playwright tests, or applying TDD in this repo.
allowed-tools:
  - Read
  - Bash
  - Grep
  - Glob
---

# Testing

Conventions for Go unit tests, Playwright e2e tests, and when to apply TDD in KCP.

## TDD scope

KCP applies TDD selectively, not universally.

| Change type | Posture |
|---|---|
| New behavior in `cmd/`, `internal/services/`, `internal/sources/` | Test-first. Write the failing test, then implement. |
| Bug fixes anywhere | Regression-first. Reproduce the bug as a failing test before fixing. |
| One-off scripts, prototypes, exploratory spikes | Tests-after acceptable. |
| Doc-only changes, pure styling, frontend-visual tweaks | No tests required. |
| Modifying packages whose existing tests are clearly test-after | Don't refactor for TDD. Add new tests for new behavior; leave the existing structure alone. |

When unsure, default to test-first. The cost of writing the test first is small; the cost of catching a missed edge case after merging is larger.

## Frontend prerequisite

**The frontend MUST be built before running any Go tests.** Tests embed `cmd/ui/frontend/dist/` via Go's `embed` directive in `cmd/ui/frontend/frontend.go`. Without it, every test fails with `pattern all:dist: no matching files found`.

```bash
make build-frontend   # one-time per session, or after frontend changes
make test-go
```

## Canonical commands

```bash
make fmt                 # format Go + frontend
make test-go             # all Go unit tests (depends on build-frontend)
make test-go-coverage    # with coverage report
make test-playwright     # frontend e2e (depends on full build)
go test ./<pkg> -v       # single-package tests
go test ./<pkg> -run TestName -v   # single test by name
```

For integration tests (`make test-osk-scan`, `make test-schema-registry`, `make test-migration`), use the `osk-integration-tests` skill.

## Go test conventions

### Table-driven tests with subtests

The dominant idiom. Use for any test with more than one input case.

```go
func TestParseFoo(t *testing.T) {
    tests := []struct {
        name    string
        input   string
        want    Foo
        wantErr bool
    }{
        {name: "valid input", input: "ok", want: Foo{Value: "ok"}},
        {name: "empty input rejected", input: "", wantErr: true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got, err := ParseFoo(tt.input)
            if tt.wantErr {
                require.Error(t, err)
                return
            }
            require.NoError(t, err)
            assert.Equal(t, tt.want, got)
        })
    }
}
```

Exemplar: `internal/types/state_test.go`.

### Assertions: testify `require` and `assert`

`stretchr/testify` is the preferred assertion library (~2/3 of test files use it). Use it for new tests.

- `require.X(t, ...)` — fatal precondition. If this fails, the test cannot continue (e.g., setup, parsing).
- `assert.X(t, ...)` — non-fatal check. The test continues so multiple assertions can fail and report together.

```go
require.NoError(t, err)              // can't continue if err
result, err := DoThing()
require.NoError(t, err)              // ditto
assert.Equal(t, "expected", result)  // checking the value
assert.Len(t, items, 3)              // independent check
```

Older test files use plain `t.Errorf` / `t.Fatalf` — when modifying them, follow the existing style; don't churn to testify for its own sake.

### Stub structs with function-field overrides

Preferred over generated mocks. Define a stub struct with `Fn` fields the test sets per-case.

```go
type stubMSKService struct {
    describeClusterV2Fn func(ctx context.Context, arn string) (*kafka.DescribeClusterV2Output, error)
    listClustersFn      func(ctx context.Context) ([]kafkatypes.Cluster, error)
}

func (s *stubMSKService) DescribeClusterV2(ctx context.Context, arn string) (*kafka.DescribeClusterV2Output, error) {
    if s.describeClusterV2Fn == nil {
        return nil, errors.New("describeClusterV2Fn not set")
    }
    return s.describeClusterV2Fn(ctx, arn)
}

// Test:
msk := &stubMSKService{}
msk.describeClusterV2Fn = func(_ context.Context, _ string) (*kafka.DescribeClusterV2Output, error) {
    return &kafka.DescribeClusterV2Output{ClusterInfo: nil}, nil
}
```

Exemplar: `cmd/discover/cluster_discoverer_test.go`. Shared stub builders go in a `testhelpers_test.go` file in the same package.

### HTTP clients: `httptest.NewServer`

For testing code that makes HTTP calls, spin up a real server in-process.

```go
server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
    assert.Equal(t, "/expected/path", r.URL.Path)
    w.Header().Set("Content-Type", "application/json")
    _ = json.NewEncoder(w).Encode(response)
}))
defer server.Close()

client := NewJolokiaClient(server.URL)
```

Exemplar: `internal/client/jolokia_client_test.go`.

## Playwright e2e tests

Specs live under `cmd/ui/frontend/tests/e2e/`. Fixtures (pre-built state files) live under `cmd/ui/frontend/tests/e2e/fixtures/`. The Playwright config starts `kcp ui --state-file <fixture>` to pre-load test data.

```bash
make test-playwright                                          # run all
cd cmd/ui/frontend && npx playwright test                     # equivalent
cd cmd/ui/frontend && npx playwright test --ui                # interactive UI mode
cd cmd/ui/frontend && npx playwright test --headed            # visible browser
cd cmd/ui/frontend && npx playwright test -g "name" --debug   # single test, paused
```

When adding a new e2e test, create or reuse a fixture state file rather than building state in the test. The fixture-driven approach keeps the test focused on UI behavior, not state setup.
