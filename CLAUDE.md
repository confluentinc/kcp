# KCP Project Rules

## Logging

- `fmt.Printf` — user-facing console output (with emoji + color via `github.com/fatih/color`)
- `slog.Debug` — verbose internal detail (kcp.log only)
- `slog.Info` — operational detail (kcp.log only, console with `--verbose`)
- `slog.Warn` — warnings (always console + kcp.log)
- `slog.Error` — errors (always console + kcp.log)
- Errors bubble up via `return fmt.Errorf(...)` — cobra handles display

## Conventions

- Exported functions above unexported in each file
- Command flags use `pflag.NewFlagSet` groups added via `cmd.Flags().AddFlagSet()`

## Build & Test

- Build: `go build ./...`
- Test: `go test ./...`
- Fast parallel tests: `go test -p 1 -parallel 4 -count=1 ./...`
- Terraform validation tests use `t.Parallel()` for faster execution
- Skip Terraform validation locally: `SKIP_TERRAFORM_VALIDATION=true go test ./...`

## Git

- Push: `git push-external` (not `git push`)
