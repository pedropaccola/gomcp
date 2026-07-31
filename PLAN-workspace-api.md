# Workspace API Redesign — Working Document

Status: **settled for implementation (steps 1-8 below); domain-type-package split deferred.**
This is scaffolding for a real
redesign of `internal/workspace`'s public (mutation) surface, as consumed by `internal/store`.
No code should change until this document settles. Also feeds directly into the directive work
(`PLAN-directives.md`'s Phase 4 generated-file guard needs exactly the kind of single choke point
this redesign is meant to produce).

## Context

Working through where to enforce a cross-cutting write rule ("never mutate a generated file")
surfaced that gomcp's mutation surface isn't one coherent interface — it's three resource kinds
(Package, File, Symbol) that grew their CRUD verbs independently, at different times, under
different pressures, and never got reconciled. `Store` is meant to *query* and *orchestrate*;
`Workspace` is meant to *mutate* — that boundary mostly holds, but where `Workspace` didn't expose
a verb `Store` needed, `Store` reached for `Workspace`'s own internal machinery instead
(`Tombstone`, `RemoveUnit`, `InstallUnit`, raw constructors) and composed it directly. That's not
a style nitpick — it means any cross-cutting rule we want to enforce inside `Workspace` (the
generated-file check being the immediate motivator, but not the only future one) can be silently
bypassed by `Store` composing the raw pieces itself, exactly like `Tx.DeletePackage` and
`Tx.CreatePackage` already do today.

## The actual verb × resource matrix today

Only counting real `Workspace`-level verbs — not `Store`/`Tx` methods that fake a verb by
composing lower-level pieces themselves:

| Resource | Create | Edit (content) | Move/Relocate | Drop |
|---|---|---|---|---|
| **File** | `SwapFile` (upsert, policy-free) | `SwapFile` (same verb) | `MoveFile` (same-pkg), `RelocateFile` (cross-pkg) | `DropFile` |
| **Symbol** | ❌ none — `Tx.CreateSymbol` computes + calls `installFile`→`SwapFile` itself | ❌ none — `Tx.EditSymbol` same shape | `RelocateSymbols`/`RelocateDeclaration` | ❌ none — `Tx.DeleteSymbol` computes via `ComputeDeletionSplices`, applies, installs itself |
| **Package** | ❌ none — `Tx.CreatePackage` composes `NewPackage`+`NewUnit`+`InstallUnit` directly | N/A (a package has no content of its own, only its files do) | `MovePackage` | ❌ none — `Tx.DeletePackage` composes `Tombstone`×N + `RemoveUnit` directly |

**File is the only resource with a complete verb set.** Package only ever got Move. Symbol only
ever got Move (as `RelocateSymbols`). Every other cell is filled by `Store` reaching into
`Workspace`'s internals instead of calling a verb, because the verb was never built.

## Diagnosis: three distinct problems, not one

1. **Safety leak (Package Create/Drop).** `NewPackage`/`NewUnit`/`InstallUnit`/`Tombstone`/
   `RemoveUnit` are raw mutable-state primitives. `Store` calling them directly means any future
   cross-cutting rule enforced only inside a `Workspace` verb (e.g. the generated-file check) can
   be routed around, because the verb doesn't exist to route through.
2. **Consistency gap (Symbol Create/Edit/Delete).** Not a safety leak the same way — `SwapFile`
   is still the one real mutation call at the bottom of `Tx.CreateSymbol`/`EditSymbol`/
   `DeleteSymbol`. But Move got promoted to a real `Workspace` verb because it has a genuine
   cross-key correctness need (`RelocateSymbols` must validate the *whole* moving set before
   touching any member — checking one key at a time gives wrong answers once an earlier key has
   already moved). Create/Edit/Delete never got the equivalent verb, seemingly because nothing
   *forced* it the way Move's correctness requirement did — not because they don't deserve one.
3. **Vocabulary mismatch.** Even where verbs exist, they don't speak one language:
   `SwapFile`/`DropFile`/`MoveFile` (File) vs. `RelocateSymbols`/`ApplyFileSplices` (Symbol) vs.
   nothing yet (Package). Confirmed `ApplyFileSplices` isn't itself a leak — it groups splices by
   path and calls `SwapFile` per file internally, i.e. it's already the correct multi-file sibling
   of `SwapFile` — but its name doesn't say that.

## Plurality: two different phenomena wearing the same word

