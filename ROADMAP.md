# Roadmap

Milestones we've agreed on but deliberately deferred, so they don't get lost.
Shipped work moves to the compact changelog at the bottom the moment it
ships — full design rationale, rejected alternatives, and bug postmortems
live in git history (and, where they're durable, in the code's own doc
comments), not here.

## TODO

Roughly in priority order.

### Tests audit

Added 2026-07-15, ahead of 0.0.1: a holistic pass over the test suite
itself — coverage gaps (recent features added many new tests at once;
worth checking nothing else grew without matching coverage), redundant or
superseded tests (any that quietly duplicate what a newer test already
covers more precisely), stale assertions, naming consistency across
`Test*` functions. Not a hunt for bugs in the tests' *subjects* — that's
what the tests are for — but in the tests' own upkeep. Scope not yet
detailed.

### Diagnostics: remaining open items

- **Edit delta scope needs an explicit test.** `EditReport.Delta`/
  `Resolved` are a set-difference over two full-workspace
  `AllDiagnostics()` snapshots, not filtered to `tx.changed` — deliberate,
  since a rename can break a caller the transaction never spliced, and
  that collateral damage has to surface in the same edit's delta.
  Exercised implicitly by every blast-radius test but never asserted as
  its own named guarantee.
- **Scoping optionality — parked 2026-07-13.** An uncapped view of *one*
  scope (not the capped view or the whole workspace) is a real gap, but a
  mutation's blast radius can span multiple scopes anyway, so a single
  scope parameter on `diagnostics()` wouldn't fully solve it. Batch has
  since shipped; worth revisiting whether it changed the round-trip
  pressure that made this matter, before designing further.

#### Implementation Alternative: Interactive Quick-Fix Synthesizer
To escape loops where agents hallucinate fixes for complex type errors (e.g., interface satisfaction mismatches), enrich the returned diagnostics with computer-generated quick-fix templates: when the compiler reports `*T does not implement I (missing method M)`, resolve the missing method's exact signature from the interface DTO and inject a precise snippet directly into the diagnostic's response metadata.

### Equivalence oracle (companion, after batch)

`assertModelEqualsDisk(t, e)` flushes to a temp copy, bootstraps a fresh
engine on it, and diffs the two worlds — symbols, sources, diagnostics.
Mutation tests call it last; a FuzzVerbs target feeds it random verb
sequences. One property subsumes the structural invariants: the in-memory
model must be indistinguishable from a cold bootstrap of its own flushed
state. Construction (the workspace package) prevents; the oracle catches
the rest.

**Sequencing note:** keep this ahead of Recheck v2 (below) in practice,
not just position on the page — incremental recompilation is exactly the
class of change that introduces cold/cache divergence bugs, and the
oracle is the regression detector that makes it safe to adopt.

### Smaller follow-ups

- **Describers could batch too — noted 2026-07-17, parked until it gets
  its own focused pass.** Creators/Editors/Deleters/Refactorings already
  take an array natively, one tool per verb; Describers
  (`describe_symbol`, `describe_file`, `describe_package`) are the one
  read-side category that never got the same treatment, so a multi-entry
  read still costs one round-trip per item. Scope not yet decided:
  `describe_symbol` alone (the one actually observed causing friction) or
  all three for full read/write consistency.

- **Recheck v2.** The post-edit reload is currently the whole workspace —
  correct by construction, sub-second at POC scale. When logged reload
  durations demand it, narrow to dirty packages plus transitive
  dependents via a stored import graph (or a Salsa/query-based
  memoization model — either is incremental recompilation, which sits in
  tension with this codebase's "derived state is rebuilt, never patched"
  invariant). Don't pursue either without the equivalence oracle first,
  and not before measuring whether batch already removed the pressure
  that made this look necessary.

- **Persistent structural sharing / content-addressable ASTs.** Today's
  transaction isolation is a full shallow clone (`e.ws.Clone()`) per
  transaction. If GC/allocation overhead ever becomes measurable at
  scale, structurally-shared immutable trees would make cloning an O(1)
  pointer copy. Not pursued preemptively — no measured pain yet.

