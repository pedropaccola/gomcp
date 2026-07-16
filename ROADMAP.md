# Roadmap

Milestones we've agreed on but deliberately deferred, so they don't get lost.

## TODO

Roughly in priority order. The tools-surface work (describe_package
through Tools doc overhaul) is done as of 2026-07-15 (see DONE) — it was
sequenced ahead of batch mutations, and batch itself is unchanged and
ready whenever its turn comes. Workspace package audit and Tests audit
(pre-0.0.1 polish, added 2026-07-15) come next, ahead of the
diagnostics/batch chain.

### Workspace package audit

Added 2026-07-15, ahead of 0.0.1: a dedicated review pass over
`internal/engine/workspace` — the trusted core, unexported hot fields,
mutable only through its named primitives. Doesn't need to wait for
batch mutations the way the engine-package audit does: batch reshapes
`Tx`'s verb categories in `internal/engine`, not workspace's own model or
primitives, so this is independent and can land whenever it's convenient.
Motivation stated plainly: close out the pre-release work on a good note,
not just a growing feature list — confirm the trusted core is as clean as
the invariants it's supposed to guarantee. Scope not yet detailed.

### Tests audit

Added 2026-07-15, ahead of 0.0.1, same motivation as the workspace audit
above. A holistic pass over the test suite itself — coverage gaps (the
iota-groups feature alone added ~15 new tests this session; worth
checking nothing else grew without matching coverage), redundant or
superseded tests (e.g. any that quietly duplicate what a newer test
already covers more precisely), stale assertions, naming consistency
across `Test*` functions. Not a hunt for bugs in the tests' *subjects* —
that's what the tests are for — but in the tests' own upkeep. Scope not
yet detailed.

### Diagnostics: remaining items (small, blocks batch)

"Speak addresses, not positions" and the `MutationOutput`→`WriteOutput`
rename both shipped 2026-07-13 (see DONE for the full implementation
record and friction notes). What's left:

**Still needs an explicit test:**
- **Edit delta scope.** `EditReport.Delta`/`Resolved` are a set-difference
  over two full-workspace `AllDiagnostics()` snapshots — one taken before
  the Tx runs, one after the post-edit recheck — not filtered to
  `tx.changed`. Deliberate: a rename can break a caller the transaction
  never spliced, and that collateral damage has to surface in the same
  edit's delta. (`Engine.Edit`, snapshot.go; `diffDiagnostics`,
  diagnostics.go.) Exercised implicitly by every blast-radius test
  (`TestDeleteSymbolBlastRadius`, `TestRenameMethodReportsBrokenSatisfaction`)
  but never asserted as its own named guarantee — do that before batch
  multiplexes this logic across statements.