Symbols are the only resource that batches by default today (`RelocateSymbols` takes `keys
[]string`), and it's tempting to read that as "symbols got it right, files/packages should catch
up." That's half true. Two distinct things are both called "batching":

- **Independent batching** — N operations bundled for round-trip efficiency, with no semantic
  dependency between members. `SwapFile` unconditionally calls `RebuildIndex()`
  (`primitives.go:51`); today's `create_files`/`create_symbols` tool handlers already loop N
  singular `Tx` calls inside one transaction, paying N redundant `RebuildIndex` passes for what
  is, semantically, one batch. This is the *same* cost shape that made `DeletePackage` bypass
  `DropFile` in the first place — not yet fixed at the file/symbol-content level the way it was
  worked around (not fixed — worked around) at the package level.
- **Correctness-dependent batching** — the set's members interact, so validating or applying them
  one at a time gives a *different, wrong* answer than validating the whole set first.
  `RelocateSymbols` needs this: an already-relocated key must not appear "left behind" to a
  conflict check that hasn't seen the rest of the batch.

These need different treatment. Naively making `SwapFile`/`CreateSymbol` "just take a slice and
loop internally, one `RebuildIndex` at the end" is *not* automatically safe the moment two items in
the same batch target the same insertion point (e.g. two new consts both wanting "the end of this
group") — computing both insertion offsets against the *original* pre-batch state and applying both
splices in one pass needs an explicit tie-breaking rule (which one ends up first in the file?),
the same shape of problem `RelocateSymbols` already solved for Move (its own explicit policy: types
before their own methods, regardless of input order). Batching Create/Edit for real efficiency
gains means designing that ordering policy on purpose, not assuming a slice parameter is free.

**Not yet resolved — needs explicit design, not assumption:**
- What ordering rule applies when N batched creates/edits target the same file, same insertion
  region? (Candidate: caller-given order = file order, mirroring how sequential single-item calls
  already behave today — but this needs to be a stated policy, not an accident of implementation.)
- Does Move (File/Package granularity) have an equivalent latent hazard once batched — e.g. two
  moves in one batch swapping paths with each other (A→B and B→A in the same call)? Symbol's
  `MoveSymbolGroup` sidesteps this (one shared destination file, not pairwise swaps) — File/Package
  batched Move hasn't been designed at all yet.

## Design principles going into the redraw

1. Every verb that mutates a resource's content or existence lives in `Workspace`, callable in one
   round trip by `Store` — no verb should require `Store` to compose two or more `Workspace`
   mutation primitives to achieve one conceptual operation. (Reading/computing via `Workspace`'s
   pure query/compute helpers before calling the one mutation verb is fine — that's how
   `RelocateDeclaration` itself works internally.)
2. One consistent vocabulary across all three resources, applied only where the underlying
   semantics actually match — not forced for symmetry's sake. (Open question below: does "Swap"
   generalize to Package, or is Package's Create genuinely a different shape since it has no
   content to swap?)
3. Batching is a first-class question per verb, not a blanket "make everything plural." Each verb
   gets an explicit answer: independent-batch (safe to loop internally, one `RebuildIndex`,
   needs an ordering policy only if insertion points can collide), correctness-dependent-batch
   (needs whole-set validation up front, Move's shape), or single-only (no batching need
   identified).
4. Raw state-mutating primitives (`Tombstone`, `RemoveUnit`, `InstallUnit`, and anything of that
   shape discovered for Symbol) become unexported once every legitimate external caller is routed
   through a real verb — the compiler enforcing the boundary, not convention.

## Open questions to resolve before drawing the final interface table

1. **Does "Swap" belong on Package?** `SwapFile`'s defining trait is *policy-free upsert of
   content* — `Tx.CreateFile`/`EditFile` layer the must-not-exist/must-exist policy on top of one
   policy-free primitive. Package has no equivalent "content" — a package's create is "install an
   empty unit shell," not "replace bytes." Does forcing `SwapPackage` as a name make the interface
   more consistent, or does it paper over a real structural difference (Package never gets an
   "Edit" verb the way File/Symbol do, precisely because it has nothing to swap)?
