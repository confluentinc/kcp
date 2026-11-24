---
paths:
  - "**/*.go"
  - "**/*.ts"
  - "**/*.tsx"
---

# Code Comments

**Principle:** comments, test names and comments, and docs state what the code does **now**. Never describe the migration away from an old design: no "there is no state file", "no registration/init step", "no longer", "used to", "previously", "renamed from X", "was removed", "any more", "back when init and execute were separate". Write "every run reconciles live from the manifest and the cluster", not "there is no state file: every run reconciles live". History belongs in commit messages and PR descriptions, not in the code.

- Default to writing **zero comments**. Code must be self-documenting.
- Aim for one short line, and only if the **WHY** is entirely non-obvious. Go beyond one line only when the WHY genuinely can't fit, and then keep it to a few lines. Never write multi-paragraph docstrings.
- **Forbidden:** Never write comments that explain **WHAT** the code does, reference past implementations, or leak your conversational chain of thought. If a fact behind a history comment still matters, state it as current behaviour.
- **Exception:** one short WHAT line is fine when the code isn't readable at first glance, i.e. a reader would have to stop and decode it. Examples: dense stdlib calls (`strings.Cut` with discarded results), copy and slice idioms, regexes, bit operations, format strings, special struct tags (`json:"-"`). An example value helps (`// Strip the port suffix (e.g. ":9096")`). A few words signposting a distinct block of setup or config also qualify (`// Internet gateway data source`). Plain control flow never qualifies (`// if empty, return`).

A good WHY: the measured basis of a magic number (`// Convergence measured ~4s on 2 replicas; generous but still fails fast`). Use plain, literal wording ("is read first", not "wins over").

A one-line description above a test is fine when it adds what the name doesn't, usually the stakes (`// Its TLS settings must not leak into the process-wide default client`).

In TypeScript, JSDoc is optional; when used, prefer the one-line `/** … */` form.

Rationale that needs more than one line goes in the commit message or PR description, not the code.

Also never:
- Narrate behaviour across scenarios or code paths.
- Describe anything other than the code directly below.
- Open a comment with the name of a test or unexported identifier. Exported functions, types and interface methods follow Go convention instead: a one-line doc starting with the name (`// Type returns the source type (msk or osk)`).
- Explain a whole test in a paragraph.
- Write "Verify X" above an assert that checks X.
- Repeat a contract in several places. State it once, where it is enforced.
- Draw full-width banners (`// -----`, `// ── x ─────`). A one-line `// --- section ---` header grouping what follows is fine.
- Cite plans, sections, requirements or tickets (`§13`, "Design §9", "task-7", `R3/R6`, `AE4`, `#6205997094`).

Reviewed good and bad examples: `.claude/skills/comment-review/examples.md`.

Enforced on changed Go lines by `.claude/bin/commentlint` at the end of each turn: 2–3 lines warn, 4+ lines fail, 100 characters per line, and no task, plan or review references. Directives (`//go:`, `//nolint`) are exempt.