- **Fine-grained modification.** Whole-declaration replacement is the
  dominant token cost of self-hosted work. `astutil.PathEnclosingInterval`
  is the likely primitive for statement-boundary detection if this gets
  picked up (composed with the existing `offsetSpan`, not hand-rolled) —
  but `astutil.Apply`'s mutate-then-reprint pattern doesn't fit here,
  since this codebase's ASTs locate byte spans for splicing and are never
  re-printed.

  #### Implementation Alternative: Structured Search and Replace (SSR)
  Match and rewrite by structure, not line position or raw text (prior
  art: JetBrains' SSR, Comby, ast-grep). `golang.org/x/tools/refactor/eg`
  already implements the core mechanism (before/after function-template
  pairs with wildcards, matched using full type information) but pkg.go.dev
  flags it as not in the latest version of its module — confirm it's
  still intact before leaning on it. Not explored in depth yet.

- **External test package (`_test`-suffixed) creation** is unsupported:
  `create_file` targets the production package only.
- **Flush is not atomic across files**: a mid-flush I/O error leaves a
  partial write on disk (in-memory state stays consistent; re-flush
  recovers).
- **Shared `FileSet` leaks entries on a rolled-back transaction** — a
  `Tx` that calls `SwapFile` at least once and then rolls back (a
  partial-success-then-failure batch) permanently leaks that parse's
  entry into `Workspace.Clone()`'s shared `token.FileSet`, which has no
  removal primitive. Bounded (any `Bootstrap`/`Reload`/successful `Edit`
  replaces the whole `FileSet`), never produces a wrong answer — just
  unused bookkeeping bytes. Investigated 2026-07-17: the natural fix
  (a scratch `FileSet` per `Tx`, merged in on commit) doesn't hold up,
  since untouched files' existing ASTs are already keyed to positions in
  the *original* `FileSet`, and `go/token.FileSet` has no supported
  clone/fork primitive that would let old entries carry over while still
  permitting independent rollback of new ones. Accepted as-is.

### BUGS

None open.

## DONE

Compact changelog, newest first — one line per shipped item. Full
rationale, rejected alternatives, and real bugs found along the way live
in git history, not here.

- **2026-07-17** — Workspace package audit: `MarkFlushed`/`MarkDirty` now
  replace-not-mutate (was contradicting `Clone`'s own documented
  immutability invariant), `pruneEmptyUnit`/`PruneFile`'s duplicated logic
  deduped, `Package.Doc()` pre-sized, `Workspace`'s concurrency contract
  documented explicitly; the `FileSet`-leak-on-rollback finding
  investigated and accepted (see Smaller follow-ups).
- **2026-07-17** — Engine package audit: fixed `Reload`'s TOCTOU race plus
  panic-unsafe lock, `Bootstrap`'s missing `defer`, narrowed
  `LoadExternal`'s lock span (retry-on-stale-`FileSet`, not a second
  mutex), threaded `ctx` through `Engine.Read`/`View`/scanners (57 call
  sites) — caught and fixed a real nil-`ctx` panic in `Engine.Edit` along
  the way, found only by running the tests.
- **2026-07-17** — AGENTS.md trimmed 225 → 131 lines: "Core invariants"
  and "Nomenclature grammars" mostly removed after verifying they were
  redundant with existing doc comments; two genuinely undocumented items
  moved into code instead of just deleted (`Engine`'s gate-safe-accessor
  deadlock warning, `Tx`'s Refactorings-verb test, `tools.go`'s
  tool-description writing rule).
- **2026-07-16/17** — internal/tools package audit: struct-scoped
  `toolConfig` replacing the `diagLimit` package global, every category's
  types co-located into their own handler file, output slices pre-sized.
- **2026-07-16** — Refactoring safety: `moveConflicts` + `qualifierFixups`
  make `move_symbol`/`move_file` provably safe across package boundaries
  (refuse when unsafe, auto-repoint qualifiers both directions when
  safe); three real, previously-undiscovered bugs found and fixed along
  the way.
- **2026-07-16** — Deletion semantics: new idempotent Deleters category
  (`delete_symbol`/`delete_file`/`delete_package` split out of Editors,
  noop-if-absent); partial-spec deletion (trim, or blank to `_`) for
  multi-name specs, closing a refusal that turned out to be a missing
  capability, not a real ambiguity.
- **2026-07-15/16** — Batch mutations collapsed into the norm: every
  Creator/Editor/Deleter takes an array natively, one tool per verb, no
  `_batch` suffix anywhere (27 tools → 25); a real deadlock (an
  `Engine`-level accessor called from inside a `Read`/`Edit` closure)
  found and fixed along the way, now documented as a standing rule.
- **2026-07-15** — Iota groups: full lifecycle design, all five phases
  shipped (rename, move, delete, edit, create) — parenthesized,
  position-dependent const/var groups are now fully addressable and
  mutable member-by-member without silently corrupting iota-derived
  values.
- **2026-07-14/15** — Refactor engine overhaul complete: `rename_symbol`
  folded into `move_symbol`; `rename_file`/`rename_package` renamed
  `move_file`/`move_package`; a conforming leading doc comment now
  travels with a rename; interface-method rename settled as
  `edit_symbol`'s job on purpose, not a `move_symbol` gap.
- **2026-07-13/14** — Tools-surface overhaul: `describe_package`/
  `describe_file`/`edit_file` (package-level docs) added;
  `describe_type`/`describe_function`/`describe_method` consolidated into
  one `describe_symbol`; "Symbol" vocabulary unified across every write
  tool; full-surface `Package`/`FileName`/`SymbolKey` field rename;
  category prefixes added to every tool description; optional fields
  unified on `*T` + `omitempty` everywhere.
- **2026-07-13** — Diagnostics speak addresses, not positions:
  `Diagnostic` gains `Package`/`Key`, drops `Line`/`Col`, at the DTO
  boundary only; `MutationOutput` renamed `WriteOutput`; `Unrelated`
  field added so an edit's echo can stay silent about pre-existing
  breakage without hiding that it exists.
- **Foundational, pre-2026-07-13** — the trusted core extracted into
  `internal/engine/workspace` (unexported fields, named primitives only);
  the anti-corruption layer at the engine gate (no more type-alias
  re-exports, real DTOs translated at the boundary); section-banner
  comments eliminated in favor of real per-category files; read-only
  dependency inspection by import path (`list_*`/`describe_*` resolve
  third-party and stdlib packages too); `reload` (reconnect-to-refresh);
  the two file-addressing styles unified; floating comments confirmed to
  silently vanish under unrelated rewrites (not just a theoretical risk).
