---
name: comment-review
description: Review and trim Go code comments against .claude/rules/code-comments.md, either on the current branch before a PR (no arguments) or on existing files or packages for cleanup (paths as arguments). Use before opening a PR, when asked to prune or review comments, or when commentlint reports findings.
argument-hint: "[path ...]  (omit to review the current branch against origin/main)"
---

# Comment review

The standard is `.claude/rules/code-comments.md`. Read it first; it overrides anything here. Then read `examples.md` next to this file: reviewed verdicts your calls must match.

## 1. Scope

- **No arguments (branch mode)**: the comments this branch adds. Run `.claude/bin/commentlint` and read `git diff origin/main...HEAD` plus the uncommitted diff.
- **Paths (cleanup mode)**: every comment in those files. Run `.claude/bin/commentlint -all $ARGUMENTS`. Work one package at a time.

Record the starting comment-line count for the files in scope.

## 2. Judge every in-scope comment, not only the flagged ones

The linter catches size and keywords. It can't tell a WHAT one-liner over readable code from one that decodes dense code (the rule's exception). For each comment, decide one of:

- **Delete** (the default): it says what readable code does, restates the name, only narrates a past implementation, cites process artefacts, or repeats a fact stated elsewhere.
- **Cut to one line**: only when the WHY is entirely non-obvious. Keep that one fact and nothing else.
- **Rewrite in present tense**: when a history comment ("no longer", "used to") hides a fact that still matters.
- **Rename instead**: if a comment is only there because a name is unclear, suggest the better name in the report. Don't rename in this pass.

Always keep `//go:` and `//nolint` directives and `// Deprecated:` notices. If a deleted comment carried a real fact that one line can't hold, list it in the report so it can go in the PR description.

Comments must stay accurate. Check each one you rewrite against the code.

## 3. Edit comments only

Change no code. In cleanup mode, confirm this mechanically:

```
.claude/bin/commentlint -verify-code -base HEAD <paths>
```

It must print `OK`. Run it only if the files had no uncommitted changes before you started; otherwise say it was skipped. Then run `gofmt -l <paths>` and `.claude/bin/commentlint -all <paths>`.

## 4. Report

- Comment lines before → after, per file.
- Anything deleted that carried information worth keeping elsewhere.
- Suggested renames.
- Standard gaps: cases where the rule gave the wrong answer or no answer. These are how the standard gets tuned.

Do not commit.
