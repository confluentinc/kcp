---
name: pr-review
description: Use when reviewing PRs, doing self-review, or checking changes before sharing in this repo.
allowed-tools:
  - Read
  - Bash
  - Grep
  - Glob
  - Task
---

# PR Review

Reviews pull requests for the KCP repo. Two modes (self-review for authors, formal review for reviewers) anchored on KCP's specific concerns: state file impact, source abstraction parity (MSK ↔ OSK), the before-pushing checklist, test coverage, and the local pre-commit hook.

## Two modes

### Self-review (PR author)

Goal: catch issues before sharing with the team.

- Verify the before-pushing checklist (see below).
- Confirm test coverage matches the change shape.
- Check whether MSK changes need a parallel OSK change (or vice versa).
- Inspect any generated artifacts (state file diffs, Terraform output) for surprises.

### Formal review (reviewer)

Goal: understand the change, evaluate it, and give actionable feedback.

- Read the PR description against the actual diff — do they match?
- Check the same KCP-specific concerns below.
- Walk the test scenarios — do the tests assert the behavior the description claims?

## Process

### 1. Gather context

```bash
# Local changes (self-review)
git diff main --name-only
git diff main --stat
git diff main

# Remote PR
gh pr view <number> --json title,body,files,additions,deletions
gh pr diff <number>
gh pr view <number> --json reviews,comments
```

### 2. Categorize the changes

| Path | What to check |
|---|---|
| `cmd/<command>/` | Cobra wiring, flag definitions, `RunE` error handling, viper config |
| `internal/services/` | Business logic separation, interface boundaries, error propagation |
| `internal/sources/msk/`, `internal/sources/osk/` | Source abstraction parity (does the other source need the same change?) |
| `internal/client/` | HTTP/Kafka client abstractions, `httptest`-shaped tests |
| `internal/types/` | State file schema — any change is load-bearing for migration & UI |
| `internal/services/persistence/` | State load/save — backward compat with existing state files |
| `internal/services/hcl/` | Terraform generation, golden-file tests if present |
| `cmd/ui/frontend/` | React/TS, must rebuild before Go tests pass; visual verification expected |
| `cmd/ui/frontend/tests/e2e/` | Playwright specs, fixture-driven |
| `integration-tests/osk-scan/` | Docker compose env — see the `osk-integration-tests` skill |
| `integration-tests/schema-registry/` | Schema Registry compose env, separate from OSK |
| `docs/` | User-facing docs; mkdocs builds them |
| `Makefile`, `.github/workflows/`, `.semaphore/` | CI / build surface — confirm CI ran green |

### 3. KCP-specific critical checks

#### State file impact

KCP's workflow is state-driven (`kcp-state.json`). Any change touching `internal/types/` or `internal/services/persistence/` warrants extra care:

- [ ] Existing state files still load (no breaking schema changes without migration).
- [ ] New fields are additive and have sensible zero values.
- [ ] If a field is renamed or removed, search for consumers across `cmd/`, `internal/services/`, and `cmd/ui/frontend/src/`.
- [ ] If the UI consumes the field, the frontend types are updated too.

#### Source abstraction parity

KCP supports MSK and OSK via the `Source` interface. Changes to one source often need the other:

- [ ] If a change touches `internal/sources/msk/`, ask whether `internal/sources/osk/` needs the same.
- [ ] If a change adds a flag to a `cmd/scan/` subcommand, both `--source-type msk` and `--source-type osk` should respect it.
- [ ] Migration types 1–3 support both sources; Type 4 is MSK-only (IAM is AWS-specific). Confirm scope is correct.

#### Before-pushing compliance (from `CLAUDE.md`)

The repo's before-pushing rule requires:

- [ ] Test output shown in the PR description, not just "tests pass".
- [ ] Generated artifacts (state files, Terraform) included or linked when the change produces them.
- [ ] Visual verification suggested for UI changes (`kcp ui --state-file <fixture>`).
- [ ] Explicit user approval before commit/push (covered by the PR being open for review).

#### Test coverage

Match coverage to the change shape (see the `testing` skill for full conventions):

- [ ] New behavior in `cmd/`, `internal/services/`, `internal/sources/` has tests.
- [ ] Bug fixes include a regression test that fails without the fix.
- [ ] HTTP clients use `httptest.NewServer`; AWS-shaped services use stub structs with function fields.
- [ ] Frontend changes that affect user flows have a Playwright spec or fixture update.
- [ ] No new behavior arrives untested unless it's pure config or scaffolding (and the PR description says so).

#### Pre-commit hook + lint

KCP ships a pre-commit hook that runs `golangci-lint`:

- [ ] If the author hasn't installed it: `make pre-commit-install` is a one-liner and prevents most CI lint failures.
- [ ] If golangci-lint surfaces issues, fix them before pushing — don't disable rules without justification.

#### Emoji standard in log lines

KCP standardised log-line emojis in PR [#234](https://github.com/confluentinc/kcp/pull/234) — see the table in `CLAUDE.md` "Logging → Emoji standard". When reviewing Go changes:

- [ ] Any new emoji in a `slog.*` call is one of the six allowed (✅, ❌, ⚠️, 🔍, ⏭️, 🚀), placed at the start of the message string.
- [ ] No emojis appear in `fmt.Errorf(...)` return values — errors are data.
- [ ] No emojis in `fmt.Print*` calls used for interactive user prompts.
- [ ] One emoji per log line — flag any double-emoji or mid-string emoji.

### 4. Anchor on the PR template

The repo's `.github/pull_request_template.md` defines five sections; review them in order:

| Template section | What to check |
|---|---|
| Description | Does it actually describe what changed and why? |
| Changes Made | Are listed items reflected in the diff? Any unlisted changes that should be called out? |
| Testing | Are the test instructions runnable? Do "all tests pass" claims match CI status? |
| Checklist | Each box checked corresponds to verifiable evidence in the diff or PR thread. |
| Screenshots/Demo/Output | UI changes: screenshots present. CLI changes: command output included when behavior changed. |

### 5. Form the review

Lead with what's working. Then surface concrete issues with file/line references. Prefer `path/to/file.go:42` format so reviewers can jump directly. Distinguish:

- **Blockers**: must change before merge (correctness bug, broken test, schema break without migration).
- **Suggestions**: would improve the PR but the author can choose.
- **Nits**: style, wording — call out as "nit:" so the author can ignore.

For self-review, the same categorization works as a personal triage list: fix blockers, decide on suggestions, ignore nits.

## When the diff is large

If the diff is over ~500 lines or touches more than ~15 files, dispatch sub-agents (via `Task`) for focused per-area review rather than trying to hold the whole change in one pass. Good axes: backend changes, frontend changes, tests, and Terraform/HCL output. Re-aggregate findings at the end.