**Resolved 2026-07-15, see DONE (batch mutations, per-verb):** per-statement
attribution — aggregate delta only, not per-statement. A batch's echo is
the same before/after full-workspace diagnostics snapshot any single edit
already produces, just taken once around N statements instead of one.
*Precondition* failures (a statement that can't even be attempted) are
per-statement by construction — the error names which entry failed — but
post-recheck *diagnostics* stay aggregate, matching today's semantics
scaled up rather than inventing new ones.

**Presentation:**
- **Scoping optionality — parked 2026-07-13.** The real gap (an uncapped
  view of *one* scope, not the capped view or the whole workspace) is
  real, but a mutation's blast radius can span multiple scopes anyway, so
  a single scope parameter on `diagnostics()` wouldn't fully solve it.
  Waiting on batch — it may reduce the round-trip pressure that makes this
  matter, or reshape the problem entirely. Not designing further until
  then.

#### Implementation Alternative: Interactive Quick-Fix Synthesizer
To escape loops where agents hallucinate fixes for complex type errors (e.g., interface satisfaction mismatches), enrich the returned diagnostics with computer-generated quick-fix templates:
- **Mechanics:** When the compiler reports `*T does not implement I (missing method M)`, the server can resolve the missing method's exact signature from the interface DTO and inject a precise snippet (`func (t *T) M(...) ...`) directly into the diagnostic's response metadata.
- **Result:** The agent gets compiler-correct code signatures on the first pass, preventing infinite correction cycles.

### Mutation-output & tool interface — resolved

Open as of 2026-07-12, resolved 2026-07-15 by actually shipping batch
(per-verb) rather than by redesigning ahead of it: `WriteOutput`'s
existing shape (`Files`/`IntroducedDiagnostics`/`ResolvedDiagnostics`/
`UnrelatedDiagnosticsCount`/`DiagnosticsUnavailable`) needed zero changes
to serve `create_symbol_batch`/`edit_symbol_batch` — `runEdit`'s existing
translation from `EditReport` to `WriteOutput` is already verb-count
agnostic, since it was never counting verbs to begin with, just
snapshotting diagnostics before and after one `Tx`. The open question
("does this shape survive multiplexing N statements") answered itself
once N statements turned out to still be one `Tx`, one recheck, same as
one statement always was.

### Batch mutations — per-verb, in progress

Promoted by evidence: the state-extraction sessions burned quota on
sequential edits whose echoes shrank one diagnostic at a time (1st edit:
99 diagnostics, 2nd: 98, ...) — every intermediate recheck and its echo
paid for, when only the final state mattered.

**Design superseded 2026-07-15**, before any of the original design below
was built: Pedro's call was a dedicated tool per verb (`create_symbol_
batch`, `edit_symbol_batch`, ...), each taking an array of that verb's
existing single-statement input, rather than one unified `batch` tool
accepting a heterogeneous list across Creators/Editors. This is a smaller,
simpler feature than what's described below, not a scoped-down version of
it — it needed none of the typed-descriptor architecture, since a
single-verb batch only ever has one verb's own precondition/collision
logic to reason about. The typed-descriptor idea isn't dead, just
unnecessary for what shipped — worth reconsidering only if/when batching
*across* heterogeneous verbs in one call becomes an actual need, not
before.

**Shipped 2026-07-15 (see DONE): `create_symbol_batch`, `edit_symbol_batch`.**
Both compose directly on the existing single-statement `Tx.CreateSymbol`/
`Tx.EditSymbol` — one `Tx`, sequential calls in submission order, first
failure discards the whole batch (`Error ⇒ untouched`, the same invariant
every other verb already has, just applied to N statements). One
real bug caught only by running the tests, not by `diagnostics()`: a
deadlock, now documented in AGENTS.md's Core invariants — see that DONE
entry for the full account.

**Design settled for the shipped two, decided directly by Pedro rather
than re-derived from the original agreed design below:**
- Resolve in submission order; the first entry that fails aborts the
  whole batch (not "collect every failure, then apply any" — simpler,
  and the error names which entry failed).
- Duplicate targets are refused outright, not deduplicated. For
  `edit_symbol_batch` specifically (the key is given explicitly): a cheap
  upfront pre-scan refuses before the `Tx` even opens. `create_symbol_
  batch` doesn't need equivalent pre-scan code — the target key isn't
  knowable without parsing `src`, which isn't exported from `internal/
  engine` to the tools layer, but `Tx.CreateSymbol`'s own existing
  collision check catches a duplicate naturally on the second attempt
  and aborts the batch the same way any other failure does.
- The echo is one final delta for the whole batch, not per-statement —
  see "Diagnostics: remaining items" and "Mutation-output & tool
  interface" above, both resolved by this decision rather than ahead of
  it.

**Not yet done, same pattern, lower priority:** `delete_symbol_batch`,
and batch variants for the other Creators/Editors
(`create_file`/`create_package`/`delete_file`/`delete_package`/
`edit_file`). `move_symbol_batch` (and any other Refactoring batch)
stays explicitly out of scope — Refactorings' processing scope (multi-site,
semantic scans) still can't be known upfront the way a single verb's own
preconditions can, the same reasoning the original design already had for
excluding them.

**Architecture-design evaluation (2026-07-12), still relevant regardless
of unified-vs-per-verb:** of the "Implementation Alternative" sketches
added alongside this roadmap, two touch batch's design space and both
turn out orthogonal to it as specified:
- Persistent structural sharing / content-addressable ASTs — `Workspace.
  Clone()` is already shallow (`maps.Clone` at the package/unit level,
  sharing `File`/`Symbol` pointers until touched), not the deep-copy
  operation the alternative assumes. Batch's real win over today's
  one-clone-per-verb pattern is clone *frequency* (N → 1), which falls out
  of batching regardless of Clone's internals. Reconsider the rewrite only
  if clone cost is still measurable after batch lands — don't do it
  preemptively for batch's sake.
- Salsa/query-based compilation (recheck v2's alternative, below) — batch
  already amortizes recheck to one call per Tx no matter which recheck
  implementation runs underneath; the two are independent. Landing recheck
  v2 first would erode part of batch's original quota-cost justification
  (N full rechecks was the measured pain, not per-recheck latency) without
  removing batch's other win — one round-trip with every precondition
  failure at once. No reason to sequence either ahead of the other.

### Deletion semantics: new Deleters category, noop-if-absent — shipped 2026-07-16

Raised while scoping `delete_symbol_batch` (above): deleting an
already-gone target inside a batch — e.g. an iota group's second member,
after the first member's delete already collapsed the whole group —
shouldn't abort the batch over a technicality the agent already achieved.
Generalized past batch once posed: deletion should be idempotent
everywhere, not just inside a batch.

**New Tx category, split out of Editors: Deleters** — `delete_symbol`,
`delete_file`, `delete_package`. Editors keep "fail if the address doesn't
exist" (a typo mid-edit should surface loudly); Deleters get "noop if the
address doesn't exist" — the target being gone *is* the success
condition, whoever caused it, the mirror image of Creators' "fail if
exists, can't destroy code." Precedent: HTTP DELETE's specified
idempotency (RFC 7231), `rm -f`, `kubectl delete --ignore-not-found`,
`DROP TABLE IF EXISTS`. Accepted trade: a genuine typo on a first-ever
delete now fails silently (noop) instead of loudly — judged acceptable
specifically because every address in this model is read from a prior
`list_*`/`describe_*` call, never guessed blind, so "target's gone"
overwhelmingly means "already handled," not "never existed."

Scope boundary — noop replaces only the *existence* check, not every
refusal:
- `DeleteFile`/`DeletePackage`: every current failure mode already *is*
  an existence check, so the reclassification is total.
- `DeleteSymbol` had a second, unrelated refusal: a multi-name
  `*ast.ValueSpec` (`var a, b int`) couldn't be deleted by whole-span
  removal without taking uninvolved names down with it. That refusal is
  gone now that the capability below ships — deletion is well-defined
  for it too, so there's no remaining exception: Deleters are noop-if-
  absent, full stop.

**New capability, previously missing, not actually ambiguous:**
partial-spec deletion. The "declared together with other names" refusal
was never a real ambiguity, just a missing one — `DeleteSymbol` only knew
whole-span removal, never surgery within a spec. Fully deterministic once
split on shape:
- `len(Values) == len(Names)` (parallel, `var a, b = 1, 2`) or
  `len(Values) == 0` (typed only, `var a, b int`): trim the targeted
  name (and its paired value, if any); siblings keep their own values
  untouched.
- `len(Values) < len(Names)` (one shared multi-valued expression,
  `var a, b = f()`): the call's arity is fixed by `f()`'s signature, so
  the targeted name blanks to `_` instead of being removed — the only
  transform leaving every other name's behavior byte-for-byte unaffected.
  Not a side effect, the minimal-diff answer to "remove `a`, disturb
  nothing else" — unlike position-dependent iota deletion, this case has
  exactly one right answer, so no refusal is warranted.
  - Convergence: once every real (non-blank) name in the spec has been
    deleted this way, the whole statement collapses, call included —
    matching solo deletion's existing behavior (nothing binds the call's
    result anymore, no reason to keep it). Iteratively deleting every
    name in a shared spec must reach the same end state as deleting a
    solo one directly.
- Noted in passing, not a design driver: shared multi-value assignment
  (`a, b := f()`) is far more common as a *local* variable inside a
  function body than as a package-level `var` — and locals aren't
  addressable symbols in this model at all. Still correct to build
  (nothing stops `var a, b = ParseTwoThings()` at package scope), just
  expect it exercised rarely.

**Files touched:** `internal/engine/editors.go` and `internal/tools/
editors.go` each split — `DeleteSymbol`/`DeleteFile`/`DeletePackage` and
their tool handlers moved to a new `deleters.go` in both packages, matching
the existing one-file-per-category convention. Two new small helpers in
`internal/engine/deleters.go`: `trimRange[T ast.Node]` (the comma-list
byte-range arithmetic, shared between the Names and Values trim) and
`Tx.trimSpecName` (the case split itself). `AGENTS.md`'s Tx category list
and `README.md`'s Write section both updated: deletion gets its own
Deleters line instead of folding under Editors. `WriteOutput` is
unchanged — files-touched/diagnostics-delta already doubles as the
noop-vs-real signal (empty ⇒ noop) without a new field. `testdata/sandbox/
shapes/groups.go` gained `boundsOf()`/`boundX, boundY` as the shared
multi-value-call fixture (`var minX, maxX = -10.0, 10.0` already covered
the parallel-trim case).

**Unblocked:** `delete_symbol_batch` — the iota-group aliasing problem
(deleting one member consumes a sibling's address) now resolves for free,
since a second lookup on an already-gone target is a noop instead of an
abort; no alias pre-scan needed. Still not built — see "Batch mutations"
above.

### Engine package audit

Added 2026-07-14, originally gated on batch mutations landing first —
that gate is gone: batch shipped per-verb (2026-07-15), composing on
`Tx.CreateSymbol`/`Tx.EditSymbol` unchanged, with zero engine-level
changes at all (no typed descriptors, nothing reshaped). The premise for
waiting no longer holds, so this audit is unblocked and can happen
whenever it's convenient — no longer tied to any other item's completion.
Scope not yet detailed.

### Equivalence oracle (companion, after batch)

`assertModelEqualsDisk(t, e)` flushes to a temp copy, bootstraps a fresh
engine on it, and diffs the two worlds — symbols, sources, diagnostics.
Mutation tests call it last; a FuzzVerbs target feeds it random verb
sequences. One property subsumes the structural invariants: the in-memory
model must be indistinguishable from a cold bootstrap of its own flushed
state. Construction (the workspace package) prevents; the oracle catches
the rest.

**Sequencing note (2026-07-12):** keep this ahead of recheck v2 in
practice, not just position on the page. Recheck v2's own alternatives
(hand-maintained import graph or Salsa memoization, below) are both forms
of incremental recompilation — exactly the class of change that introduces
cold/cache divergence bugs. The oracle is the regression detector that
makes either one safe to adopt; build it before, not alongside, whichever
recheck v2 approach is ever chosen.

### Refactor engine overhaul — complete

Verb consolidation shipped 2026-07-13 (see DONE): `rename_symbol` is gone,
folded into `move_symbol`; `rename_file`/`rename_package` renamed to
`move_file`/`move_package` (one Move verb per entity now — Consistency).
That also resolved this section's third known gap (`rename_symbol` vs.
`edit_symbol` description clarity) by construction: there's no longer a
second tool whose rename path could be confused for the reference-chasing
one. "Renames don't touch prose" shipped 2026-07-14 (see DONE).
"Interface-method rename doesn't chase implementors" resolved the same day
— not a gap, a scope boundary (see DONE): renaming the interface *type*
already works via `move_symbol` unmodified (one mechanical resolution,
same as any other type rename); renaming one of its *required methods*
has no single correct resolution across implementors, so it stays
`edit_symbol`'s job by design, folded into the Tools doc overhaul's
principle-statement item above rather than tracked here. The last item —
iota groups, which turned out substantially bigger than "move" alone —
shipped in five phases 2026-07-15, see "Iota groups: full lifecycle
design" below for the complete design record and "Iota groups, phase 5"
in DONE for the closing summary. Nothing remains open in this section.

Answered by what actually happened, not decided in advance: every gap in
this section landed as an isolated, composed fix (`splitNewSymbolKey`,
`constPositionDependent`, `groupPositionDependent`, the prose-rename
helpers) rather than a typed-descriptor redesign of Refactorings as a
category. A shared multi-site-edit primitive future refactorings could
compose on remains a reasonable idea if a *new* gap surfaces that these
don't already cover — not pursued preemptively for gaps that turned out
to have narrower, cheaper fixes once actually worked through.

### Iota groups: full lifecycle design

Added 2026-07-15, all five phases shipped the same day. What started as
"move refuses iota groups, fix that" grew once the actual question was
asked: *how does an agent reference an
iota group at all?* Answer: it can't — a parenthesized const/var group is
an unaddressable shell in a declaration-scoped model; only its members
have identity. That reframing killed two earlier ideas in sequence (a
`ConstIota` kind with a shared-type prefix, then a first-element-anchor
prefix — both broken by `const ( _ Type = iota; ... )`, where the first
element isn't even a resolvable name) and produced the design below,
spanning Creators, Editors, and Refactorings — not just Refactorings,
which is why it's its own section rather than a Refactor-engine-overhaul
bullet. Solo-member iota groups are treated identically to multi-member
ones throughout (no special-casing a group of one).

**Creation.** A plain (non-iota) const/var joins the file's existing
non-iota group if one exists; the goal is at most one such group per file,
no standalone declarations. Not a Go requirement — Go tolerates any
number of groups and standalone declarations — it's a deliberate
consequence of the group shell having no addressable identity here:
multiplying shells creates placement decisions with no semantic backing
for the agent to reason about. The diffs this causes can touch unrelated
existing declarations (wrapping a standalone one into the shared group,
or appending to it), but never change meaning or values — the same
"transparent structural reorganization" precedent `goimports` already
sets on every write, not a new kind of side effect.
An iota (position-dependent) group never merges with an existing group,
typed or untyped, always starts its own:
- Untyped: placed in the standard const/var region, same as any other
  const/var (`declRegion`'s existing top-of-file ordering).
- Typed (`Kind = iota`): placed adjacent to that type's own declaration
  when it's in the same file — reusing `declRegion`'s existing
  method-clusters-with-receiver-type rule for a new case, not inventing
  placement machinery. Falls back to the untyped rule when the type isn't
  declared in the file being written to.

**Edition.** Addressing stays exactly as today — any member's own bare
key, no new grammar. A non-iota group member edits normally; introducing
`iota` into its expression is refused (converting a plain group into a
position-dependent one is a structural change, not a value edit, and
shouldn't slip in through a single-member replacement). An iota group
member's `src` must represent the group's complete new state — every
member, doc included — matching Move's atomic-unit treatment, applied
consistently to Edit instead of being Move-only. This must be documented
prominently: a partial submission is accepted as given, silently dropping
whatever member names aren't mentioned. That's not a new danger category
needing a bespoke guard — `edit_symbol` already trusts the agent's stated
`src` completely for every other kind of replacement; this extends the
same trust to a larger unit, and a dropped member that's still referenced
elsewhere surfaces exactly like any other replacement's collateral damage
already does, via diagnostics.
One refusal, not a heuristic: **the specifically-targeted key (the one
named in the call) must still be present among the resubmitted fragment's
keys.** If it isn't, refuse and point at `move_symbol`. This is
deliberately narrower than "no existing name may go missing" — that
version was tried and rejected, because a name disappearing from a
whole-group resubmission is observationally identical whether the intent
was "rename it" or "delete it entirely," and refusing on an ambiguous
signal would refuse legitimate whole-group rename-via-resubmission
attempts along with the accidental ones. Checking only the addressed key
sidesteps the ambiguity: that key is not ambiguous, the agent named it
explicitly. Other members appearing or disappearing in the resubmission
is ordinary, permitted edit behavior either way.

**Deletion.** Revised 2026-07-15, replacing an earlier promotion-based
draft. A plain group member's deletion is unchanged: last remaining
member removes the whole shell, a non-last member removes just that spec.
Deleting *any* member of a position-dependent group removes the whole
group, same shape as Move, not just the one named. The original draft had
this auto-promoting the new first remaining member to carry the anchor
role (copying the `iota` expression forward) — dropped once actually
examined: whether the promoted member should keep `iota` (renumbering
every remaining value, since `iota` is positional) or get its original
evaluated value written as a literal (preserving every value, no
renumbering) has no single correct answer, unlike the interface-method
case this reasoning was modeled on. Rather than guess, deletion doesn't
attempt partial removal at all for a position-dependent group — reducing
membership while deciding what happens to everyone else's values is
`edit_symbol`'s job, via a whole-group replacement that states the intent
explicitly, not a guess this verb would have to make silently.

**Move.** A plain const/var member moves individually, as any other
symbol does. Any member of an iota group moves the whole group, verbatim,
preserving relative order — regardless of which single member's key was
named in the call. `declSpan(sym)` already computes the whole group's
span for *any* member today (`sym.Decl()` is the shared `GenDecl`
regardless of which spec `sym` is) — the same code path `extractDecl`'s
solo-group branch already uses, so this is redirecting the existing
`groupUsesIota` refusal to that existing path, not building new
extraction machinery. Applies to *both* triggering conditions of today's
refusal (real `iota` use anywhere in the group, and a member with no
explicit value inheriting the previous spec's expression) — both are
"this spec's meaning depends on its position," both are fixed the same
way by moving the whole unit intact.

**Rename.** Both plain and iota-group members rename individually, the
same way, through `move_symbol`'s reference-propagating rename — not
`edit_symbol`'s replace-based rename, which the Edition rule above already
refuses for a group's targeted key. Reverses the 2026-07-13 "renaming an
iota-group member stays refused permanently" call: that reasoning didn't
hold up once actually worked through. Move is dangerous for a group
because it changes byte *position*, breaking the position-derived value
chain if a member is separated from its siblings. Rename never touches
position or order — it relabels an identifier in place and updates
references, exactly like renaming anything else. There's no mechanism by
which it could corrupt a position-derived value.

**Verification needed before implementing** (from the live-code read
2026-07-14, still unconfirmed at time of writing): `parseSpecFragment`'s
exact behavior on a genuinely multi-spec fragment — solo-group extraction
has only ever fed it one spec; whole-group extraction is new input shape
for it, though it's expected to just work (valid Go source either way).

**Explicitly dropped from scope, not deferred:** the `ConstIota`-kind /
shared-type-prefix and first-element-anchor-prefix addressing schemes —
both broken by blank-identifier first elements, superseded by "just
extend the existing const/var machinery, no new grammar." Also dropped:
refusing a second explicit `iota` reference elsewhere in an already-iota
group — that concern only existed to protect a "unique anchor" invariant
the abandoned addressing schemes needed; nothing in the current design
depends on there being exactly one recognized reference, so a group with
more than one explicit `iota` occurrence (legal, if unusual, Go) needs no
special handling.

**Implementation progress** (smallest/lowest-risk first, each phase
flushed and fully tested before the next): 1. Rename — **shipped
2026-07-15** (see DONE), the refusal check deleted from `Tx.MoveSymbol`,
nothing else changed since `renameSymbol` itself never special-cased
iota. 2. Move — **shipped 2026-07-15** (see DONE), `extractDecl`'s
refusal redirected to the same `declSpan` whole-group extraction the
solo-group case already used; `relocateSymbol` needed zero changes, it
was already agnostic to whether the extracted text was one spec or a
whole group. 3. Deletion — **shipped 2026-07-15** (see DONE), simpler
than originally scoped once the promotion draft was dropped: whole-group
deletion, no new "what happens to remaining values" logic at all. New
shared `constPositionDependent` (shared.go) factors the condition
`extractDecl` and `DeleteSymbol` both need, learned from the `symbolDoc`
duplication bug earlier in this same feature — named once, not
re-derived twice. 4. Edition — **shipped 2026-07-15** (see DONE): the
one real bug of this whole feature so far, caught before running any
test — the whole-group replacement text is bare specs (matching the
existing convention), but the target span is the whole group including
`const ( )`; splicing bare specs directly over that span would have
deleted the wrapper, producing invalid Go. Fixed by reconstructing
`tok + " (\n" + src + "\n)"` before splicing. `fragment` gained a
`usesIota` field (computed once in `classifyFragment`, reusing
`groupUsesIota`) to support the "refuse introducing iota into a plain
group" rule. One genuine regression from the test suite, not the new
code: `TestReplaceGroupedSpec` used `KindSquare` as its example of
"ordinary grouped spec editing," which — correctly, now — requires
whole-group resubmission since it was position-dependent; switched the
test to the var group (`DefaultScale`/`debugMode`), which was never
position-dependent, restoring what the test was actually meant to check.
5. Creation — **shipped 2026-07-15** (see DONE), last phase. **All five
phases now shipped — "Iota groups: full lifecycle design" is complete.**
Deliberately scoped narrower than the original design's most aspirational
clause: a new plain const/var merges into an *existing* grouped block,
but a *standalone* declaration is never retroactively converted into a
group — rewriting a declaration nobody asked to touch to make it a merge
target is a different, riskier kind of side effect than appending inside
parens that already exist, and the original design itself flagged this
exact case as "would cause too much friction" if forced.

### Smaller follow-ups

- **Recheck v2.** The post-edit reload is currently the whole workspace —
  correct by construction, sub-second at POC scale. When logged reload
  durations demand it, narrow to dirty packages plus transitive dependents
  via a stored import graph, and budget FileSet compaction into that
  change. Implemented inside the workspace package, behind the same
  accessors — verbs and lookups never learn which recheck ran.

  #### Implementation Alternative: Salsa/Query-Based Compilation
  Instead of manually maintaining an error-prone and drift-susceptible dependency/import graph:
  - **Mechanics:** Transition the compilation framework to a demand-driven, memoized query model (inspired by Salsa/rust-analyzer). Re-frame the compilation steps as queries: `ParseFile(path)`, `GetPackageSymbols(pkg)`, `TypeCheck(pkg)`. 
  - **Result:** Pure-function caching with automatic, correct-by-construction invalidation. Modifying a file only invalidates its direct parse and the downstream packages that actually consumed its inputs, eliminating cache-drift bugs entirely.

  **Tension worth naming (2026-07-12):** both recheck v2 options — the
  hand-maintained import graph above and the Salsa alternative — are
  incremental recompilation, which sits in direct tension with this
  codebase's second core invariant ("derived state is rebuilt, never
  patched... nothing incrementally maintained, so nothing drifts",
  AGENTS.md). Don't pursue either without the equivalence oracle in place
  first, and don't pursue either before measuring recheck latency *after*
  batch mutations land — batch already collapses N rechecks to 1 per
  transaction, which may remove the pressure that made this look necessary
  in the first place.

- **Persistent Structural Sharing / Content-Addressable ASTs.** As the codebase scales, transaction isolation via full deep-copying (`e.ws.Clone()`) on every transaction will incur severe garbage-collection (GC) and memory-allocation overhead.
  - **Mechanics:** Store files and symbols inside persistent, immutable data structures (like structurally shared hash-tries or Git-like content-addressable trees).
  - **Result:** Cloning a workspace becomes an $O(1)$ pointer copy. Applying edits copy-on-write only re-allocates path nodes up to the root while sharing unchanged sub-trees with previous snapshots, scaling effortlessly with zero heap churn.

- **Fine-grained modification.** Whole-declaration replacement is the
  dominant token cost of self-hosted work — a one-line change to a
  190-line declaration costs the whole declaration in the request.
  Sub-declaration addressing doesn't fit the current model, but if the
  weight keeps bearing, find a way: anchored splices within a declaration,
  or statement-range addressing bridged from SymbolAt.

  #### Implementation Alternative: Unified/Line-Based Diff Splicing
  To circumvent the token-heavy cost of whole-declaration replacement without exposing raw statement ranges to the agent:
  - **Mechanics:** Enhance `edit_symbol` to support submitting a Unified Diff hunk or a targeted line-replacement payload. Apply the diff strictly within the boundaries of the matched Symbol's byte span, and let `imports.Process` clean up surrounding syntax.
  - **Result:** Slices the request token cost of minor changes to large methods by up to 95%, keeping the virtual compilation loop extremely fast and context-efficient.

  #### Implementation Alternative: Structured Search and Replace (SSR)
  Added 2026-07-15, not explored yet — mapped only, so the idea isn't
  lost. Prior art: JetBrains' SSR, Comby, ast-grep/semgrep autofix — all
  match and rewrite by *structure*, not by line position or raw text.
  - **Mechanics:** Agent submits a structural pattern with placeholders
    (e.g. `if $COND { return $ERR }`) matching statement- or
    expression-shaped fragments *inside* a declaration's own AST, plus a
    replacement template using the same placeholders. The server matches
    against the symbol's existing parsed AST (already in memory — no new
    parse pass) and splices only the matched span(s), the same
    span-and-splice mechanism every other verb already uses, just
    anchored to a sub-declaration match instead of a whole-declaration or
    whole-spec boundary.
  - **Result:** Sidesteps line-position fragility entirely (unlike a
    line-based diff, a structural pattern survives surrounding
    reformatting) and doesn't require the agent to know or express byte/line
    ranges at all — the pattern *is* the address. Open questions for
    whenever this gets picked up: how large a pattern DSL to expose
    (full go/ast shape vs. a constrained subset), whether multiple matches
    within one declaration are a batch-like "all or refuse" or independently
    addressed, and how this interacts with the existing whole-declaration
    `edit_symbol` convention rather than replacing it.

- **External test package (_test-suffixed) creation** is unsupported:
  create_file targets the production package only.
- **Flush is not atomic across files**: a mid-flush I/O error leaves a
  partial write on disk (in-memory state stays consistent; re-flush
  recovers).

### BUGS

None open.

## DONE

- **Deletion semantics: new Deleters category, noop-if-absent — 2026-07-16.**
  See "Deletion semantics" above for the full design record. `delete_symbol`/
  `delete_file`/`delete_package` split out of Editors into their own Tx
  category and Go file (`deleters.go`, both `internal/engine` and
  `internal/tools`), and stopped erroring when the target's already gone —
  idempotent deletion, the mirror of Creators' "fail if exists."
  - Raised while scoping `delete_symbol_batch`: deleting an already-gone
    iota-group sibling (the first member's delete already collapsed the
    whole group) shouldn't abort a batch over a technicality the agent
    already achieved. Generalized past batch once posed — resolves that
    aliasing problem for free, no pre-scan needed, whenever
    `delete_symbol_batch` gets built.
  - **A refusal turned out to be a missing capability, not a real
    ambiguity.** `DeleteSymbol` used to refuse a multi-name `*ast.ValueSpec`
    (`var a, b int`) outright, described as "no single correct resolution."
    Under scrutiny that was wrong: it's fully deterministic once split on
    shape — one value per name (or none) trims the targeted name and its
    paired value; one shared multi-valued expression (`var a, b = f()`)
    blanks the targeted name to `_` instead, since the call's arity is
    fixed and blanking is the only transform leaving every other name
    unaffected. Deleting every real name in a shared spec converges to a
    full removal, call included, matching solo deletion's existing
    behavior. Caught by the same discipline as the iota-groups bugs:
    working the concrete example by hand before accepting "ambiguous" as
    the reason a refusal existed.
  - New fixture: `testdata/sandbox/shapes/groups.go` gained
    `boundsOf()`/`var boundX, boundY = boundsOf()` for the shared-call
    case (`var minX, maxX = -10.0, 10.0` already covered the parallel
    case) — "add the fixture shape that would have caught its absence."

- **Batch mutations, per-verb, first two shipped: `create_symbol_batch`,
  `edit_symbol_batch` — 2026-07-15.** Superseded the original unified,
  typed-descriptor batch design before building any of it — see "Batch
  mutations" above for the full reasoning. Each new tool takes an array of
  its existing single-statement input (`CreateSymbolBatchInput{Creates
  []CreateSymbolInput}`, `EditSymbolBatchInput{Edits []EditSymbolInput}`)
  and returns the same `WriteOutput` every other Creator/Editor already
  does — no new output type, confirmed by actually building it rather
  than designing it in advance (resolves "Mutation-output & tool
  interface," see that entry above).
  - Both compose directly on `Tx.CreateSymbol`/`Tx.EditSymbol`: one `Tx`,
    a sequential loop calling the existing per-statement method, first
    error returned aborts the whole batch via the same clone-discard
    `Engine.Edit` already does for a single statement. Composition, not
    new machinery.
  - Duplicate targets are refused, not deduplicated — precedent: SQL
    multi-row `INSERT` failing a unique-constraint violation atomically,
    Terraform's hard "duplicate resource address" error; rejected the
    alternative (Elasticsearch Bulk API's silent last-write-wins) as the
    wrong shape for a batch where each entry is a discrete, deliberate
    agent decision, not a stream of idempotent events. `edit_symbol_batch`
    pre-scans and refuses before the `Tx` opens (the key is given
    explicitly); `create_symbol_batch` needed no equivalent code at all —
    `Tx.CreateSymbol`'s own existing collision check already catches a
    repeated name on the second attempt, for free.
  - **Real bug, caught only by running the tests, not by `diagnostics()`:
    a deadlock.** `TestCreateSymbolBatch` hung for the full 10-minute test
    timeout and panicked. Root cause: `createSymbolBatch`'s first draft
    called `packageArg` (which needs `eng.ModulePath()`, a read lock)
    *inside* the loop *inside* the `Tx` closure — but `Engine.Edit` holds
    the write lock for the closure's whole lifetime, and `sync.RWMutex`
    isn't reentrant, so the second entry's read-lock acquisition deadlocked
    against the write lock the same goroutine already held. No compile
    error, no panic until the test timeout fired — a purely runtime
    property invisible to static diagnostics. `editSymbolBatch` was
    accidentally safe: its duplicate pre-scan already forced every
    `packageArg` call before the `Tx` opened, for an unrelated reason.
    Fixed by resolving every entry's `pkg`/`file` in a pre-pass, passing
    only the resolved values into the closure — matching the discipline
    every existing single-statement handler already followed, just broken
    when the loop moved. **Documented in AGENTS.md's Core invariants** as
    a new, previously-unstated rule ("never call an `Engine`-level
    accessor from inside a `Read`/`Edit` closure") specifically so a
    future batch handler (or any future multi-step verb) doesn't
    rediscover this the same way — via a 10-minute hang.
  - New tests: success and abort-on-failure for both tools, duplicate-
    target refusal for edit, empty-batch refusal for both. 78/78 tests
    (verified via `rtk proxy` with an explicit `-timeout`, not the
    default, after the deadlock — a bounded timeout is now the safer
    default for this codebase's test runs generally), `gofmt`/`go vet`
    clean, flushed.
- **Tools doc overhaul, closed out, 2026-07-15.** Both remaining items
  shipped:
  - **Rephrase pass.** Found two real weak spots reading through every
    description fresh, not just cosmetic: `list_symbols` still said "Pass
    file to restrict" — the one place a description referenced a
    parameter by a name stale relative to the actual field (`file_name`),
    everywhere else already consistent. `move_package`'s description
    never mentioned the doc-comment prose fix ("Renames don't touch
    prose," shipped the previous day) — it only described the import
    path and qualifier rename, silently missing a whole piece of what the
    tool now does. Both fixed.
  - **Stated the "prefer Refactor over Editor" principle**, in both
    places the open item named, scoped to their different audiences: a
    concrete, actionable line in `main.go`'s `Instructions` ("prefer
    move_symbol over edit_symbol when renaming: move_symbol propagates
    the rename to every resolved reference automatically; edit_symbol's
    replacement only changes the declaration itself") for agents *using*
    gomcp's tools; the abstract test ("does this edit have exactly one
    mechanically correct resolution, or does it need per-site judgment?")
    as a new AGENTS.md Nomenclature-grammar bullet, citing both concrete
    instances (`move_symbol`'s rename, the interface-method-rename scope
    decision) as examples, for future gomcp *developers* deciding which
    category a new verb belongs to. Two audiences, two placements, not
    redundant.
  - `Instructions` is a behavior-facing change to the running binary —
    takes effect on rebuild + reconnect, not on flush alone.
  - Tests unaffected (nothing asserts on exact description/Instructions
    text); `gofmt`/`go vet`/`go test ./...` all clean, flushed.
- **AGENTS.md summarization, shipped 2026-07-15.** Read the whole file
  fresh against its own stated goal (pure reference material, not a
  record of what/when/why) and found two real spots, not a wholesale
  rewrite — most of the file was already tight:
  - The Nomenclature grammars section opened by restating the Pillars'
    Nomenclature bullet nearly verbatim before adding anything new —
    trimmed to keep only the additive part (the `move_symbol`
    one-call-fix detail, the domain-vocabulary rule) instead of repeating
    the principle a second time.
  - The "always flush" note under "Working on this repo from a connected
    gomcp session" had accreted into a full rationale trail ("not a
    general gomcp requirement, but... two more consequences worth
    remembering here...") across three sentences — compressed to the
    rule itself plus its two consequences, same information, roughly
    half the words.
  - 206 → 197 lines. Everything else (Pillars, Layout, Core invariants,
    Address convention, Testing) held up against the same standard
    without needing a change — already crisp, reference-oriented prose,
    not narrative.
- **BUGS: "edit_symbol on an iota group's first member collapses the
  group" resolved by iota groups phase 4 (Edition), 2026-07-15.**
  Replacing the spec that carried `T = iota` used to make every bare
  sibling silently inherit the new expression. Fixed as a side effect of
  requiring whole-group resubmission for any position-dependent member's
  edit — `specSpan`-based single-spec replacement, the mechanism that let
  this happen, no longer applies to a position-dependent member at all.
  See "Iota groups, phase 4" below for the full change.

- **Iota groups, phase 5 (Creation) shipped, closing out "Iota groups:
  full lifecycle design" — 2026-07-15.** Last and largest phase, the only
  one needing genuinely new machinery rather than redirecting or
  extending an existing path. `CreateSymbol` now: merges a new plain
  const/var into the target file's existing grouped block of the same
  kind, if a non-position-dependent one already exists (new
  `findMergeableGroup`, new `groupPositionDependent` — the whole-group
  counterpart to `constPositionDependent`, used to decide whether an
  *existing* group is safe to merge into rather than whether one member
  is); places a new typed iota group next to its shared type's own
  declaration when that type is in the same file (new `typeDeclOffset`,
  extending `declPrecedes`'s existing method-clusters-with-receiver
  logic to a new case rather than inventing placement machinery); falls
  back to the standard const/var region otherwise, same as an untyped
  iota group always does. New `constVarSpecs` (fragments.go) parses the
  agent's `src` a second, more detailed way than `classifyFragment`
  needs — extracting each spec's own text and the first explicit type
  name — machinery neither merge nor type-adjacent placement could do
  with the summary-only `fragment` struct alone.
  - **Deliberately narrower than the original design's most aspirational
    clause**: merges into an *existing* group, but never retroactively
    converts a *standalone* declaration into one. The original design
    itself flagged forcing every const/var into one shape as something
    that "would cause too much friction" if pursued fully — rewriting a
    declaration nobody asked to touch to manufacture a merge target is a
    different, riskier kind of side effect than appending inside parens
    that already exist.
  - New tests: `TestCreatePlainConstMergesIntoExistingGroup`,
    `TestCreateIotaGroupNeverMergesIntoExistingGroup` (two separate iota
    groups created in the same file stay separate, confirmed by counting
    `const` keyword occurrences), `TestCreateTypedIotaGroupNearItsType`
    (byte-offset order confirms the const group lands *after* its type,
    the opposite of default region-based placement, proving the new
    clustering logic actually fired), `TestCreateUntypedIotaGroupStandard
    Region` (confirms the default/fallback path is unaffected — same file
    shape, but the untyped group lands *before* the type, as it always
    did).
  - 71/71 tests, `gofmt`/`go vet` clean, flushed. All five phases of the
    feature complete; Refactor engine overhaul has nothing open.

- **Iota groups, phase 4 (Edition), shipped 2026-07-15.** Fourth phase of
  "Iota groups: full lifecycle design" (see TODO); resolves the BUGS
  entry above as a side effect, not a separate fix. `EditSymbol` now
  branches three ways: a position-dependent member requires the group's
  whole state (every spec, still bare — no `const ( )` wrapper in the
  agent's `src`, matching the existing single-spec convention); an
  ordinary grouped member edits as before, except introducing `iota`
  into a group that didn't use it is now refused (new `fragment.usesIota`,
  computed once in `classifyFragment` via the existing `groupUsesIota`);
  an ungrouped symbol is unchanged.
  - **Real bug caught before running anything, not after**: the
    whole-group branch's replacement text is bare specs (the established
    convention), but its target span (`declSpan`) is the *whole* group
    including the `const ( )` wrapper. Splicing bare specs directly over
    that span would have deleted the wrapper — invalid Go, a hard
    parse/type error on the very first test run. Caught by tracing the
    span/replacement mismatch through by hand before writing a single
    test, not by watching a test fail. Fixed by reconstructing
    `gen.Tok.String() + " (\n" + src + "\n)"` before splicing.
  - One refusal, deliberately narrow: the *targeted* key must still
    appear in the resubmitted fragment, or refuse and point at
    `move_symbol` — not "no existing name may vanish," which was
    considered and rejected during design (see the design section) for
    producing false positives on legitimate whole-group renames.
  - Collision check extended, not replaced: a `newKey` that already
    exists is fine if it belongs to the *same* group being resubmitted
    (an expected sibling, not a collision) — checked via `groupOf(existing)
    == gen`, not just `newKey == key` as before.
  - New tests: `TestEditIotaGroupWholeReplacement` (grows the group by
    one member in a single whole-state edit), `TestEditIotaGroupPartial
    SubmissionDropsSiblings` (confirms the documented risk is real and
    intentional, not accidentally guarded against), `TestEditIotaGroup
    RefusesRenamingTargetedKey`, `TestEditGroupRefusesIntroducingIota`.
  - One genuine regression surfaced by the full suite, not a bug in the
    new code: `TestReplaceGroupedSpec` used `KindSquare` — now correctly
    classified as position-dependent — as its example of "ordinary
    grouped spec editing, siblings survive." Switched to the var group
    (`DefaultScale`/`debugMode`, never position-dependent) to restore
    what the test actually meant to check, rather than weakening the new
    behavior to keep an example that no longer fit it.
  - 67/67 tests, `gofmt`/`go vet` clean, flushed.
- **Iota groups, phase 3 (Deletion), shipped 2026-07-15.** Third phase of
  "Iota groups: full lifecycle design" (see TODO), and simpler than
  originally designed. The original plan had `DeleteSymbol` auto-promote
  the new first remaining member when a group's anchor was deleted,
  copying its `iota` expression forward — dropped before implementation,
  not after: asked directly whether that should renumber every remaining
  value (literal `iota`, positional) or preserve them (write the original
  evaluated value as a literal instead), realized there's no single
  correct answer, and that guessing either way risked exactly the kind of
  silent-wrong-value failure this whole feature exists to prevent.
  Resolved by not attempting partial removal at all: deleting any member
  of a position-dependent group now deletes the whole group, the same
  shape `move_symbol` already has — reducing a group's membership while
  deciding what happens to everyone else's values is `edit_symbol`'s job
  now (its whole-group-replacement requirement from phase 4's design
  already covers exactly this), not something delete should guess at.
  New shared `constPositionDependent(gen, grouped, sym)` (shared.go)
  factors the condition `extractDecl` (phase 2) and `DeleteSymbol` both
  need — named once, not re-derived independently a second time, directly
  applying the lesson from the `symbolDoc`/`soloGroup` bug earlier in
  this same session (see the DONE entry on auditing that placement).
  New `TestDeleteWholeIotaGroup`: deleting the *non-anchor* member
  (`KindSquare`) removes the anchor (`KindCircle`) too, even though it
  was never named. 63/63 tests, `gofmt`/`go vet` clean, flushed.
- **Iota groups, phase 2 (Move), shipped 2026-07-15.** Second phase of
  "Iota groups: full lifecycle design" (see TODO). `extractDecl`'s
  refusal for a position-dependent const member (`len(spec.Values) == 0
  || groupUsesIota(gen)`) redirected to the same `declSpan`-based
  whole-group extraction the solo-group branch already used — not new
  extraction machinery, the exact existing path for a different trigger.
  `relocateSymbol` needed zero changes: it was already agnostic to
  whether the extracted text represented one spec or a whole multi-spec
  group, and `RebuildIndex` already re-derives every symbol's file from
  wherever it physically ends up post-swap, so every group member's
  `File` updates correctly with no special bookkeeping. `move_symbol`'s
  description corrected (it briefly said relocation "refuses... alone"
  after phase 1's edit, stale the moment phase 2 changed what actually
  happens). New `TestMoveWholeIotaGroup`: names the *non-anchor* member
  (`KindSquare`) and confirms the anchor (`KindCircle`) — never named —
  moves along with it, into a fresh file, verbatim and in order; old file
  left with neither. `TestMoveRefusals`' `KindCircle` case removed (no
  longer refused, now covered by the success test instead). 62/62 tests,
  `gofmt`/`go vet` clean, flushed.
- **Iota groups, phase 1 (Rename), shipped 2026-07-15.** First phase of
  "Iota groups: full lifecycle design" (see TODO). Deleted the refusal
  check from `Tx.MoveSymbol` (`if grouped && groupUsesIota(gen) { return
  error }`) — nothing else needed changing, since `renameSymbol` itself
  never special-cased iota membership; the refusal lived entirely in the
  wrapper. New `TestMoveSymbolRenamesIotaGroupMember` covers both a
  non-anchor member (`KindSquare`) and the anchor itself (`KindCircle`,
  the member carrying the explicit `iota` expression) renaming cleanly,
  with the sibling and the anchor's own leading-doc-comment update (from
  the prose-rename feature) both verified as a side effect of using the
  real sandbox fixture. 61/61 tests, `gofmt`/`go vet` clean, flushed.
- **"Interface-method rename doesn't chase implementors" resolved as scope,
  not a gap, 2026-07-14.** Refactor engine overhaul's last remaining item
  besides the iota-group move. Pedro's framing: `move_symbol` can already
  rename an interface — the type itself, `Shape → Polygon` — since a
  type's name is one identity everywhere, exactly the mechanical case
  Refactorings exist for; renaming one of its *required methods* is a
  different kind of edit entirely, because there's no single correct
  propagation (one implementor might rename to match, another might need
  a new delegating method, another might deliberately drop conformance) —
  that's `edit_symbol`'s job, with diagnostics enumerating every site
  needing a decision, exactly like today's behavior already does. No code
  change: the current behavior (rename exactly one object, report broken
  satisfaction via diagnostics) was already correct, just undocumented as
  intentional rather than pending.
  - Generalizes to a test worth stating once, not per-feature: does an
    edit have exactly one mechanically correct resolution across every
    site, or does each site potentially need a different judgment call?
    The former is Refactorings territory; the latter is `edit_symbol` plus
    diagnostics, on purpose, not as a gap. Applied the same test to a
    gopls capability survey (extract/inline function, change signature)
    at Pedro's prompt ("is there something else in gopls missing from our
    side?") — extract/inline is blocked on the already-tracked
    Fine-grained-modification item (no sub-declaration addressing exists
    yet), not a new Refactorings gap; change signature fails the same
    single-resolution test interface-method rename just failed (most call
    sites need a judgment call — what value for a new parameter, what to
    do about a removed one's callers), so it doesn't belong here either.
    No new Refactorings gap surfaced.
  - Folded into the Tools doc overhaul's still-open "state the principle"
    item rather than tracked separately — it's a second instance of the
    same unstated rule, not an independent finding.
- **Real bug caught auditing the prose-rename helpers' placement, fixed
  2026-07-14.** Pedro asked "are the helpers in the correct place?" right
  after the prose-rename feature shipped — surfaced two distinct findings,
  not one:
  - `symbolDoc` (new, for the prose feature) copied `extractDecl`'s span
    condition (`!grouped || len(gen.Specs) == 1`, collapsing a solo-member
    parenthesized group to "ungrouped") to decide which `.Doc` field holds
    a symbol's own comment. That condition is *correct* for span-based
    extraction/deletion (removing the only member removes the whole group
    either way, so the collapse is harmless there) but *wrong* for doc
    lookup: Go's parser attaches a per-spec comment to that spec, never to
    the enclosing `GenDecl`, regardless of member count. `EditSymbol`
    already had the right condition (plain `grouped`, no count check) for
    the same reason. Verified empirically before asserting anything —
    wrote `TestMoveSoloGroupedSpecPreservesDoc` (confirmed extraction is
    fine: it grabs the whole `GenDecl` byte range regardless of formal
    `.Doc` attachment) and `TestRenameSoloGroupedSpecUpdatesDoc` (confirmed
    the rename case was genuinely broken — a solo grouped const's doc
    comment survived a rename of its identifier untouched). Fixed
    `symbolDoc` to match `EditSymbol`'s condition instead of `extractDecl`'s.
  - Named both conditions explicitly (`soloGroup(gen, grouped) bool`, now
    shared by `extractDecl` and `DeleteSymbol`, which had spelled the exact
    same expression out independently) specifically so the two boundaries
    — "solo group ≈ ungrouped" for span purposes, "grouped, any count" for
    doc-attachment purposes — can't be silently confused again the way
    they almost were here. Composition pillar in the negative: the
    original `symbolDoc` violated it by re-deriving logic under a new name
    instead of reusing what `extractDecl` already had; the fix wasn't to
    reuse it (the semantics genuinely differ), but to make both boundaries
    named and impossible to mix up by accident.
  - The file placement question itself resolved cleanly: `shared.go` is
    the right home for `symbolDoc`/`leadingDocWord`/`soloGroup` — it's
    exactly where cross-category helpers (`groupOf`, `definingIdent`)
    already live, because both extraction and refactoring genuinely need
    them. The placement was fine; the content momentarily wasn't.
  - Both new regression tests kept permanently, not treated as scratch
    verification — matches this project's standing rule: "the sandbox
    exists to be broken... add the fixture shape that would have caught
    its absence." 60/60 tests, `gofmt`/`go vet` clean (via `rtk proxy`),
    flushed.

- **Renames don't touch prose, shipped 2026-07-14.** Refactor engine
  overhaul's second item. Guiding principle settled before any code, worth
  keeping for future automation decisions on this codebase: match Go's own
  tooling conventions wherever they exist and are already tool-checked
  (here, `go vet`'s comment lint — a declaration's doc opens with its bare
  name, a package's doc opens with `"Package name"`); never invent a
  broader heuristic where no such convention exists. That ruled out the
  originally-proposed wider scope (text-replacing every mention of a name
  across every doc comment in the workspace) — a real hazard for any
  short/common identifier (`Add`, `Set`, `Get`) or package name (`io`,
  `time`) that doubles as an ordinary English word.
  - `move_symbol`: renaming a symbol also splices its own leading
    doc-comment mention, when present and conforming — folded into
    `tx.renameSymbol`'s existing splice set (one atomic edit, not a
    separate pass). Applies uniformly to every kind Go doc convention
    covers, including grouped const/var members, since the only real
    variation is *where* the doc attaches in the AST (`GenDecl` for an
    ungrouped declaration, the individual `ValueSpec`/`TypeSpec` for a
    grouped member) — semantics don't change per kind. New `workspace.
    DocOf` is the single authority both extraction and this rename now
    agree on for "where a node's doc lives"; new engine-side `symbolDoc`/
    `leadingDocWord` helpers do the locate-and-validate-and-splice work,
    shared by both this and the `move_package` case below.
  - `move_package`: when the identifier rename already fires (declared
    name matches the old directory base), every file in the moved unit
    (`Prod` and `XTest` both — the same set `Package.Doc()` concatenates)
    gets its own doc comment checked for a leading `"Package oldBase"`
    opening, rewritten to `"Package newBase"` when found. Reuses the
    existing `oldBase`/`newBase` derivation `Tx.MovePackage` already
    computes for the qualifier rename — no new "trim the package name
    from PkgPath" logic needed, Composition over reinventing it.
    Deliberately does *not* scan other packages' doc comments for prose
    mentions of this package's name, same false-positive reasoning as
    `move_symbol`.
  - `move_file` explicitly excluded from this item — no Go convention
    ties a file's base name to any comment text, so there's nothing
    tool-checked to fix; a file's own doc comment is about the *package*,
    not the file's identity.
  - New tests: `TestRenameUpdatesLeadingDoc` (engine) — a conforming doc
    gets its leading mention rewritten; a non-conforming doc (real prose
    that doesn't open with the declared name) is left untouched, the
    explicit safety-boundary case. `TestMovePackagePropagates` extended
    with the same shape: `shapes.go`'s real fixture doc (`"Package shapes
    provides..."`) gets rewritten to `"Package geo provides..."`;
    `groups.go`'s doc (which mentions "shapes" mid-sentence, not as its
    opening) is asserted unchanged.
  - AGENTS.md gained a Nomenclature-grammar bullet stating the general
    principle (match official Go conventions where tool-checked, don't
    invent heuristics where they aren't) for future automation decisions
    to cite, not just this one feature.
  - 58/58 tests, `gofmt`/`go vet` clean (verified via `rtk proxy` for raw
    output — see the process note above), flushed.

- **`MoveSymbolInput.NewSymbolKey` replaces `NewName`, shipped 2026-07-14.**
  Follow-on to the field-naming audit above: `new_name` was the one
  destination field on the whole surface that didn't share `SymbolKey`'s
  grammar, forcing an agent that mirrors the source key's shape
  (`"Circle.Extent"`) into a guaranteed failed call, then a retry with the
  bare form. Fixed by making the destination genuinely `SymbolKey`-shaped
  instead of just documenting the bare-only exception:
  - For a method source, `new_symbol_key` must be qualified (`"Recv.Name"`)
    and `Recv` must equal the symbol's actual receiver exactly — a rename
    can never change what a method belongs to, so this isn't optional
    leniency, it's the same requirement `symbol_key` already has as a
    source address. For a non-method source, it must stay bare — a dotted
    value is rejected outright.
  - New `splitNewSymbolKey` (engine/refactorings.go, beside `Tx.
    MoveSymbol`) does the split-and-validate; `Tx.MoveSymbol`'s last
    parameter renamed `newSymbolKey` to match.
  - New tests: `TestMoveSymbolRenameQualification` (engine) exercises all
    four paths — bare non-method rename, qualified-on-non-method refusal,
    unqualified-on-method refusal, mismatched-receiver refusal, successful
    qualified method rename; `TestMoveSymbolInputWiring` (tools) is the
    end-to-end smoke test through the JSON-decoded `*string` field. Neither
    existed before — the composed rename-via-MoveSymbol path had zero
    coverage previously (only the private `renameSymbol` primitive was
    tested directly).
  - Caught a real bug in the first draft of the engine test itself:
    asserted zero diagnostics from renaming `Circle.Area`, which is wrong —
    that rename legitimately breaks `Shape` satisfaction, exactly what
    `TestRenameMethodReportsBrokenSatisfaction` already establishes as
    correct behavior elsewhere. Caught by actually reading `go test`'s raw
    output rather than trusting a summarized-in-the-terminal PASS count
    (see the process note below).
  - 57/57 tests, `gofmt`/`go vet` clean, flushed.

**Process note, 2026-07-14: verify against disk state, not gomcp's
in-memory model.** `go test`/`gofmt`/`go vet` via the shell read the
filesystem — gomcp's edits aren't there until `flush`. Ran the full suite
via Bash immediately after two `create_symbol` calls without flushing
first; the shell's summarized output claimed "55 passed" both before and
after (stale — silently checking pre-edit disk state, not the new tests at
all). Only caught by explicitly re-running through `rtk proxy` (raw,
unfiltered output) after flushing, which surfaced a real test bug the
summarized count had hidden. Lesson: flush before any disk-reading
verification step, and prefer `rtk proxy <cmd>` over the summarized form
when a test run needs to be trusted, not just glanced at.

- **Optional-field convention: `*T` + `omitempty` everywhere, shipped
  2026-07-14.** Triggered by a shape audit that noticed `Move*Input`'s
  destination fields (`NewPkgPath`/`NewFileName`/`NewName`) had no
  `omitempty` at all — traced to the SDK actually in use
  (`mcp.AddTool` → `google/jsonschema-go`): a field's presence in the
  generated schema's `required` array is driven solely by the
  `omitempty`/`omitzero` tag, independent of pointer-ness. That fixed the
  agent-facing bug, but left a human-facing one: a plain `string` field
  gives no type-level signal that it's optional, tag or not — the exact
  complaint that started this ("I can't tell for sure if those are
  optional"). Resolved by unifying on one rule for every optional field,
  input and output alike: pointer type + `omitempty`, nil means omitted.
  - `omitempty`/`omitzero` are marshal-only directives — neither affects
    `json.Unmarshal`. An explicit `""` from a caller always decodes to a
    non-nil `*string` pointing at `""`; only an absent field or an
    explicit JSON `null` decodes to `nil`. So handlers still can't get
    away with a bare `!= nil` check — a new `optStr(*string) string`
    helper (shared.go) collapses nil-or-empty to `""` in one place,
    replacing the ad hoc `in.X != ""` checks that used to live at each
    call site.
  - Converted: `DiagnosticEntry.{PkgPath,FileName,SymbolKey}`,
    `DescribePackageOutput.Doc`, `DescribeFileOutput.Doc` (output side);
    `ListSymbolsInput.FileName`, `CreatePackageInput.Name`,
    `CreateFileInput.Doc`, `EditFileInput.Doc`, `MoveSymbolInput.{New
    PkgPath,NewFileName,NewName}`, `MoveFileInput.{NewPkgPath,NewName}`
    (input side). Slices and maps (`Files []string`, `Written map[...]`)
    deliberately left alone — nil already means "none" for those with no
    ambiguity, pointer-to-slice would add a second way to say the same
    thing.
  - Constructors use `new(expr)` (Go 1.26) for pointer literals —
    `new("New file doc.")`, `new(true)` — same pattern `WriteOutput`
    already established for `*DiagBlock`/`*int`/`*bool`.
  - 55/55 tests, `gofmt`/`go vet` clean, flushed.
- **Move verb consolidation + PkgPath/FileName/SymbolKey rename, shipped
  2026-07-13.** Refactor engine overhaul's first pass, same day as the
  natural interface audit that flagged it: one Move verb per entity,
  Consistency over accreted one-bug-at-a-time fixes.
  - **Full-surface rename, all 26 (now 25) tools:** `Package → PkgPath`,
    `File → FileName`, `Key → SymbolKey` — Go fields, JSON tags, and output
    entry types (`SymbolEntry`, `MatchEntry`, `DiagnosticEntry`) all moved
    together. Pedro's call, explicit: "definitely b, full consistency" over
    scoping the rename to just the new move_* fields. Also fixed the two
    field names the audit flagged (`ListMethodsInput.Type`,
    `SearchImplementorsInput.Name` → `Key`, pre-rename) in the same pass.
  - **`rename_symbol` retired, folded into `move_symbol`.** `Tx.RenameSymbol`
    became private `tx.renameSymbol`, now composed by `Tx.MoveSymbol`
    (Composition: rename first via the same workspace-wide reference
    chasing a standalone rename always did, then relocate the — possibly
    renamed — declaration). New signature: `MoveSymbol(pkg, key, newPkgPath,
    newFileName, newName)` — any combination of the three destination
    fields, at least one required; `newPkgPath` given without `newFileName`
    is refused (a cross-package move must name its destination file).
  - **`rename_file` → `move_file`, gained cross-package relocation.**
    Same-package call still delegates to the plain rename primitive;
    cross-package rewrites the package clause via splice and re-enters
    through `reloadFile`. Cross-package moves (both `move_file` and
    `move_symbol`) deliberately don't rewrite qualifiers at use sites still
    referring to the old package — Pedro's "take the hit" call: let the
    diagnostics echo surface the breakage rather than attempting
    reference-aware qualifier rewriting for a mechanism (whole-declaration
    extraction) that isn't the identifier-only splicing `gatherUses`/
    `definingIdent` do for renames.
  - **`rename_package` → `move_package`, verb-only rename.** Behavior
    unchanged (import-path rewrite in every importer, qualifier rename when
    the package name matched the old directory base); this resolves the
    audit's `NewPath`-vs-`NewName` question — the tool itself was the
    outlier, not the field, since a package's identity really is its path.
  - **Iota decision, permanent:** `move_symbol` refuses renaming any
    iota-group member outright (checked before any edit is attempted, same
    place the existing relocation refusal lives) — Pedro: renaming a value
    defined purely by its position in the group doesn't have a coherent
    meaning. Relocating an iota group across files as one atomic unit is a
    separate, real capability, deliberately deferred (see Refactor engine
    overhaul TODO) — today's per-member relocation refusal for iota members
    is unchanged.
  - Tools-layer shapes: `MoveSymbolInput{PkgPath, SymbolKey, NewPkgPath,
    NewFileName, NewName}`, `MoveFileInput{PkgPath, FileName, NewPkgPath,
    NewName}`, `MovePackageInput{PkgPath, NewPkgPath}`. `RenameFileInput`/
    `RenamePackageInput` renamed in place (same declaration, new shape);
    `RenameSymbolInput` and its handler deleted outright, nothing to rename
    it to.
  - Tests: engine-side `TestRenamePackagePropagates` → `TestMovePackage
    Propagates`, `TestRenameFileAndFlush` → `TestMoveFileAndFlush` (renamed
    alongside their calls, now exercising the public verb under its current
    name); `TestRenameSymbolPropagates`/`TestRenameMethodReportsBroken
    Satisfaction` kept their names (they exercise the private `renameSymbol`
    primitive itself, still named "rename" at that layer) with calls updated
    to `tx.renameSymbol`. `TestMoveSymbol`/`TestMoveGroupedSpec`/
    `TestMoveRefusals`/`TestMoveToNewFile` updated to the 5-arg signature.
    Tools-side `TestAddressForms` updated to `moveFile`/`MoveFileInput`.
    55/55 passing, `gofmt`/`go vet` clean, flush checkpoints after the
    field rename and after the engine-layer move_* work landed.
  - README: 26 → 25 tools (rename_symbol retired, net −1); Refactorings
    line reworded to `move_symbol` (rename, relocate, or both), `move_file`,
    `move_package`.
- **Natural interface audit + tool description category prefixes, shipped
  2026-07-13.** Third and fourth items of the tools-surface work, same
  day. Audit pulled every tool's input/output struct straight from
  `tools.go` (26 tools) into a reference table (published as an artifact),
  organized by the same eight categories `Register`/README already use.
  Findings:
  - Two field names break the established `Key` vocabulary for no found
    reason: `ListMethodsInput.Type` and `SearchImplementorsInput.Name`
    both address a type by the same concept every other tool calls `Key`.
    Fix filed above (Tools doc overhaul), not yet applied.
  - `RenamePackageInput.NewPath` breaks the `NewName` pattern
    `rename_symbol`/`rename_file` both use — but its own description
    already says "Move a package directory," so this may be `rename_
    package` being the wrong verb rather than `NewPath` being the wrong
    field. Left as an open decision, not a mechanical fix.
  - Finders (`search_*`) carry no diagnostics at all, unlike every
    Enumerator/Describer. Plausibly deliberate (a workspace-spanning
    match list has no single scope to attach diagnostics to) but flagged
    rather than assumed.
  - Confirmed *not* inconsistencies, stated explicitly so they don't read
    as oversights later: `flush` lacking a `DiagBlock` while `reload` has
    one (flush never rechecks; reload always does), and `WriteOutput`'s
    diagnostics shape deliberately differing from read-side `DiagBlock`
    embedding (settled earlier this session, see the diagnostics-shipped
    entry below).
  - Everything else — `Package`/`File`/`Doc`/`Source` naming, snake_case
    JSON tags throughout, `DiagBlock`'s shape wherever embedded,
    `WriteOutput` shared byte-for-byte across every Creator/Editor/
    Refactoring — holds across all 26 tools. Two stray field names and one
    debatable tool verb, out of 26.
  - Category prefixes applied to every tool description in `Register`
    (`[Enumerator]`, `[Describer]`, `[Finder]`, `[Diagnostics]`,
    `[Creator]`, `[Editor]`, `[Refactoring]`, `[Session]`) — the other half
    of the Tools doc overhaul (rephrase pass, the "prefer Refactor over
    Editor" principle, the two field renames, the `NewPath` decision)
    remains open, see TODO. 52/52 tests, `gofmt`/`go vet` clean.
- **describe_symbol (describe_* consolidation) + "Symbol" vocabulary
  unified across every write tool, shipped 2026-07-13.** Second item of
  the tools-surface work, same day as the first.
  - `describe_type`/`describe_function`/`describe_method` (and their
    shared `describeDecl` body) retired; one `describe_symbol(package,
    key)` resolves any symbol via the already-kind-agnostic `readSymbol`/
    `DeclSource`, dispatches on `sym.Kind()`, and only adds `Methods` when
    `Kind == "type"`. Prerequisite check (done *before* writing any code,
    at Pedro's request) confirmed every piece already existed and was
    already kind-agnostic — this shipped as a pure tools-layer
    consolidation, zero engine changes.
  - **Real capability gain, not just a merge:** `var`/`const` had zero
    describe_* coverage before this — `list_symbols`'s own description
    said so. `describe_symbol` covers them for free, since nothing about
    it needs to refuse based on kind. Confirmed live (`DefaultScale`
    → `kind: "var"`, `KindCircle` → `kind: "const"`) and confirmed
    create/edit already fully supported const/var too (including grouped
    `const (...)`/`var (...)` blocks) — verified with a live scratch
    probe (create → edit → list_symbols → delete) plus existing
    `TestReplaceGroupedSpec` coverage. Nothing new needed there either.
  - **list_methods cross-file correctness, confirmed not a bug.** Pedro
    asked directly whether method lookup finds methods spread across
    multiple files in a package. Verified two ways: live (`list_methods`
    on this repo's own `Tx` type returned methods from
    creators.go/editors.go/refactorings.go/pipeline.go/snapshot.go — five
    files) and architecturally (`Package.RebuildIndex` folds every file's
    symbols into one shared package-level map, so `View.Methods`'s filter
    is cross-file by construction). No BUGS entry needed.
  - **Nomenclature: "Symbol" won over "Declaration" for the whole public
    surface**, resolving the split between read tools (already
    `list_symbols`/`SymbolEntry`/`SymbolKind`) and write tools (`create_
    declaration`/`edit_declaration`/`delete_declaration`/`rename_
    declaration`/`move_declaration`). Deciding factor: the *engine* already
    called these `Tx.CreateSymbol`/`DeleteSymbol`/`RenameSymbol`/
    `MoveSymbol` — only `Tx.ReplaceSymbol` and the tool-name layer had
    drifted to "Declaration." Renamed the tools to match the engine
    instead of the reverse:
    `create_declaration→create_symbol`, `edit_declaration→edit_symbol`,
    `delete_declaration→delete_symbol`, `rename_declaration→rename_symbol`,
    `move_declaration→move_symbol` (input types renamed to match:
    `CreateSymbolInput`, `EditSymbolInput`, etc.). `Tx.ReplaceSymbol`
    renamed `Tx.EditSymbol` for the same reason, once Pedro pointed out
    `edit_file`/`Tx.EditFile` already set the verb precedent and `Replace`
    would've been the one remaining mismatch. `keyNote` reworded
    ("declaration's address" → "symbol's address").
  - Reversed an earlier call from the same session: `symbolAt` had been
    renamed `declarationFromPos` (plus new `declarationFromLine`) at
    Pedro's direction to consolidate on "declaration" wording internally.
    With "Symbol" now the whole-codebase decision, swapped both back to
    `symbolFromPos`/`symbolFromLine` — "the code should tell the story,"
    consistency across all three pillars (nomenclature/organization/
    consistency) beats the earlier internal/public layering argument.
  - Two real, unrelated bugs caught and fixed by grepping for every old
    name across the repo rather than trusting the compiler alone (the
    compiler can't catch a stale string literal or comment): `Tx.
    CreateSymbol`'s own collision error still said `"use ReplaceSymbol"`
    — fixed to `"use EditSymbol"`. Two stale doc comments (`methodSignatures`
    citing `describe_type`; `DiagnosticEntry` citing all three retired
    describe_* tools) — reworded.
  - Tests: `TestDescribers` rewritten around `describe_symbol` (with new
    var/const cases — the point of the exercise); `TestMutationTools`,
    `TestAddressForms`, `TestExternalReadToolsAndRefusals` updated for the
    renamed tools/types. 52/52 passing, `gofmt`/`go vet` clean, two flush
    checkpoints (engine layer, then tools layer) rather than one batch at
    the end.
  - README: 28 → 26 tools (three describe_* collapsed to one, net −2).
    AGENTS.md's one `rename_declaration` mention fixed. ROADMAP's own
    still-open sections (Refactor engine overhaul, Batch mutations'
    collision description, the iota BUGS entry, the diff-splicing
    alternative) updated to current tool names; historical DONE entries
    elsewhere left as accurate point-in-time record, not rewritten.
- **describe_package / describe_file / edit_file (package-level docs),
  shipped 2026-07-13.** First item of the tools-surface work, implemented
  same day via gomcp self-hosted edits, live-verified after reconnect
  (`describe_package` on `internal/address` returned its real godoc;
  `describe_file` on `address_test.go` correctly returned empty).
  - `workspace.File.Doc()`/`workspace.Package.Doc()`: derived, never
    stored, same `RebuildIndex` philosophy as everything else in the
    model — `Package.Doc()` concatenates every file's own doc in file
    order (`Files()`'s existing sort order), not just the first found.
  - `engine.File`/`engine.Package` DTOs carry `doc` as a plain copied
    field, populated by `newFile`/`newPackage` — pure translation, no
    `View` access needed. Unlike the diagnostics attribution work, this
    one never needed the View-resolver detour, since a file's own doc
    comment is a fact about that file alone.
  - `Tx.CreateFile` gained a `doc string` parameter (empty means none);
    new `Tx.EditFile(pkg, name, doc)` replaces or clears a file's package
    doc comment via the same `offsetSpan`/`applySplices` machinery every
    other Editor uses, touching nothing else in the file. Both share a
    new `renderDocComment` helper (pipeline.go) that formats plain text as
    `// `-prefixed lines, bare `//` for blank lines per gofmt convention.
  - Tools: `describe_package` (godoc + file list), `describe_file` (one
    file's fragment alone), `edit_file`, registered under their
    README-matching categories. `diagsForFile` extracted to shared.go and
    reused by both `describeFile` and the pre-existing `listSymbols`
    file-filter branch, which had the same loop inline — one fewer
    duplicate.
  - Sandbox fixture extended for real coverage (not synthetic): package
    doc comments added to `shapes.go` and `groups.go` (two files, same
    package) specifically to exercise concatenation-in-file-order, since
    no existing fixture file had one. Raw-edited, since `testdata` is a
    separate module the connected server can't see or edit — confirmed
    empirically this session (`list_files` on it fails: not a workspace
    package, not resolvable as a dependency).
  - Real bug caught while writing tests, unrelated to this feature:
    `TestMutationTools`'s blast-radius assertion checked
    `d.Key == "Circle" || strings.Contains(d.Message, "Radius")` — neither
    ever matches (the real message says `"unknown field R"`, the old
    field name; the real `Key` is `"Circle.Area"`, not `"Circle"`). Fixed
    to check `d.File == "use/use.go"`, matching what the engine-layer
    equivalent (`TestReplaceSymbolBlastRadiusAndHealing`) already asserted
    correctly. Caught only because a debug test was written to print real
    diagnostic content rather than trusting the original guess.
  - README updated: 25 → 28 tools, `describe_package`/`describe_file`
    added to Describers, `edit_file` added to Editors.
  - **Friction point — significant, project-specific, not a batch-mutations
    data point.** A `/mcp` reconnect restarts the connected server process;
    anything edited via gomcp but not yet `flush`ed is discarded, reverting
    to disk state — same effect as `reload`, but triggered client-side, not
    via a tool call. Cost the entire first implementation pass (everything
    from the workspace layer through `Register`) when a reconnect happened
    mid-session before anything had been flushed. Recovered cheaply only
    because nothing had reached disk. Now flushing at safe checkpoints
    (after each layer compiles clean) rather than batching every change to
    one flush at the end. Pedro: reconnects needing this care is specific
    to gomcp's self-hosted workflow, not a general concern — noted here so
    it isn't forgotten, not filed as a batch-mutations argument.
  - Second friction point, is a batch-mutations data point: `create_declaration`
    still refuses more than one top-level declaration per call — hit again
    adding four `tools.go` types in one attempt. Same known gap, no new
    information.
- **Diagnostics speak addresses, not positions; `MutationOutput` →
  `WriteOutput`.** Full design agreed over several rounds 2026-07-13 (see
  git history / prior conversation for the back-and-forth), implemented
  same day via gomcp self-hosted edits. Summary of the shipped shape:
  - `engine.Diagnostic` drops `Line`/`Col`, gains `Package address.PkgPath`
    and `Key string` (the enclosing declaration's address, empty when
    unattributable). `workspace.Diagnostic` untouched — only the DTO
    translation at the gate changed.
  - `symbolAt` renamed `declarationFromPos`; new sibling
    `declarationFromLine` resolves a file:line coordinate the same way,
    line-keyed since no `token.Pos` survives into `Diagnostic`. Both
    resolve to the Declaration entity, never a bare string — `Key` is only
    ever produced by calling `.Key()` on what a resolver returns, kept
    deliberately distinct from "Declaration."
  - New `View.attributeDiagnostics` (diagnostics.go) does the per-item
    resolution for diagnostics that can span multiple declarations
    (`View.Diagnostics`, `WorkspaceDiagnostics`); `newDiagnostic`/
    `newDiagnostics` (dto.go) stay pure translators taking pre-resolved
    `pkg`/`key`, consistent with `newSymbol`/`newPackage`/`newFile`.
  - Tools layer: `DiagnosticEntry{Package, File, Key, Kind, Message}`
    (`Entry` suffix matching `SymbolEntry`/`MatchEntry`). `DiagBlock`
    becomes `{Diagnostics []DiagnosticEntry, Truncated *int}` — one field
    for truncation, not a bool/int pair, nil when nothing was hidden.
    Every prior `DiagBlock` consumer (list/describe/diagnostics tools)
    keeps its embedding unchanged. `MutationOutput` renamed `WriteOutput`
    ("mutation" no longer named this domain — verb categories are
    Creators/Editors/Refactorings); its two diagnostics fields became
    `*DiagBlock` (pointer, unlike the read-side embedding) specifically so
    an edit that introduces/resolves nothing — the common case — omits the
    field entirely instead of shipping an empty `{}`.
  - Real bug fixed as a side effect: `describeType` and `listMethods` used
    to concatenate each method's *already-truncated* diagnostics list, so
    a type with several over-limit methods could return multiple "+N more"
    trailers stitched into one list. Both now gather every method's raw
    diagnostics first and truncate once, which the structured `DiagBlock`
    (one `Truncated` field, not one per fragment) forced into the open.
  - Unplanned mid-implementation discovery: `File.Diags()` (engine DTO)
    broke because `newFile`/`newPackage` are pure functions with no `View`
    access, so they can't resolve a declaration for per-file diagnostics.
    Its only caller (`listSymbols`'s file-filtered branch) was rewritten to
    filter the already-attributed `View.Diagnostics(pkg)` by `File`
    instead, making `File.Diags()` dead code — deleted, along with the
    `diags` field on `File`. Not caught by the design review; only
    surfaced when the compiler did.
  - Tests: `TestReplaceSymbolBlastRadiusAndHealing` and `TestMutationTools`
    rewritten to assert on `Diagnostic`'s structured fields (`Kind`,
    `File`, `Key`) directly rather than parsing rendered strings — an
    improvement, not just a fix. `TestDiagStringsTruncation` renamed
    `TestDiagBlockTruncation`, rewritten for `diagBlock`/`diagBlockPtr`.
    `TestFindersAndDiagnostics` updated for `DiagnosticEntry`. 52/52
    passing, `gofmt`/`go vet` clean.
  - **Friction points — corrected 2026-07-13.** `create_declaration`/
    `edit_declaration` refusing a batch of 2 declarations in one call
    ("expected exactly one top-level declaration") is real batch-mutations
    evidence, hit twice this session firsthand. The other friction logged
    here originally — 11 separate `edit_declaration` calls to rename
    `MutationOutput`→`WriteOutput` — was **not** a gomcp gap: every one of
    those 11 functions only needed the type-reference change, nothing
    else, which is exactly `rename_declaration`'s job (it walks
    `TypesInfo().Uses` the same way `TestRenameSymbolPropagates` already
    proves works across files and qualified references — a generic type
    argument is an ordinary identifier reference to `go/types`, no
    different from any other use). One call would have done what 11
    `edit_declaration` calls did. Pedro pushed back on filing this as pure
    operator error, though — worth also asking whether the tool
    descriptions make it clear enough *when* `rename_declaration` is the
    right call instead of `edit_declaration`'s replace-by-name-collision
    path; see the Tools doc overhaul and Refactor engine overhaul TODO
    items, where this is now filed as a concrete finding rather than a
    floating note.
- **Diagnostics centralized; `Unrelated` field added.** The before/after
  delta math that used to live inline inside `Engine.Edit` (snapshot.go)
  is now `diffDiagnostics` in `diagnostics.go`, next to the rest of the
  aggregation logic it had been split from — `Edit` just calls it.
  `EditReport`/`MutationOutput` gained `Unrelated int`: the count of
  diagnostics present both before and after an edit, silent in
  `Delta`/`Resolved` by design, giving the agent a directional signal for
  whether a standalone `diagnostics()` call is worth making without
  drowning the echo in problems the edit didn't cause.
  `TestEditDeltaExcludesPreexistingBrokenness` (mutation_test.go) covers
  both "still-present diagnostics stay silent" and "bootstrap onto an
  already-broken workspace doesn't leak into the first edit's delta"
  against the sandbox's permanently-broken package. `gofmt`/`go vet`/
  `go test ./...` all clean (50/50). `MutationOutput` still carries the
  "Mutation" name pending the tool-interface rename decision (ROADMAP
  TODO).
- **main.go server Instructions enriched.** The `Instructions` string is the
  only guidance a connecting model gets — no AGENTS.md, no repo access.
  It covered the never-touch-files-directly rule but missed two things that
  change how a model should read tool output and write code: the
  diagnostics-scoping convention (every read tool's diagnostics field is
  scoped to what it read, not the whole workspace — `diagnostics` is the
  only uncapped inventory) and the floating-comment stance (a comment not
  attached to a declaration or package clause can silently vanish under a
  later edit, confirmed empirically this session). Added both, plus a
  one-line safety note that `reload` discards unflushed work.
- **Anti-corruption layer at the engine gate.** engine.go used to re-export
  workspace's whole vocabulary as aliases (`type Symbol = workspace.Symbol`),
  giving tools/cmd no real insulation from workspace's internal shape. Now:
  `RelativePath`/`PkgPath`/`CleanPath` live in a dependency-free leaf package
  (`internal/address`) that workspace, engine, and tools each depend on
  directly; `Symbol`/`Package`/`File`/`Diagnostic`/`SymbolKind`/`DiagKind`
  are real engine-owned types (dto.go) built from `*workspace.X` by `new*`
  translators at the one point each crosses the gate; `View`'s methods
  split into private, workspace-typed resolvers (internal AST/type-info
  work) and public methods that translate to the DTOs. No local
  convenience aliases survive anywhere (removed on review: an alias hides
  a type's origin regardless of whether the type is safe to leak — see
  AGENTS.md's "Aliasing is not free ergonomics" convention). `Unit` stays
  a plain `workspace.Unit` reference throughout engine, since it's
  structurally bound to the sealed `Package` type and can't be extracted.
  Byproduct: deleted `workspace.CleanPath` (dead wrapper) and `tombstone()`
  in mutation.go (dead overlay-mask builder, zero callers).
- **No section-banner comments.** Comment-based section banners
  (`// ----- Resolvers -----`) are gomcp-invisible and silently rot — found
  one mislabeling 8 public methods as "Internal helpers." Replaced with
  real per-category files throughout `internal/engine` and `internal/tools`
  (see AGENTS.md's Layout for the full file list); `read.go`/`edit.go` in
  tools hold only their relay functions, plus a `shared.go` per package for
  helpers genuinely called from both sides. In engine, `lookup.go` and
  `mutation.go` each ended up holding nothing but a lock-scoped entry point
  (`View`+`Read`, `Tx`+`EditReport`+`Edit`) — since both are really the
  same concept (the two ways anything gets access to Engine's state),
  merged them into one file, `snapshot.go`.
- **Post-refactor doc/test audit.** `workspace/doc.go` and
  `internal/address` had stale/missing package docs; `engine.go` carried a
  factually-wrong floating banner from before the alias removal — all
  fixed. Added `TestPublicViewSurface`: every prior lookup test exercised
  the private resolvers only, leaving the public DTO-returning methods
  (`Package`, `Symbol`, `DeclSource`, ...) covered solely through
  `tools_test.go` — a translator bug would have surfaced as a confusing
  tools-layer failure instead of at the engine layer that broke it.
  51/51 tests passing.
- **AGENTS.md simplified.** 240 → 187 lines. Layout no longer re-enumerates
  every engine/tools file — it points at `View`'s, `Tx`'s, and `tools.go`'s
  own doc comments instead, which now carry that breakdown themselves
  (redundant prose is one more place to go stale). Core invariants
  compressed from 6 numbered items with heavy explanation to 3
  by-construction + 2 discipline + 1 mixed, same facts. Cut a paragraph in
  Nomenclature that duplicated the Conventions section's aliasing bullet
  near-verbatim. Fixed a real inconsistency: the opening line claimed
  normative docs live in "section-header comments" — stale, since those
  are exactly what got eliminated this session.
- **tools.go tool descriptions reviewed.** Found two real issues:
  `rename_package`'s annotation title said "Move Package" (every other
  tool's title matches its name); and 5 tools that take a `Key` input
  (`edit_declaration`, `delete_declaration`, `move_declaration`,
  `rename_declaration`, `search_references`) never explained its format,
  even though `list_symbols`/`search_declarations_like` document it on the
  read side ("Type.Name" for methods) — a model calling a write tool
  without listing symbols first had no way to know. Fixed the title and
  added a shared `keyNote` constant, applied consistently.
- **Diagnostics truncation.** Every scoped `DiagBlock` is capped at
  `diagLimit` (`diagStrings` in tools/shared.go) and closed with a pointer
  back to the uncapped `diagnostics` tool when truncated. Cap is a
  process-wide flag, `-diagnostics-limit` (default 20).
- **State extraction.** The trusted core moved into
  `internal/engine/workspace`: unexported fields, named primitives only
  (SwapFile, AddLoadedFile, DropFile/MoveFile, InstallUnit/RemoveUnit, the
  tombstone family, Clone/Reset/SwapLoaded), one concept per file.
  Pruning logic, rechecks, and overlays are now properties of what compiles.
- **Two address styles for files, unified.** Package addresses are import
  paths everywhere; bare workspace directories are accepted and
  module-prefixed at the gate (`canonPkg`). File arguments are bare names
  within their package. Diagnostics strings stay `path:line:col` prose,
  not addresses.
- **Reconnect-to-refresh.** `reload` rebuilds from disk, discarding
  unflushed work (reported per package) — manual edits and git operations
  no longer force a reconnect.
- **Read-only dependency inspection by import path.** Any importable
  package — third-party or stdlib — answers `list_*`/`describe_*` by
  import path: exported API only, lazily cached with its own FileSet,
  negative results cached. Mutations refuse dependencies by name; semantic
  finders stay workspace-only (a dependency's type universe can't be
  matched exactly against the workspace's).
- **Floating comments confirmed harmful, not just risky.** A manually
  added header in `internal/tools/shared.go` silently vanished under an
  unrelated `move_declaration`/`create_declaration` rewrite of that file —
  floating comments aren't part of the tracked AST, so a file rewrite has
  nothing to reattach them to. Real package docs (directly above
  `package X`) survive the same rewrites untouched. This codebase commits
  to never needing floating comments at all.