2. **Should Symbol Create/Edit/Delete get pre-validated whole-batch verbs like Move, or is
   sequential apply-and-recompute (today's actual, correct behavior) the right permanent shape,
   just moved from `Store` into a real `Workspace` verb?**
3. **The insertion-order tie-break policy** for batched creates/edits sharing a target region —
   needs an explicit decision, not an implementation accident.
4. **Does Move (File/Package) need a batch form at all**, given `MoveSymbolGroup` already exists
   for the one case (type + its methods) where batching Move earns its keep?
5. Naming table itself — draft only, not settled:

| Resource | Create | Edit | Move | Drop |
|---|---|---|---|---|
| File | `SwapFile` | `SwapFile` | `MoveFile`/`RelocateFile` | `DropFile` |
| Symbol | ? (new verb, name TBD) | ? (same verb as Create, per File's pattern?) | `MoveSymbol(s)` (rename from `RelocateSymbols`?) | ? (new verb, name TBD) |
| Package | ? (`CreatePackage` vs `SwapPackage` — open question 1) | N/A | `MovePackage` (unchanged) | `DropPackage` (new verb) |

## Settled implementation order

Confirmed via a full audit (agent-driven, gomcp-tools-only, cross-checked against `search_source`/
`search_references` for every leak claim — see git history for the full report if needed). Three
leaks confirmed, all matching this document's earlier diagnosis, plus concrete resolutions:

1. **`Workspace.CreatePackage(pkg PackagePath, name string) (FilePath, error)`** — absorbs
   `NewPackage`+`NewUnit`+`InstallUnit`+stub-file `SwapFile`+existence/validity checks as one
   direct function body. No single-caller helper extraction — write it as one function, matching
   how `EnsurePackage`/`MovePackage` are already shaped.
2. **`Workspace.DropPackage(pkg PackagePath) []FilePath`** — per-file `Tombstone`+`RemoveUnit`,
   idempotent no-op if missing, one direct function body.
3. `Tx.CreatePackage`/`Tx.DeletePackage` shrink to call the above. Investigate whether they can be
   eliminated entirely rather than kept as pass-throughs — `Tx.changed`/`markChanged` bookkeeping
   is a per-transaction reporting concern `Workspace` has no notion of, so some bridge is likely
   unavoidable, but confirm rather than assume, and justify whichever shape survives.
4. Unexport `InstallUnit`, `Tombstone`, `RemoveUnit` once verified zero remaining callers outside
   `internal/workspace`. (`NewPackage`/`NewUnit` stay exported — `disk.Loader` is a legitimate,
   separate caller.)
5. Move `internal/store/fragments.go` (`classifyFragment`, `parseDeclFragment`,
   `parseSpecFragment`, `constVarEntries`, `importsPath`, `renderDocComment`, `symbolDoc`) into
   `internal/workspace` — its only real dependency is `workspace` itself.
6. Add `Workspace.CreateSymbol`/`EditSymbol`/`DeleteSymbol` as real verbs, absorbing the
   placement/grouping/collision logic currently living in `Tx`'s versions (`InsertOffset`,
   `MergeableGroupInsertOffset`, `TypeDeclOffset`, `ComputeEditPlan`, `DetectEditCollisions`,
   `ComputeDeletionSplices`). Same pass-through-elimination scrutiny as step 3.
7. `go build`/`go vet`/full test suite after each numbered step, not just at the end.
8. Naming cleanup, not deferred despite being independent of the leak-closing work:
   `EnsurePackage`→`EnsureXTest` (it only ever auto-creates the XTest half; the generic name
   overclaims). `SwapLoaded`→`Rebuild` ("Swap" stays reserved for the CRUD content-replacement
   tier — `SwapFile` — and shouldn't also mean "install a fresh post-recheck generation," a
   different operation entirely). Whether `Reset` also renames for pairing consistency with
   `Rebuild` is still open — decide during step 8 itself.

**Working principle for this pass**: no premature abstraction — when a verb absorbs what was
previously scattered `Store`-side composition, write it as one direct function body, not a clutch
of single-caller helpers. Pass-throughs are fine only where something makes them unavoidable
(per-transaction bookkeeping); default to elimination, justify what survives.

**Deferred, not abandoned**: splitting `Unit`/`Package`/`File`/`Symbol`/the `ByteOffset` family out
of `internal/workspace` into their own domain-type package. Real complication identified: Go's
per-package field privacy means single-type mutators (`setFile`/`unsetFile`-shaped primitives)
would have to travel with their types into the new package, not stay behind in `workspace` — the
split isn't "types here, all mutation there," it's "types + their own narrow primitives in one
package, cross-type orchestration in `workspace`." Sequenced after the verb redesign above, not
alongside it.

## Progress

Steps 1-7 implemented and verified (build/vet/test green throughout, incremental checkpoints, not
just at the end):
- `Workspace.CreatePackage`/`DropPackage` added; `Tx.CreatePackage`/`DeletePackage` shrank to
  essential thin pass-throughs (they can't be eliminated — `Tx.changed`/`markChanged` is a
  per-transaction reporting concern `Workspace` has no notion of; confirmed rather than assumed).
- `InstallUnit`, `Tombstone`, `RemoveUnit` audited individually via `search_source`: `Tombstone`/
  `RemoveUnit` had zero external callers, unexported cleanly. `InstallUnit` does not — it's a real
  cross-package dependency for `internal/testutil`'s fixture builders and some of `internal/store`'s
  own tests, which construct arbitrary workspace states (including ones no production verb reaches,
  like pre-seeded diagnostics) directly. Stays exported, documented as such ("Exported for tests
  only").
- `internal/store/fragments.go` moved into `internal/workspace` in full (`Fragment` type + 7
  functions). Two rounds of a real, reproducible tool limitation surfaced along the way: combining
  a rename and a cross-package move in one `refactor_move_symbol` call triggers a hard tool error
  ("type information unavailable"); doing them as two separate calls (rename, then move) works and
  surfaces ordinary diagnostics instead. Also: a moved type/function whose own source still
  qualifies identifiers with the package it just left behind (self-reference once it's the same
  package) isn't cleaned up automatically — needs a manual pass to strip the now-stale qualifier
  each time.
- `Workspace.CreateSymbol`/`EditSymbol`/`DeleteSymbol` added, absorbing the full placement/
  grouping/collision logic `Tx`'s versions used to own. `Tx`'s versions are now the same shape as
  `CreatePackage`/`DeletePackage` — thin, essential pass-throughs for `markChanged`.
- `errSymbolExists` (store-local) retired in favor of a new `workspace.SymbolExistsError`, matching
  the existing `NoSymbolError`/`PackageExistsError` vocabulary — used by both the new
  `Workspace.CreateSymbol` and the unchanged `Tx.renameSymbol`.
- Dead code removed as it was orphaned, not left behind: empty `store/fragments.go` deleted,
  `errSymbolExists` deleted once its last caller moved.

Step 8 (naming cleanup) done: `EnsurePackage`→`EnsureXTest`, `SwapLoaded`→`Rebuild`, both clean
renames with zero diagnostics, propagated to `store` automatically. `Reset` left unrenamed — it
already reads clearly for what it does (full replacement including `module`, the Bootstrap/Reload
"start completely fresh" case), and pairing it with `Rebuild` for symmetry didn't seem to add real
clarity over the cost of renaming a lower-risk, already-distinct method. Flagging this call rather
than treating it as silently settled — revisit if it still bothers you on reflection.

**Additional cleanup, found by inspection rather than planned**: `internal/store/pipeline.go`'s
`installFile`/`applyFileSplices` wrappers turned out to be fully dead weight once the `Workspace`
verbs each return their own touched paths. Checked every remaining caller (`CreateFile`, `EditFile`,
`renameSymbol`, `RepairMissingImports` — the only 4 left after steps 1-6's rewrites) and confirmed
each already has everything it needs to call the `Workspace` verb directly and `markChanged` inline,
with no wrapper adding value: `SwapFile`'s callers already hold the path they passed in (nothing to
return), and `ApplyFileSplices` already returns touched paths itself. Inlined all 4, deleted
`installFile`/`applyFileSplices`, moved the two survivors (`RepairMissingImports`, `markChanged`)
into `tx.go`, deleted the now-empty `pipeline.go`. Verified clean throughout.

**New finding, not yet acted on**: `Tx.RepairMissingImports` has the same shape as the leaks above
— entirely `Workspace`-state orchestration plus one final mutation call — but moving it fully
depends on `Store.View.AllDiagnostics`, a presentation-layer diagnostic aggregator (converts raw
`Package.Diags` into the tool-facing `Diagnostic` shape), not pure workspace state. Flagged as a
separate follow-up rather than folded into this pass — needs its own look at whether `Workspace`
should grow an equivalent raw-diagnostics walk, not a quick bolt-on.

## Non-goals for this redesign

- Not touching `Store`'s query-side surface (`ResolvePackage`, `ResolveSymbol`, etc.) — this is
  scoped to mutation verbs only.
- Not deciding yet how the generated-file check (from `PLAN-directives.md` Phase 4) plugs into
  whatever the final verb set looks like — that slots in once the verb set itself is settled,
  as the one thing every verb checks before mutating.
- Not implementing anything — this document is pre-implementation design only.
